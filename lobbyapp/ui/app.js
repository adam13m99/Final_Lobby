// LobbyBaz - the lobby (D42).
//
// The page is a renderer. It polls one endpoint, draws what came back, and
// posts actions. It holds no state of its own beyond which screen is showing,
// which filter is set, and what the player is halfway through typing, so a
// refresh never loses anything and there is no second copy of the truth to
// drift.
//
// No user-facing text is written in this file. Every string is t("some.key"),
// resolved from strings/<lang>.json by i18n.js, and lobbyapp/ui_test.go fails
// the build if a key is missing or a quoted sentence appears here (D44).

const TOKEN = new URLSearchParams(location.search).get("t") || "";
const POLL_MS = 2000;

let state = {};
let screen = "lobby";
let chatTab = "lobby";
let filter = "all";
let query = "";
let busy = false;
let dmWith = null;
let authMode = "signin";

const $ = (id) => document.getElementById(id);

// --------------------------------------------------------------- plumbing

async function api(path, body) {
  const res = await fetch(path, {
    method: body === undefined ? "GET" : "POST",
    headers: { "X-Lobby-Token": TOKEN, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let data = {};
  try { data = await res.json(); } catch (_) { /* empty body */ }
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

async function act(fn) {
  if (busy) return;
  busy = true;
  try {
    await fn();
    banner("");
    await refresh();
  } catch (e) {
    banner(e.message);
  } finally {
    busy = false;
  }
}

// banner shows a sentence across the top.
//
// Some of what lands here is an error message from the service or the
// coordinator, which arrives already written in English and cannot go through
// the lookup from here. Translating those means translating them at their
// source, in Go; that is a separate surface and a later task. Everything this
// file writes itself goes through t().
function banner(msg) {
  const b = $("banner");
  b.textContent = msg;
  b.classList.toggle("hidden", !msg);
}

// needName stops an action that cannot be done anonymously and asks for
// whatever this server requires, saying which action prompted it. Returns
// true if it stopped.
//
// Browsing needs nothing. Sitting in somebody's room, or talking in it, does:
// the other nine people are entitled to know who they are playing with.
//
// What it asks for depends on the server. A coordinator with no database has
// no accounts, and there a typed name is all there is and all that is asked
// for - the app has to keep working against the server that is running today.
function needName(why) {
  if (state.accounts ? state.signed_in : state.named) return false;
  gateMode(authMode);
  $("namewhy").textContent = t(why);
  $("nameerr").textContent = "";
  $("namegate").classList.remove("hidden");
  ($("accountform").classList.contains("hidden") ? $("nameinput") : $("a-user")).focus();
  return true;
}

// gateMode switches the gate between its three shapes: a name, signing in, or
// creating an account.
function gateMode(mode) {
  authMode = mode;
  const accounts = !!state.accounts;
  $("nickonly").classList.toggle("hidden", accounts);
  $("accountform").classList.toggle("hidden", !accounts);
  for (const f of document.querySelectorAll(".signup-only")) {
    f.classList.toggle("hidden", !accounts || mode !== "signup");
  }
  for (const tab of document.querySelectorAll(".modetabs .chattab")) {
    tab.classList.toggle("active", tab.dataset.mode === mode);
  }
  $("a-pass").setAttribute("autocomplete",
    mode === "signup" ? "new-password" : "current-password");
  $("gatego").textContent = t(!accounts ? "namegate.submit"
    : mode === "signup" ? "auth.signup" : "auth.signin");
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// el builds an element with text already in it. Used wherever the content is
// somebody's name or somebody's sentence, so it can never be markup.
function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// ------------------------------------------------------------- portraits

// Nobody uploads a picture, and asking them to would be one more thing
// standing between installing the app and finding a game. A person is drawn
// instead from what we already have: their initials, on a colour derived from
// their account id.
//
// The colour comes from the id rather than the name, so the same person is
// the same colour on every machine, two players called "Pudge" are still
// told apart, and changing a display name does not change a face.

function initials(name) {
  const words = String(name || "").trim().split(/\s+/).filter(Boolean);
  if (!words.length) return "?";
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
  return (words[0][0] + words[words.length - 1][0]).toUpperCase();
}

// hueOf is a small deterministic hash. It is not security and does not need
// to be: the worst outcome of a collision is two people being the same
// colour, which their initials and names still separate.
function hueOf(id) {
  let h = 0;
  const s = String(id || "");
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

function avatar(name, id, cls) {
  const e = el("div", "avatar" + (cls ? " " + cls : ""), initials(name));
  const h = hueOf(id);
  // Held to one band of lightness and saturation so every face sits at the
  // same weight against the panel and none of them shouts.
  e.style.background =
    "linear-gradient(145deg, hsl(" + h + " 44% 42%), hsl(" + ((h + 26) % 360) + " 46% 30%))";
  e.title = name || "";
  return e;
}

// ----------------------------------------------------------------- render

function render() {
  const s = state;

  pill("p-service", s.service, t(s.service ? "status.service.up" : "status.service.down"));
  $("p-online").textContent = t("status.online", { n: s.online ?? 0 });
  $("menick").textContent = s.nick || t("status.dash");
  $("memmr").textContent = s.mmr ? t("status.mmr", { n: s.mmr }) : t("status.nomm");

  renderConnection(s);

  // The lobby is browsable before signing up (D45). Nothing is asked for
  // until the player does something that needs a name, so somebody who just
  // installed the app can see whether anyone is playing before deciding
  // whether to bother. Asking first is how an install gets abandoned.

  banner(s.service_error || s.coordinator_error || s.tunnel_error ||
    (s.room_gone ? t("err.room_gone") : "") ||
    (s.removed ? t("err.removed") : "") ||
    (s.build_warning || ""));

  // Terms that changed after somebody signed up are terms they have not
  // agreed to. Shown only where all three facts are known: signed in, a
  // version in force, and this account not on it.
  $("termsmoved").classList.toggle("hidden",
    !(s.signed_in && s.terms_version && s.terms_accepted === false));

  renderUpdate(s.update);
  renderAds(s.banners || []);

  const inRoom = !!s.room_id && !!s.room;
  $("roomtab").disabled = !inRoom;
  $("chattab-room").disabled = !inRoom;
  if (!inRoom && screen === "room") show("lobby");
  if (!inRoom && chatTab === "room") showChat("lobby");

  renderRooms(s.rooms || []);
  renderChat("lobbylog", s.lobby_chat || []);
  renderFriends(s.friends, s.friends_error);

  if (inRoom) {
    renderRoom(s.room);
    renderChat("roomlog", s.room_chat || []);
  }
  renderDiag();
  renderMod(s);
}

function pill(id, ok, text) {
  const e = $(id);
  e.textContent = text;
  e.className = "pill " + (ok === "wait" ? "wait" : ok ? "ok" : "bad");
}

// The toolbar's connection light (D42.5). Being in a room and being on its
// network are different states, and this is the permanent reminder of which
// one you are in.
function renderConnection(s) {
  let cls = "bad", key = "status.tunnel.off";
  if (s.connected) {
    cls = "ok"; key = "status.tunnel.on";
  } else if (s.tunnel === "connecting") {
    cls = "wait"; key = "status.tunnel.connecting";
  }
  $("conndot").className = "dot " + cls;
  $("conntext").textContent = t(key);
}

function renderUpdate(u) {
  const e = $("update");
  if (!u) { e.classList.add("hidden"); return; }
  e.classList.remove("hidden");
  e.textContent = "";
  if (u.error) {
    e.appendChild(el("span", "", t("update.failed", { version: u.version, error: u.error })));
  } else if (u.ready) {
    e.appendChild(el("span", "", t("update.ready", { version: u.version })));
    const b = el("button", "", t("update.install"));
    b.onclick = () => act(() => api("/api/update", {}));
    e.appendChild(b);
  } else {
    e.appendChild(el("span", "", t("update.downloading", { version: u.version })));
  }
}

// renderAds draws the announcement strip (D42.7).
//
// Every field here is text one member of staff typed and every other player's
// client displays, which is the exact shape of a stored scripting hole. It is
// built out of elements with textContent rather than markup, and the link is
// only followed if the server said it was http or https.
function renderAds(ads) {
  const box = $("banners");
  box.textContent = "";
  for (const a of ads) {
    const card = el("div", "ad");
    card.appendChild(el("strong", "", a.title || ""));
    if (a.body) card.appendChild(el("span", "", a.body));
    if (a.link_url) {
      const link = el("a", "", t("banner.more"));
      link.href = a.link_url;
      link.target = "_blank";
      link.rel = "noreferrer noopener";
      card.appendChild(link);
    }
    box.appendChild(card);
  }
}

// ------------------------------------------------------------- room list

// visible applies the search box and the filter chips (D42.6).
//
// Both are done here rather than on the server: the whole list already
// arrives on every poll, it is a few dozen rooms, and filtering locally means
// typing in the search box is instant instead of waiting two seconds for the
// next sync.
function visible(rooms) {
  const q = query.trim().toLowerCase();
  return rooms.filter((r) => {
    if (q) {
      const hay = [r.name, r.host_nick, r.description].join(" ").toLowerCase();
      if (!hay.includes(q)) return false;
    }
    switch (filter) {
      case "joinable":
        return r.joinable && r.seats < 10;
      case "waiting":
        return r.status === "open_to_new_players" || r.status === "open";
      case "mine":
        // Rooms this player would actually be let into: the MMR floor is the
        // one door that silently excludes somebody who has not tried it.
        return !r.min_mmr || (state.mmr || 0) >= r.min_mmr;
      default:
        return true;
    }
  });
}

function renderRooms(rooms) {
  const box = $("roomlist");
  box.textContent = "";
  const shown = visible(rooms);
  if (!shown.length) {
    // An empty list says so in the middle of its own space. A single grey
    // line against the top edge reads as a page that failed rather than as a
    // lobby with nothing in it yet.
    const none = el("div", "nothing");
    none.appendChild(el("div", "big", "▦"));
    none.appendChild(el("p", "", t(rooms.length ? "lobby.nomatch" : "lobby.empty")));
    box.appendChild(none);
    return;
  }
  for (const r of shown) box.appendChild(roomCard(r));
}

// pingCell is the lobby's latency column (D54).
//
// It is the *host's* round trip to the relay, never the reader's - a player
// in the lobby has no path to a host they have not joined, so no other number
// exists. Both the column heading and every cell say so, because somebody who
// reads it as their own ping will blame the wrong thing when a game plays
// badly. Zero means the host has not reported one, which is shown as unknown
// rather than as an excellent connection.
function pingCell(r) {
  const ms = r.host_relay_ms || 0;
  const grade = !ms ? "unknown" : ms < 60 ? "good" : ms < 140 ? "fair" : "poor";
  const outer = el("div", "stat");
  const cell = el("div", "room-ping " + grade);
  outer.appendChild(cell);
  outer.title = ms ? t("lobby.ping.explain") : t("lobby.ping.none");

  // Three rising bars, the shape every network indicator uses, with the
  // number beside them. The bars are read at a glance and the number is read
  // when it matters; neither replaces the other.
  const lit = grade === "good" ? 3 : grade === "fair" ? 2 : grade === "poor" ? 1 : 0;
  const bars = el("div", "bars");
  for (let i = 0; i < 3; i++) bars.appendChild(el("i", i < lit ? "lit" : ""));
  cell.appendChild(bars);
  cell.appendChild(el("span", "n",
    ms ? t("lobby.ping.value", { n: ms }) : t("lobby.ping.unknown")));
  return outer;
}

// seatCell draws the ten playing slots as ten marks.
//
// Somebody scanning the lobby is asking one question - is there room for me -
// and a bar answers it in the time the eye takes to pass over it. "7/10"
// makes them read and subtract. The number stays underneath for the times
// they want to be exact.
function seatCell(r) {
  const cell = el("div", "stat");
  const taken = r.seats || 0;
  const mine = r.id === state.room_id;
  const pips = el("div", "pips");
  for (let i = 0; i < 10; i++) {
    pips.appendChild(el("i", "pip" + (i < taken ? (mine ? " you" : " on") : "")));
  }
  cell.appendChild(pips);
  const count = el("div", "room-count");
  count.appendChild(el("b", "", String(taken)));
  count.appendChild(el("span", "", t("lobby.of10")));
  cell.appendChild(count);
  return cell;
}

function roomCard(r) {
  const card = el("div", "room");
  // A stripe on the inline edge, coloured by whether this player can actually
  // get in. It is the only part of the row that can be read without looking
  // directly at it, so it carries the one fact that decides everything else.
  card.classList.add(r.joinable && r.id !== state.room_id ? "can-join" : "shut");

  const main = el("div", "room-main");
  main.appendChild(avatar(r.host_nick, r.host_id));
  card.appendChild(main);

  // Column 1: name, door, status, the host's sentence, and who is in it.
  const about = el("div", "grow");
  const title = el("div", "room-title");
  title.appendChild(el("strong", "", r.name));
  title.appendChild(statusBadge(r.status));
  if (r.needs_password) title.appendChild(el("span", "tag lock", t("lobby.door.password")));
  if (r.privacy === "friends") title.appendChild(el("span", "tag lock", t("lobby.door.friends")));
  if (r.privacy === "invite") title.appendChild(el("span", "tag lock", t("lobby.door.invite")));
  about.appendChild(title);

  about.appendChild(el("div", "room-desc",
    r.description || t("lobby.hostedby", { host: r.host_nick })));

  const who = el("div", "room-players");
  for (const m of r.members || []) {
    if (m.spectator) continue;
    const chip = el("span", "tag" + (m.is_host ? " host" : ""));
    chip.appendChild(el("b", "", m.nick));
    if (m.mmr) chip.appendChild(el("span", "", t("lobby.player.mmr", { mmr: m.mmr })));
    who.appendChild(chip);
  }
  about.appendChild(who);
  main.appendChild(about);

  // Columns 2-4: the numbers a player chooses a room on.
  card.appendChild(seatCell(r));
  card.appendChild(mmrCell(r));
  card.appendChild(pingCell(r));
  card.appendChild(roomActions(r));
  return card;
}

// mmrCell separates the number from what it means, because the two are read
// at different moments: the figure tells a player whether they belong here,
// the label tells them whether it is a floor they must clear or an average
// they are being compared against.
function mmrCell(r) {
  const cell = el("div", "stat");
  const box = el("div", "mmr" + (r.min_mmr ? " floor" : ""));
  if (r.min_mmr) {
    box.appendChild(el("span", "v", String(r.min_mmr)));
    box.appendChild(el("span", "k", t("lobby.mmr.min")));
  } else if (r.avg_mmr) {
    box.appendChild(el("span", "v", String(r.avg_mmr)));
    box.appendChild(el("span", "k", t("lobby.mmr.avg")));
  } else {
    box.appendChild(el("span", "k", t("lobby.mmr.any")));
  }
  cell.appendChild(box);
  return cell;
}

function roomActions(r) {
  const acts = el("div", "room-actions");
  if (r.id === state.room_id) {
    const open = el("button", "primary tiny", t("room.open"));
    open.onclick = () => show("room");
    acts.appendChild(open);
    return acts;
  }

  const join = el("button", "primary tiny", t("room.join"));
  join.disabled = !r.joinable || !!state.room_id;
  join.title = state.room_id ? t("room.join.busy")
    : r.joinable ? "" : t("room.join.closed");
  join.onclick = () => joinRoom(r);
  acts.appendChild(join);

  // No spectate button here. Watching is an admin's seat and an observer's
  // deliberate choice, not something to offer beside Join on every row - it
  // was one more thing to read on a line that has to be read at a glance.
  return acts;
}

// joinRoom asks for the password only when the room actually has one, so an
// open room is one click and a locked one is honest about why it is asking.
function joinRoom(r) {
  if (needName("namegate.why.join")) return;
  let password = "";
  if (r.needs_password) {
    password = window.prompt(t("lobby.door.ask")) || "";
    if (!password) return;
  }
  act(() => api("/api/rooms/join", { room_id: r.id, password }));
}

function statusClass(status) {
  if (status === "locked_in_game" || status === "closed") return "badge locked";
  if (status === "open_to_new_players") return "badge replace";
  return "badge";
}

function statusLabel(status) {
  if (status === "locked_in_game") return t("room.status.locked");
  if (status === "open_to_new_players") return t("room.status.replacing");
  if (status === "closed") return t("room.status.closed");
  return t("room.status.open");
}

function statusBadge(status) {
  return el("span", statusClass(status), statusLabel(status));
}

// ------------------------------------------------------------------ room

function renderRoom(r) {
  $("room-face").textContent = "";
  $("room-face").appendChild(avatar(r.host_nick, r.host_id, "lg"));
  $("room-name").textContent = r.name;
  $("room-sub").textContent = r.avg_mmr
    ? t("room.meta.mmr", { seats: r.seats, host: r.host_nick, mmr: r.avg_mmr })
    : t("room.meta", { seats: r.seats, host: r.host_nick });
  const badge = $("room-status");
  badge.className = statusClass(r.status);
  badge.textContent = statusLabel(r.status);

  const iAmHost = !!state.is_host;
  $("hostcontrols").classList.toggle("hidden", !iAmHost);
  $("describeform").classList.toggle("hidden", !iAmHost);
  // Only fill the box when it is not being typed in, or every poll would
  // overwrite what the host is halfway through writing.
  if (document.activeElement !== $("describe")) $("describe").value = r.description || "";
  $("doorform").classList.toggle("hidden", !iAmHost);
  if (iAmHost) drawDoor(r);

  const seated = {};
  for (const m of r.members || []) if (!m.spectator) seated[m.slot] = m;

  // Slots 0-4 are Radiant and 5-9 are Dire, which is how the game itself
  // divides them. Drawing all ten in one list hid the only structural fact
  // about a room: which five you would be joining.
  const box = $("slots");
  box.textContent = "";
  box.appendChild(teamColumn("radiant", "room.team.radiant", 0, seated, iAmHost));
  box.appendChild(teamColumn("dire", "room.team.dire", 5, seated, iAmHost));

  const specs = (r.members || []).filter((m) => m.spectator);
  const sbox = $("spectators");
  sbox.textContent = "";
  if (!specs.length) sbox.appendChild(el("p", "muted small", t("room.spectators.none")));
  for (const m of specs) sbox.appendChild(slotCard(m.slot, m, false, true));

  const bits = [];
  if (state.virtual_ip) bits.push(t("net.you", { ip: state.virtual_ip }));
  if (state.host_ip) bits.push(t("net.host", { ip: state.host_ip }));
  if (state.relay_ms) bits.push(t("net.relay", { n: state.relay_ms }));
  if (state.adapter) bits.push(state.adapter);
  $("netinfo").textContent = bits.join("  ·  ");

  $("btn-connect").disabled = !!state.connected;
  $("btn-disconnect").disabled = !state.connected;
  $("btn-play").disabled = !state.connected;
  $("btn-play").title = state.connected ? "" : t("room.play.note");

  drawNetBanner();
}

// drawDoor fills the host's door controls from the room.
//
// The password is never filled in, because the coordinator never sends it
// back and should not: what is on screen would then be a guess at a secret.
// An empty box means "leave it as it is"; typing in one changes it.
function drawDoor(r) {
  const door = r.privacy || "public";
  if (document.activeElement !== $("door")) $("door").value = door;
  if (document.activeElement !== $("doormmr")) {
    $("doormmr").value = r.min_mmr ? String(r.min_mmr) : "";
  }
  $("doorpass").classList.toggle("hidden", $("door").value !== "password");
  $("doorpass").placeholder = r.needs_password
    ? t("door.password.keep") : t("door.password.placeholder");
  $("doornow").textContent = t("door.now", { door: t("door." + door) });
}

// drawNetBanner says, in words and where it cannot be missed, whether this
// player is actually on the room's network.
//
// Joining a room now connects on its own, so most of the time this reassures
// rather than instructs. It matters when that fails: a player who starts Dota
// themselves gets no other warning, and the failure would otherwise reach
// them minutes later as an error inside the game.
function drawNetBanner() {
  const e = $("netbanner");
  e.hidden = false;
  if (state.connected) {
    e.className = "netbanner ok";
    e.textContent = state.virtual_ip
      ? t("net.on", { ip: state.virtual_ip })
      : t("net.on.noip");
    return;
  }
  if (state.connect_error) {
    e.className = "netbanner bad";
    e.textContent = t("net.failed", { error: state.connect_error });
    return;
  }
  if (state.tunnel === "connecting") {
    e.className = "netbanner wait";
    e.textContent = t("net.connecting");
    return;
  }
  e.className = "netbanner bad";
  e.textContent = t("net.off");
}

// teamColumn draws one side: a heading in that side's colour, how many of
// its five seats are taken, and the seats themselves.
function teamColumn(side, titleKey, first, seated, canKick) {
  const col = el("div", "team " + side);
  const head = el("div", "team-head");
  head.appendChild(el("span", "swatch"));
  head.appendChild(el("span", "", t(titleKey)));
  let taken = 0;
  for (let i = first; i < first + 5; i++) if (seated[i]) taken++;
  head.appendChild(el("span", "n", t("room.team.count", { n: taken })));
  col.appendChild(head);
  for (let i = first; i < first + 5; i++) {
    col.appendChild(slotCard(i, seated[i], canKick));
  }
  return col;
}

function slotCard(index, member, canKick, spectator) {
  const card = el("div");
  const mine = member && member.player_id === state.player_id;
  card.className = "slot" + (member ? "" : " empty") + (mine ? " you" : "");
  card.appendChild(el("div", "slot-num", spectator ? "S" + (index + 1) : String(index + 1)));

  if (member) card.appendChild(avatar(member.nick, member.player_id, "sm"));

  const body = el("div", "slot-body");
  card.appendChild(body);
  if (!member) {
    body.appendChild(el("div", "slot-name muted", t("room.slot.empty")));
    return card;
  }

  const mmr = member.mmr ? t("status.mmr", { n: member.mmr }) : t("status.nomm");
  body.appendChild(el("div", "slot-name",
    mine ? t("room.slot.you", { name: member.nick }) : member.nick));
  body.appendChild(el("div", "slot-sub",
    member.is_host ? t("room.slot.host", { sub: mmr }) : mmr));

  if (canKick && !member.is_host && !mine) {
    const b = el("button", "", t("room.kick"));
    b.title = t("room.kick.note");
    b.onclick = () => act(() => api("/api/rooms/kick", { target: member.player_id }));
    card.appendChild(b);
  }
  return card;
}

// ---------------------------------------------------------- friends rail

// renderFriends draws the rail (D42.2).
//
// A coordinator running without a database has no accounts and therefore no
// friends list. That is the state the live server is in, and it has to read
// as "not on this server" rather than as an error the player caused - so the
// rail explains itself and the rest of the lobby carries on.
function renderFriends(list, why) {
  const box = $("friendlist");
  box.textContent = "";

  if (!list) {
    // Three different absences, and they mean different things to a player:
    // this server has no friends list at all, you have not signed in yet, or
    // it simply has not arrived. Saying "unavailable" for all three would
    // send somebody looking for a fault that is not there.
    let key = "friends.loading";
    if (state.accounts && !state.signed_in) key = "friends.signin";
    else if (state.accounts === false) key = "friends.unavailable";
    else if (why) key = "friends.unavailable";
    box.appendChild(el("p", "muted small friend-empty", t(key)));
    return;
  }

  const waiting = list.incoming || [];
  const friends = list.friends || [];

  if (waiting.length) {
    box.appendChild(el("h3", "friend-group", t("friends.requests")));
    for (const f of waiting) box.appendChild(requestRow(f));
  }

  if (!friends.length) {
    box.appendChild(el("p", "muted small friend-empty", t("friends.none")));
    return;
  }
  box.appendChild(el("h3", "friend-group", t("friends.yours")));
  // Online first: a friend who is not there is not someone you can play with
  // now, and the rail is for deciding who to play with now.
  const sorted = friends.slice().sort((a, b) => (b.online ? 1 : 0) - (a.online ? 1 : 0));
  for (const f of sorted) box.appendChild(friendRow(f));
}

// whereabouts is the line under a friend's name. It answers one question:
// can I play with this person right now?
//
// The server sends a room id, not a room name - it has no reason to duplicate
// the lobby list into the friends list. The name is looked up from the rooms
// already on screen, and a friend in a room this player cannot see (a private
// one) is simply "in a room", which is the truth.
function whereabouts(f) {
  if (!f.online) return t("friends.offline");
  if (f.in_game) return t("friends.ingame");
  if (f.room_id) {
    const room = (state.rooms || []).find((r) => r.id === f.room_id);
    return room ? t("friends.inroom", { room: room.name }) : t("friends.inroomhidden");
  }
  return t("friends.online");
}

function friendRow(f) {
  const row = el("div", "friend");
  // Presence sits on the face rather than beside the name. The rail is narrow
  // and a separate column costs a word off every name in it.
  const port = el("div", "who-av");
  port.appendChild(avatar(f.display_name || f.player_id, f.player_id, "sm"));
  port.appendChild(el("span",
    "live" + (f.online ? (f.in_game ? " game" : " on") : "")));
  row.appendChild(port);

  const who = el("div", "who");
  who.appendChild(el("div", "name", f.display_name || f.player_id));
  who.appendChild(el("div", "where", whereabouts(f)));
  row.appendChild(who);

  if (f.unread) row.appendChild(el("span", "unread", String(f.unread)));

  const acts = el("div", "acts");
  const msg = el("button", "tiny", t("friends.message"));
  msg.onclick = () => openConversation(f);
  acts.appendChild(msg);

  if (state.room_id) {
    const inv = el("button", "tiny", t("friends.invite"));
    inv.onclick = () => act(() => api("/api/friends/invite", { target_id: f.player_id }));
    acts.appendChild(inv);
  }
  row.appendChild(acts);
  return row;
}

function requestRow(f) {
  const row = el("div", "friend");
  const who = el("div", "who");
  who.appendChild(el("div", "name", f.display_name || f.player_id));
  who.appendChild(el("div", "where", t("friends.wants")));
  row.appendChild(who);

  const acts = el("div", "acts");
  acts.style.opacity = "1";
  const yes = el("button", "primary tiny", t("friends.accept"));
  yes.onclick = () => act(() => api("/api/friends", { action: "accept", target_id: f.player_id }));
  const no = el("button", "tiny", t("friends.decline"));
  no.onclick = () => act(() => api("/api/friends", { action: "decline", target_id: f.player_id }));
  acts.appendChild(yes);
  acts.appendChild(no);
  row.appendChild(acts);
  return row;
}

// --- one friend's private conversation ----------------------------------

async function openConversation(f) {
  dmWith = f;
  $("dmwho").textContent = f.display_name || f.player_id;
  $("dmgate").classList.remove("hidden");
  $("dmlog").textContent = "";
  await loadConversation();
  $("dminput").focus();
}

async function loadConversation(send) {
  if (!dmWith) return;
  try {
    const out = await api("/api/friends/messages",
      { target_id: dmWith.player_id, send: send || "" });
    const log = $("dmlog");
    log.textContent = "";
    // A private message carries who sent it but not their name - the server
    // does not repeat what both ends already know. There are only two people
    // in the conversation, so anything not from them is from me.
    for (const m of out.messages || []) {
      const theirs = m.from_id === dmWith.player_id;
      const line = el("div", "msg");
      line.appendChild(el("span", "who" + (theirs ? "" : " self"),
        t("chat.said", { name: theirs ? (dmWith.display_name || dmWith.player_id) : (state.nick || t("chat.me")) })));
      line.appendChild(document.createTextNode(" " + m.body));
      log.appendChild(line);
    }
    log.scrollTop = log.scrollHeight;
  } catch (e) {
    banner(e.message);
  }
}

// ------------------------------------------------------------------ chat

function renderChat(id, msgs) {
  const log = $(id);
  // Only redraw when something changed, or the log scrolls away from under
  // the reader every two seconds.
  const sig = msgs.length ? msgs[msgs.length - 1].id + ":" + msgs.length : "0";
  if (log.dataset.sig === sig) return;
  log.dataset.sig = sig;

  const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  log.textContent = "";
  for (const m of msgs) {
    if (m.system) {
      log.appendChild(el("div", "msg system", m.text));
      continue;
    }
    const line = el("div", "msg");
    line.appendChild(el("span", "who" + (m.player_id === state.player_id ? " self" : ""),
      t("chat.said", { name: m.nick })));
    line.appendChild(document.createTextNode(" " + m.text));
    log.appendChild(line);
  }
  if (atBottom) log.scrollTop = log.scrollHeight;
}

// showChat switches the tab strip (D42.3). Party is present and honest about
// itself: parties are not built, and a tab that silently does nothing is
// worse than one that says so.
function showChat(which) {
  chatTab = which;
  for (const id of ["lobbylog", "roomlog", "partylog"]) {
    $(id).classList.add("hidden");
  }
  $(which === "room" ? "roomlog" : which === "party" ? "partylog" : "lobbylog")
    .classList.remove("hidden");
  for (const tab of document.querySelectorAll(".chattab")) {
    tab.classList.toggle("active", tab.dataset.chat === which);
  }
  $("chatform").classList.toggle("hidden", which === "party");
}

// ----------------------------------------------------------- diagnostics

function renderDiag() {
  $("btn-diag").disabled = !!state.diag_running;
  $("btn-diag").textContent = t(state.diag_running ? "checks.running" : "checks.run");

  const checks = state.diagnostics;
  if (!checks) return;

  const box = $("diaglist");
  box.textContent = "";
  for (const c of checks) {
    const row = el("div", "check " + (c.ok ? "ok" : "bad"));
    row.appendChild(el("div", "mark", c.ok ? "✓" : "✗"));
    const body = el("div", "body");
    body.appendChild(el("div", "name", c.name));
    if (c.detail) body.appendChild(el("div", "detail", c.detail));
    row.appendChild(body);
    if (c.ms) row.appendChild(el("div", "ms", t("checks.ms", { n: c.ms })));
    box.appendChild(row);
  }

  if (state.diag_at) {
    $("diagwhen").textContent = t("checks.when", {
      time: new Date(state.diag_at).toLocaleTimeString(I18n.lang),
    });
  }
}

// ------------------------------------------------------------ moderation

// What T8 built, with a door on it (D43, D47).
//
// Nothing here is a permission check. Every call goes to the coordinator,
// which refuses anybody whose session holds no role; this only decides what
// is worth drawing. Hiding the toolbar entry from a player is a courtesy, and
// treating it as a defence would be a mistake.
//
// Everything a moderator writes down travels with the action. The audit log
// is read months later by somebody who was not there, and "banned" with no
// reason beside it is a row that cannot be reviewed, appealed, or defended.

let modRecord = null;
let modLabels = [];
// Who is on screen, remembered so an action can redraw the record it just
// changed. The username field is not it: a moderator types a name, acts, then
// edits the field to look somebody else up, and the redraw would follow the
// half-typed name instead of the person they just banned.
let lastLookedUp = "";

function renderMod(s) {
  const role = s.role || "";
  $("modtab").classList.toggle("hidden", !role);
  if (!role) {
    if (screen === "mod") show("lobby");
    return;
  }
  $("mod-role").textContent = t(role === "head_admin" ? "mod.role.head" : "mod.role.admin");
  $("mod-staffpanel").classList.toggle("hidden", role !== "head_admin");

  renderModRooms(s.rooms || []);
  renderModBanners(s.banners || []);
  renderStaff(s.staff || []);
}

// renderModRooms lists every room with the two things staff can do to one:
// end it, or hand it to somebody else already in it.
function renderModRooms(rooms) {
  const box = $("mod-rooms");
  box.textContent = "";
  if (!rooms.length) {
    box.appendChild(el("p", "muted pad", t("mod.rooms.none")));
    return;
  }
  for (const r of rooms) {
    const row = el("div", "logrow");
    row.appendChild(el("span", "grow", t("mod.room.line", {
      room: r.name,
      host: r.host_nick || t("status.dash"),
      n: r.members ? r.members.length : 0,
    })));

    const pick = el("select", "small");
    pick.appendChild(el("option", "", t("mod.host.pick")));
    for (const m of r.members || []) {
      if (m.is_host) continue;
      const o = el("option", "", m.nick);
      o.value = m.player_id;
      pick.appendChild(o);
    }
    row.appendChild(pick);

    const hand = el("button", "tiny", t("mod.host.give"));
    hand.onclick = () => {
      if (!pick.value) { banner(t("mod.host.nobody")); return; }
      const why = window.prompt(t("mod.reason.ask"));
      if (!why) return;
      act(() => api("/api/admin/rooms/host",
        { room_id: r.id, new_host_id: pick.value, reason: why }));
    };
    row.appendChild(hand);

    const close = el("button", "tiny danger", t("mod.room.close"));
    close.onclick = () => {
      const why = window.prompt(t("mod.reason.ask"));
      if (!why) return;
      act(() => api("/api/admin/rooms/close", { room_id: r.id, reason: why }));
    };
    row.appendChild(close);
    box.appendChild(row);
  }
}

// renderModBanners lists the announcement strip as it stands, so an editor
// can see what everybody else is seeing before adding to it.
function renderModBanners(ads) {
  const box = $("mod-bannerlist");
  box.textContent = "";
  if (!ads.length) {
    box.appendChild(el("p", "muted pad", t("mod.banners.none")));
    return;
  }
  for (const a of ads) {
    const row = el("div", "logrow");
    row.appendChild(el("span", "grow", a.title || a.body));
    row.appendChild(el("span", "muted small", t(a.active ? "mod.banner.on" : "mod.banner.off")));
    const del = el("button", "tiny danger", t("mod.banner.remove"));
    del.onclick = () => act(() => api("/api/admin/banners/remove", { id: a.id }));
    row.appendChild(del);
    box.appendChild(row);
  }
}

// renderStaff is drawn for the head admin alone (D47).
function renderStaff(staff) {
  const box = $("mod-staff");
  box.textContent = "";
  for (const m of staff) {
    const row = el("div", "logrow");
    row.appendChild(el("span", "grow", m.display_name));
    row.appendChild(el("span", "muted small",
      t(m.role === "head_admin" ? "mod.role.head" : "mod.role.admin")));
    if (m.role !== "head_admin") {
      const drop = el("button", "tiny danger", t("mod.staff.revoke"));
      drop.onclick = () => act(() =>
        api("/api/admin/staff", { target_id: m.account_id, grant: false }));
      row.appendChild(drop);
    }
    box.appendChild(row);
  }
}

async function lookUp(username) {
  if (!username) return;
  lastLookedUp = username;
  try {
    modRecord = await api("/api/admin/player?username=" + encodeURIComponent(username));
    if (!modLabels.length) {
      const got = await api("/api/admin/labels");
      modLabels = got.labels || [];
    }
    banner("");
  } catch (e) {
    modRecord = null;
    banner(e.message);
  }
  renderRecord();
}

// renderRecord draws one person's whole moderation history in one place:
// what they are barred from now, what marks they carry, every sanction they
// have had, and everything staff have done to them.
function renderRecord() {
  const box = $("mod-record");
  box.classList.toggle("hidden", !modRecord);
  if (!modRecord) return;
  const r = modRecord;

  $("mod-who").textContent = r.display_name || r.player_id;
  // Only the head admin may appoint, and nobody may appoint themselves.
  $("mod-appointrow").classList.toggle("hidden",
    state.role !== "head_admin" || r.player_id === state.player_id);
  $("mod-restriction").textContent = restrictionLine(r.restriction, r.kicks_this_week);

  const labels = $("mod-labels");
  labels.textContent = "";
  if (!(r.labels || []).length) {
    labels.appendChild(el("span", "muted small", t("mod.labels.none")));
  }
  for (const name of r.labels || []) {
    const tag = el("span", "labeltag", name);
    const off = el("button", "tiny", t("mod.label.remove"));
    off.onclick = () => act(async () => {
      await api("/api/admin/label", { target_id: r.player_id, label: name, remove: true });
      await lookUp(lastLookedUp);
    });
    tag.appendChild(off);
    labels.appendChild(tag);
  }

  const pick = $("mod-label");
  pick.textContent = "";
  for (const name of modLabels) {
    const o = el("option", "", name);
    o.value = name;
    pick.appendChild(o);
  }

  drawSanctions(r);
  drawActions(r.actions || []);
}

// restrictionLine is the first thing a moderator needs: what is this person
// barred from right now, and how often have they been kicked lately.
function restrictionLine(rest, kicks) {
  const parts = [];
  if (rest && rest.banned) parts.push(t("mod.restricted.banned"));
  if (rest && rest.muted) parts.push(t("mod.restricted.muted"));
  if (rest && rest.timeout) parts.push(t("mod.restricted.timeout"));
  const what = parts.length ? parts.join(", ") : t("mod.restricted.none");
  return t("mod.restricted.line", { what: what, n: kicks || 0 });
}

function drawSanctions(r) {
  const box = $("mod-sanctions");
  box.textContent = "";
  const list = r.sanctions || [];
  if (!list.length) {
    box.appendChild(el("p", "muted pad", t("mod.history.none")));
    return;
  }
  for (const sn of list) {
    const row = el("div", "logrow");
    row.appendChild(el("span", "grow", t("mod.history.line", {
      kind: t("mod.sanction." + sn.kind),
      reason: sn.reason,
      when: when(sn.at),
    })));
    if (lifted(sn)) {
      row.appendChild(el("span", "muted small", t("mod.history.lifted")));
    } else {
      const lift = el("button", "tiny", t("mod.lift"));
      lift.onclick = () => act(async () => {
        await api("/api/admin/sanction/lift", { sanction_id: sn.id, target_id: r.player_id });
        await lookUp(lastLookedUp);
      });
      row.appendChild(lift);
    }
    box.appendChild(row);
  }
}

// lifted reads Go's zero time, which arrives as the year 1, rather than as an
// absent field. Treating that as a real date would show every open sanction
// as already ended.
function lifted(sn) {
  return !!sn.lifted_at && !sn.lifted_at.startsWith("0001-01-01");
}

function drawActions(actions) {
  const box = $("mod-actions");
  box.textContent = "";
  if (!actions.length) {
    box.appendChild(el("p", "muted pad", t("mod.actions.none")));
    return;
  }
  for (const a of actions) {
    const row = el("div", "logrow");
    row.appendChild(el("span", "grow", t("mod.actions.line", {
      action: a.action,
      detail: a.detail || "",
      when: when(a.at),
    })));
    box.appendChild(row);
  }
}

function when(stamp) {
  if (!stamp) return "";
  const d = new Date(stamp);
  return isNaN(d.getTime()) ? "" : d.toLocaleString();
}

// --------------------------------------------------------------- screens

function show(name) {
  screen = name;
  for (const s of document.querySelectorAll(".screen")) s.classList.add("hidden");
  $("screen-" + name).classList.remove("hidden");
  for (const nav of document.querySelectorAll(".nav[data-screen]")) {
    nav.classList.toggle("active", nav.dataset.screen === name);
  }
}

// ---------------------------------------------------------------- events

document.querySelectorAll(".nav[data-screen]").forEach((nav) => {
  nav.onclick = () => show(nav.dataset.screen);
});
document.querySelectorAll(".chattab").forEach((tab) => {
  tab.onclick = () => showChat(tab.dataset.chat);
});

$("chatcollapse").onclick = () => $("chatpanel").classList.toggle("collapsed");

// Moderation. Each form sends what the coordinator requires and nothing it
// does not: a reason with every action, and a duration the moderator chose
// rather than one an empty field produced.
$("modfind").onsubmit = (e) => {
  e.preventDefault();
  lookUp($("mod-username").value.trim());
};

$("mod-appoint").onclick = () => {
  if (!modRecord) return;
  act(() => api("/api/admin/staff", { target_id: modRecord.player_id, grant: true }));
};

$("modlabelform").onsubmit = (e) => {
  e.preventDefault();
  if (!modRecord || !$("mod-label").value) return;
  act(async () => {
    await api("/api/admin/label",
      { target_id: modRecord.player_id, label: $("mod-label").value });
    await lookUp(lastLookedUp);
  });
};

$("modsanctionform").onsubmit = (e) => {
  e.preventDefault();
  if (!modRecord) return;
  const reason = $("mod-reason").value.trim();
  if (!reason) { banner(t("mod.reason.required")); return; }
  act(async () => {
    await api("/api/admin/sanction", {
      target_id: modRecord.player_id,
      kind: $("mod-kind").value,
      reason: reason,
      minutes: Number($("mod-minutes").value) || 0,
    });
    $("mod-reason").value = "";
    await lookUp(lastLookedUp);
  });
};

$("modbannerform").onsubmit = (e) => {
  e.preventDefault();
  const title = $("ban-title").value.trim();
  const body = $("ban-body").value.trim();
  if (!title && !body) { banner(t("mod.banner.empty")); return; }
  act(async () => {
    await api("/api/admin/banners", {
      title: title,
      body: body,
      link_url: $("ban-link").value.trim(),
      active: $("ban-active").checked,
    });
    $("ban-title").value = "";
    $("ban-body").value = "";
    $("ban-link").value = "";
  });
};

// Search and filter run against the list already on screen, so they are
// instant rather than waiting for the next two-second poll.
$("search").oninput = (e) => { query = e.target.value; renderRooms(state.rooms || []); };
document.querySelectorAll(".chip").forEach((chip) => {
  chip.onclick = () => {
    filter = chip.dataset.filter;
    for (const c of document.querySelectorAll(".chip")) {
      c.classList.toggle("active", c === chip);
    }
    renderRooms(state.rooms || []);
  };
});

document.querySelectorAll(".modetabs .chattab").forEach((tab) => {
  tab.onclick = () => gateMode(tab.dataset.mode);
});

$("nameform").onsubmit = async (e) => {
  e.preventDefault();
  const say = (m) => { $("nameerr").textContent = m; };
  try {
    if (!state.accounts) {
      const mmr = $("mmrinput").value;
      await api("/api/profile", {
        nick: $("nameinput").value,
        mmr: mmr === "" ? null : Number(mmr),
      });
    } else if (authMode === "signup") {
      // The terms are accepted as part of creating the account, because that
      // is what the server records. There is no account that has not
      // accepted them.
      if (!$("a-terms").checked) { say(t("auth.mustaccept")); return; }
      await api("/api/auth/signup", {
        username: $("a-user").value,
        display_name: $("a-nick").value || $("a-user").value,
        password: $("a-pass").value,
        terms_version: state.terms_version || "",
      });
      const mmr = $("a-mmr").value;
      if (mmr !== "") await api("/api/profile", { mmr: Number(mmr) });
    } else {
      await api("/api/auth/signin", {
        username: $("a-user").value,
        password: $("a-pass").value,
      });
    }
    // Never leave a typed password sitting in the document.
    $("a-pass").value = "";
    say("");
    $("namegate").classList.add("hidden");
    await refresh();
  } catch (err) {
    say(err.message);
  }
};

$("mebtn").onclick = () => {
  $("p-face").textContent = "";
  $("p-face").appendChild(avatar(state.nick || state.username, state.player_id, "lg"));
  $("p-nick").value = state.nick || "";
  $("p-mmr").value = state.mmr || "";
  $("mmrnote").textContent = state.mmr_locked_until
    ? t("profile.mmr.locked", {
        date: new Date(state.mmr_locked_until).toLocaleDateString(I18n.lang),
      })
    : t("profile.mmr.free");
  $("profileerr").textContent = "";
  // Signing out only exists where there is a session to end.
  $("p-signout").classList.toggle("hidden", !state.signed_in);
  $("p-password").classList.toggle("hidden", !state.signed_in);
  $("p-who").textContent = state.username
    ? t("profile.signedin", { username: state.username })
    : t("profile.local");
  $("profilegate").classList.remove("hidden");
};
$("p-cancel").onclick = () => $("profilegate").classList.add("hidden");

$("p-password").onclick = () => {
  $("pw-user").value = state.username || "";
  $("pw-old").value = "";
  $("pw-new").value = "";
  $("pw-again").value = "";
  $("passerr").textContent = "";
  $("profilegate").classList.add("hidden");
  $("passgate").classList.remove("hidden");
};
$("pw-cancel").onclick = () => $("passgate").classList.add("hidden");

$("passform").onsubmit = async (e) => {
  e.preventDefault();
  // Checked here as well as by the coordinator: a mistyped confirmation is
  // worth catching before it becomes a password nobody knows.
  if ($("pw-new").value !== $("pw-again").value) {
    $("passerr").textContent = t("pass.mismatch");
    return;
  }
  try {
    await api("/api/auth/password", {
      current: $("pw-old").value,
      next: $("pw-new").value,
    });
    $("passgate").classList.add("hidden");
    banner(t("pass.done"));
    await refresh();
  } catch (err) {
    $("passerr").textContent = err.message;
  }
};

// The terms moved under somebody who had already agreed to the old ones.
$("termsread").onclick = () => $("readterms").onclick();
$("termsaccept").onclick = () => act(async () => {
  await api("/api/auth/terms", { version: state.terms_version });
});

$("p-signout").onclick = () => act(async () => {
  await api("/api/auth/signout", {});
  $("profilegate").classList.add("hidden");
  show("lobby");
});

$("profileform").onsubmit = async (e) => {
  e.preventDefault();
  const mmr = $("p-mmr").value;
  try {
    await api("/api/profile", {
      nick: $("p-nick").value,
      mmr: mmr === "" ? null : Number(mmr),
    });
    $("profilegate").classList.add("hidden");
    await refresh();
  } catch (err) {
    $("profileerr").textContent = err.message;
  }
};

$("createform").onsubmit = (e) => {
  e.preventDefault();
  if (needName("namegate.why.create")) return;
  const name = $("roomname").value;
  const door = $("newdoor").value;
  const pass = $("newpass").value;
  if (door === "password" && !pass) { banner(t("door.password.needed")); return; }
  act(async () => {
    await api("/api/rooms/create", {
      name: name,
      privacy: door,
      password: pass,
      min_mmr: Number($("newmmr").value) || 0,
    });
    $("roomname").value = "";
    $("newpass").value = "";
    show("room");
  });
};

// A password box is only shown for the door that uses one. Showing it always
// invites somebody to type a password into a room that will ignore it.
$("newdoor").onchange = () =>
  $("newpass").classList.toggle("hidden", $("newdoor").value !== "password");
$("door").onchange = () =>
  $("doorpass").classList.toggle("hidden", $("door").value !== "password");

$("doorform").onsubmit = (e) => {
  e.preventDefault();
  act(() => api("/api/rooms/privacy", {
    privacy: $("door").value,
    password: $("doorpass").value,
    min_mmr: Number($("doormmr").value) || 0,
  }));
};

$("describeform").onsubmit = (e) => {
  e.preventDefault();
  act(() => api("/api/rooms/describe", { description: $("describe").value }));
};

$("chatform").onsubmit = (e) => {
  e.preventDefault();
  if (needName("namegate.why.chat")) return;
  const text = $("chatinput").value.trim();
  if (!text) return;
  $("chatinput").value = "";
  act(() => api("/api/chat", { channel: chatTab === "room" ? "room" : "lobby", text }));
};

$("btn-findfriend").onclick = () => {
  $("findform").classList.toggle("hidden");
  if (!$("findform").classList.contains("hidden")) $("findinput").focus();
};

$("findform").onsubmit = async (e) => {
  e.preventDefault();
  const who = $("findinput").value.trim();
  if (!who) return;
  const out = $("findresult");
  try {
    const found = await api("/api/players/find?username=" + encodeURIComponent(who));
    out.textContent = "";
    out.appendChild(document.createTextNode(found.display_name || found.player_id));
    const add = el("button", "primary tiny", t("friends.request"));
    add.onclick = () => act(async () => {
      await api("/api/friends", { action: "request", target_id: found.player_id });
      out.textContent = t("friends.requested");
      $("findinput").value = "";
    });
    out.appendChild(add);
  } catch (err) {
    out.textContent = err.message;
  }
};

// The terms are shown as text, in a <pre>, exactly as the server sent them.
// They are an agreement somebody is about to accept; rendering them as markup
// would mean the words on screen could differ from the words on file.
$("readterms").onclick = async () => {
  $("termsgate").classList.remove("hidden");
  $("termstext").textContent = t("auth.termsloading");
  try {
    const got = await api("/api/terms");
    $("termstext").textContent = got.text || "";
  } catch (err) {
    $("termstext").textContent = err.message;
  }
};
$("termsclose").onclick = () => $("termsgate").classList.add("hidden");

$("dmclose").onclick = () => { dmWith = null; $("dmgate").classList.add("hidden"); };
$("dmform").onsubmit = (e) => {
  e.preventDefault();
  const text = $("dminput").value.trim();
  if (!text) return;
  $("dminput").value = "";
  loadConversation(text);
};

$("btn-connect").onclick = () => act(() => api("/api/connect", {}));
$("btn-disconnect").onclick = () => act(() => api("/api/disconnect", {}));
$("btn-leave").onclick = () => act(async () => {
  await api("/api/rooms/leave", {});
  show("lobby");
});
$("btn-tolobby").onclick = () => show("lobby");

$("btn-lock").onclick = () => act(() => api("/api/rooms/status", { status: "locked_in_game" }));
$("btn-reopen").onclick = () => act(() => api("/api/rooms/status", { status: "open_to_new_players" }));
$("btn-open").onclick = () => act(() => api("/api/rooms/status", { status: "open" }));

$("btn-play").onclick = () => act(() => api("/api/play", {
  mode: Number($("mode").value),
  team: $("team").value,
}));

$("btn-diag").onclick = () => act(() => api("/api/diagnose", {}));

// ----------------------------------------------------------------- poll

async function refresh() {
  try {
    state = await api("/api/state");
    render();
  } catch (e) {
    banner(t("err.dead", { error: e.message }));
  }
}

// Nothing may draw before the strings are in: a screen that flashes its keys
// and then corrects itself looks broken, and on a slow PC the flash lasts long
// enough to be read.
I18n.load("en").then(() => {
  I18n.apply();
  showChat("lobby");
  refresh();
  setInterval(refresh, POLL_MS);
});
