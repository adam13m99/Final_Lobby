#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

//! The LobbyBaz desktop window (D45).
//!
//! This is a shell, and deliberately a thin one. Everything the product does -
//! rooms, the tunnel, accounts, moderation - lives in the Go binary beside it,
//! which serves the interface over loopback. That split is worth stating
//! plainly because the obvious alternative was to rewrite the client in Rust:
//!
//!   - The Go client is the code that has actually carried a Dota match
//!     between two PCs. Rewriting it would put the one proven part of the
//!     system back to zero.
//!   - The same Go binary is still the thing the Windows service talks to,
//!     and still runs headless for testing. One implementation, one set of
//!     bugs.
//!   - What a browser page genuinely cannot do is exactly what is here: a
//!     window that is not a browser tab, a tray icon, and notifications that
//!     arrive while the window is hidden.
//!
//! So this process starts the Go binary, waits for it to say which loopback
//! address and token it is serving on, and points a webview at it.

use std::collections::HashSet;
use std::io::{BufRead, BufReader};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::time::Duration;

use tauri::menu::{Menu, MenuItem};
use tauri::tray::TrayIconBuilder;
use tauri::{Manager, RunEvent, WindowEvent};
use tauri_plugin_notification::NotificationExt;

/// How often the tray process asks the local server what is happening.
///
/// Slower than the interface's own poll on purpose. This loop exists to raise
/// notifications, and nothing here needs to be noticed within two seconds -
/// but it does keep running while the window is hidden, which is the whole
/// point of it being here rather than in the page.
const WATCH_EVERY: Duration = Duration::from_secs(5);

/// The child Go process, kept so it can be killed when the window closes.
/// A lobby server left running after its window is gone is a port held open
/// and a tunnel nobody is watching.
struct Server {
    child: Mutex<Option<Child>>,
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .manage(Server {
            child: Mutex::new(None),
        })
        .setup(|app| {
            build_tray(app.handle())?;

            let handle = app.handle().clone();
            std::thread::spawn(move || {
                match start_server(&handle) {
                    Ok(url) => {
                        if let Some(window) = handle.get_webview_window("main") {
                            // The splash is bundled; this is where the real
                            // interface takes over.
                            if let Ok(parsed) = url.parse() {
                                let _ = window.navigate(parsed);
                            }
                        }
                        watch(handle, url);
                    }
                    Err(why) => {
                        // Without the server there is no product, and a blank
                        // window explains nothing. Say so where it will be
                        // seen even if the window is already hidden.
                        let _ = handle
                            .notification()
                            .builder()
                            .title("LobbyBaz could not start")
                            .body(&why)
                            .show();
                    }
                }
            });
            Ok(())
        })
        .on_window_event(|window, event| {
            // Closing the window minimises to the tray (D45). A player who
            // closes the lobby while a friend is filling a room should still
            // hear about it - that is what the tray is for.
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .build(tauri::generate_context!())
        .expect("could not start the LobbyBaz window")
        .run(|handle, event| {
            if let RunEvent::Exit = event {
                if let Some(state) = handle.try_state::<Server>() {
                    if let Ok(mut child) = state.child.lock() {
                        if let Some(mut c) = child.take() {
                            let _ = c.kill();
                        }
                    }
                }
            }
        });
}

/// build_tray puts LobbyBaz in the notification area with two entries.
///
/// Two, not ten: the tray is where somebody goes to get the window back or to
/// actually quit, and every other entry is a thing they will click by mistake.
fn build_tray(app: &tauri::AppHandle) -> tauri::Result<()> {
    let show = MenuItem::with_id(app, "show", "Open LobbyBaz", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show, &quit])?;

    TrayIconBuilder::with_id("main")
        .icon(app.default_window_icon().unwrap().clone())
        .tooltip("LobbyBaz")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| match event.id.as_ref() {
            "show" => reveal(app),
            "quit" => app.exit(0),
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            // A left click is what everybody tries first.
            if let tauri::tray::TrayIconEvent::Click {
                button: tauri::tray::MouseButton::Left,
                button_state: tauri::tray::MouseButtonState::Up,
                ..
            } = event
            {
                reveal(tray.app_handle());
            }
        })
        .build(app)?;
    Ok(())
}

fn reveal(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

/// start_server launches the Go binary and returns the URL it is serving on.
///
/// The URL carries a per-run token, so it cannot be guessed by anything else
/// on the machine and there is no fixed port to collide with. The binary
/// prints it on its first line of output when asked to; reading it back is
/// more reliable than agreeing a port in advance and finding it taken.
fn start_server(app: &tauri::AppHandle) -> Result<String, String> {
    // Two places it can be, and the path has to be *checked*, not merely
    // built: resolve() composes a path whether or not anything is there, so
    // trusting it means a development run silently tries to execute a file
    // that does not exist and reports nothing useful.
    let installed = app
        .path()
        .resolve("binaries/lobbyapp.exe", tauri::path::BaseDirectory::Resource)
        .ok();
    let beside = std::env::current_exe()
        .ok()
        .map(|p| p.with_file_name("lobbyapp.exe"));

    let exe = [installed, beside]
        .into_iter()
        .flatten()
        .find(|p| p.exists())
        .ok_or_else(|| "the LobbyBaz server is missing from this install".to_string())?;

    let mut command = Command::new(&exe);
    command
        .arg("-url-only")
        .arg("-no-browser")
        .stdout(Stdio::piped());

    // CREATE_NO_WINDOW. Without it the Go binary gets its own console, which
    // flashes up behind the window on every start and sits in the taskbar
    // looking like a second copy of the app. It is a console application; it
    // just is not one a player should ever see.
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x0800_0000);
    }

    let mut child = command
        .spawn()
        .map_err(|e| format!("could not start {}: {e}", exe.display()))?;

    let stdout = child
        .stdout
        .take()
        .ok_or_else(|| "the LobbyBaz server produced no output".to_string())?;

    let mut line = String::new();
    BufReader::new(stdout)
        .read_line(&mut line)
        .map_err(|e| format!("could not read the server's address: {e}"))?;
    let url = line.trim().to_string();
    if !url.starts_with("http://127.0.0.1") {
        return Err(format!("the server reported an address we will not open: {url}"));
    }

    if let Some(state) = app.try_state::<Server>() {
        if let Ok(mut slot) = state.child.lock() {
            *slot = Some(child);
        }
    }
    Ok(url)
}

/// watch raises the desktop notifications: the two D45 asked for - a room
/// filling up, a host starting the match - and the three the owner added on
/// 2026-08-29 (D66): a room opening in the lobby, a friend coming online, and
/// the tunnel dropping under a player who is in a room.
///
/// It runs here rather than in the page because the page is not running when
/// the window is hidden in the tray, and hidden in the tray is exactly when a
/// notification is worth anything. A player who is watching the lobby can see
/// the room fill with their own eyes.
///
/// **Every one of them is edge-triggered against the previous poll.**
/// Level-triggering would notify every five seconds for as long as the
/// condition held, which is how a player learns to ignore notifications.
///
/// **Every one of them is switchable, and the switches are read here on every
/// poll** rather than captured once at start-up: somebody who turns a
/// notification off because it is interrupting them means now, not the next
/// time they restart the app.
fn watch(app: tauri::AppHandle, url: String) {
    let state_url = url.replace("/?t=", "/api/state?t=");
    let token = url.split("t=").nth(1).unwrap_or_default().to_string();

    let mut was_full = false;
    let mut was_playing = false;
    let mut was_connected = false;
    let mut known_rooms: HashSet<String> = HashSet::new();
    let mut online_friends: HashSet<String> = HashSet::new();
    let mut first = true;

    loop {
        std::thread::sleep(WATCH_EVERY);

        let body = match fetch(&state_url, &token) {
            Some(b) => b,
            None => continue,
        };
        let state: serde_json::Value = match serde_json::from_str(&body) {
            Ok(v) => v,
            Err(_) => continue,
        };

        // The switches, read fresh every poll. A missing "notify" object
        // means an older app server is answering, and the honest reading of
        // that is the behaviour it had: the two D45 notifications on, the
        // three that came later off.
        let on = |key: &str, fallback: bool| -> bool {
            state["notify"][key].as_bool().unwrap_or(fallback)
        };

        // --- the lobby: rooms that were not there a moment ago -----------
        //
        // Only joinable ones, and a room is remembered only while it stays
        // joinable: one that fills up and empties again is a fresh chance to
        // play, and saying so is the entire point of this notification.
        let mut rooms_now: HashSet<String> = HashSet::new();
        let mut opened: Vec<(String, String)> = Vec::new();
        if let Some(list) = state["rooms"].as_array() {
            for r in list {
                let id = match r["id"].as_str() {
                    Some(i) => i.to_string(),
                    None => continue,
                };
                if r["joinable"].as_bool() != Some(true) {
                    continue;
                }
                if !known_rooms.contains(&id) {
                    opened.push((
                        r["name"].as_str().unwrap_or("A room").to_string(),
                        r["host_nick"].as_str().unwrap_or("").to_string(),
                    ));
                }
                rooms_now.insert(id);
            }
        }

        // --- friends: who is online who was not --------------------------
        let mut friends_now: HashSet<String> = HashSet::new();
        let mut arrived: Vec<String> = Vec::new();
        if let Some(list) = state["friends"]["friends"].as_array() {
            for f in list {
                let id = match f["player_id"].as_str() {
                    Some(i) => i.to_string(),
                    None => continue,
                };
                if f["online"].as_bool() != Some(true) {
                    continue;
                }
                if !online_friends.contains(&id) {
                    arrived.push(
                        f["display_name"].as_str().unwrap_or("A friend").to_string(),
                    );
                }
                friends_now.insert(id);
            }
        }

        // --- this player's own room, and their own tunnel ----------------
        let room = &state["room"];
        let in_room = room.is_object();
        let full = in_room && room["seats"].as_i64().unwrap_or(0) >= 10;
        let playing = in_room && room["status"].as_str() == Some("locked_in_game");
        let connected = state["connected"].as_bool() == Some(true);

        // The first poll establishes what is already true. Announcing the
        // state of a room the player is looking at, the moment they open the
        // app, is noise - and without this the first poll would announce
        // every room already in the lobby and every friend already online.
        if first {
            was_full = full;
            was_playing = playing;
            was_connected = connected;
            known_rooms = rooms_now;
            online_friends = friends_now;
            first = false;
            continue;
        }

        if full && !was_full && on("room_full", true) {
            notify(&app, "The room is full", "All ten slots are taken.");
        }
        if playing && !was_playing && on("match_starts", true) {
            notify(&app, "The match is starting", "The host has locked the room.");
        }

        // A player already sitting in a room is not looking for another one.
        if !in_room && on("room_opens", false) {
            for (name, host) in &opened {
                let body = if host.is_empty() {
                    "A room just opened.".to_string()
                } else {
                    format!("{host} just opened a room.")
                };
                notify(&app, name, &body);
            }
        }

        if on("friend_online", false) {
            for name in &arrived {
                notify(&app, name, "is online.");
            }
        }

        // Only while they are in a room. Outside one there is no tunnel to
        // lose, and "not connected yet" is not the same event as "dropped".
        if in_room && was_connected && !connected && on("tunnel_drops", false) {
            notify(
                &app,
                "The connection to the room dropped",
                "Open LobbyBaz - the room is still there, and Reconnect is on the room screen.",
            );
        }

        was_full = full;
        was_playing = playing;
        was_connected = connected;
        known_rooms = rooms_now;
        online_friends = friends_now;
    }
}

fn notify(app: &tauri::AppHandle, title: &str, body: &str) {
    let _ = app.notification().builder().title(title).body(body).show();
}

/// fetch does one loopback GET.
///
/// Written by hand rather than pulling in an HTTP client: this asks one
/// address on this machine for a short JSON document, and a dependency for
/// that would cost more to carry - in build time, in binary size, and in
/// things to keep updated - than the twenty lines it replaces.
fn fetch(url: &str, token: &str) -> Option<String> {
    use std::io::{Read, Write};
    use std::net::TcpStream;

    let rest = url.strip_prefix("http://")?;
    let (authority, path) = match rest.find('/') {
        Some(i) => (&rest[..i], &rest[i..]),
        None => (rest, "/"),
    };

    let mut stream = TcpStream::connect(authority).ok()?;
    stream.set_read_timeout(Some(Duration::from_secs(5))).ok()?;
    let request = format!(
        "GET {path} HTTP/1.0\r\nHost: {authority}\r\nX-Lobby-Token: {token}\r\nConnection: close\r\n\r\n"
    );
    stream.write_all(request.as_bytes()).ok()?;

    let mut raw = String::new();
    stream.read_to_string(&mut raw).ok()?;
    // HTTP/1.0 with Connection: close means the body is everything after the
    // blank line and there is no chunked encoding to unpick.
    let body = raw.split_once("\r\n\r\n")?.1;
    Some(body.to_string())
}
