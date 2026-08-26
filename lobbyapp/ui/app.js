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
let sortKey = "players";
let sortDir = "desc";
let query = "";
let busy = false;
let authMode = "signin";

// The chat dock. dmTabs is what the tab strip shows, dmLogs what each of
// those tabs holds, and seen the last thing this window noticed in a tab -
// which is how an arriving message gets to ring exactly once.
let dmTabs = [];
let dmLogs = {};
let seen = {};
let audio = null;

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
  const mf = $("meface");
  if (mf.dataset.who !== (s.player_id || "") + "/" + (s.nick || "")) {
    mf.dataset.who = (s.player_id || "") + "/" + (s.nick || "");
    mf.textContent = "";
    mf.appendChild(avatar(s.nick || s.username, s.player_id, "sm"));
  }
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
  renderFriends(s.friends, s.friends_error);
  if (inRoom) renderRoom(s.room);
  renderChatDock(s);
  renderDiag();
  renderSettings(s);
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
  const friendIDs = ((state.friends && state.friends.friends) || []).map((f) => f.player_id);
  return rooms.filter((r) => {
    if (q) {
      const hay = [r.name, r.host_nick, r.description].join(" ").toLowerCase();
      if (!hay.includes(q)) return false;
    }
    switch (filter) {
      case "joinable":
        return r.joinable && (r.seats || 0) < 10;
      case "waiting":
        return !inGame(r);
      case "playing":
        return inGame(r);
      case "mine":
        // Rooms this player would actually be let into: the MMR floor is the
        // one door that silently excludes somebody who has not tried it.
        return !r.min_mmr || (state.mmr || 0) >= r.min_mmr;
      case "friends":
        return (r.members || []).some((m) => friendIDs.indexOf(m.player_id) >= 0);
      default:
        return true;
    }
  });
}

function inGame(r) { return r.status === "locked_in_game"; }

// How a room ranks on the column being sorted. Joinability is a rank rather
// than a flag so that "status" sorts into three bands - open to me, running,
// shut - rather than into two.
function sortKeyOf(r, key) {
  switch (key) {
    case "name": return (r.name || "").toLowerCase();
    case "mmr": return r.min_mmr || r.avg_mmr || 0;
    // A host who has not reported a ping sorts last whichever way round the
    // column is, because "unknown" is not "excellent" (D54).
    case "ping": return r.host_relay_ms || 9999;
    case "status": return r.joinable && !inGame(r) ? 2 : inGame(r) ? 1 : 0;
    default: return r.seats || 0;
  }
}

function sortRooms(rooms) {
  const dir = sortDir === "asc" ? 1 : -1;
  return rooms.slice().sort((a, b) => {
    const x = sortKeyOf(a, sortKey), y = sortKeyOf(b, sortKey);
    if (typeof x === "string") return x.localeCompare(y) * dir;
    return (x - y) * dir;
  });
}

function toggleSort(key) {
  if (sortKey === key) {
    sortDir = sortDir === "asc" ? "desc" : "asc";
  } else {
    sortKey = key;
    // A name reads naturally from A downwards; every number reads best with
    // the biggest first, because that is the room somebody is looking for.
    sortDir = key === "name" ? "asc" : "desc";
  }
  drawSortHeads();
  renderRooms(state.rooms || []);
}

function drawSortHeads() {
  for (const b of $("roomhead").querySelectorAll("button")) {
    const on = b.dataset.sort === sortKey;
    b.classList.toggle("on", on);
    const up = sortDir === "asc";
    b.querySelector(".arrow").textContent = on ? (up ? " ↑" : " ↓") : "";
  }
  $("sorthint").textContent = t("lobby.sortedby", {
    what: t("lobby.col." + (sortKey === "status" ? "status" : sortKey)),
    dir: t(sortDir === "asc" ? "lobby.ascending" : "lobby.descending"),
  });
}

function renderRooms(rooms) {
  const box = $("roomlist");
  const shown = sortRooms(visible(rooms));
  $("roomcount").textContent = t("lobby.shown", { shown: shown.length, all: rooms.length });

  box.textContent = "";
  if (!shown.length) {
    // An empty list says so in the middle of its own space, and offers the
    // two ways out. A single grey line against the top edge reads as a page
    // that failed rather than as a lobby with nothing in it yet.
    const none = el("div", "nothing");
    none.appendChild(el("div", "mark", "⬡"));
    none.appendChild(el("h2", "", t(rooms.length ? "lobby.nomatch" : "lobby.empty")));
    none.appendChild(el("p", "", t(rooms.length ? "lobby.nomatch.why" : "lobby.empty.why")));
    const acts = el("div", "inline");
    if (rooms.length) {
      const clear = el("button", "", t("lobby.clearfilters"));
      clear.onclick = () => setFilter("all");
      acts.appendChild(clear);
    }
    const make = el("button", "primary", t("lobby.create"));
    make.onclick = openCreate;
    acts.appendChild(make);
    none.appendChild(acts);
    box.appendChild(none);
    return;
  }
  for (const r of shown) box.appendChild(roomCard(r));
}

// One row per room: who is hosting it, one line of everything else, and the
// three numbers a player chooses on. The whole row opens the room; only the
// button in the last column joins it.
function roomCard(r) {
  const card = el("div", "room");
  card.onclick = () => (r.id === state.room_id ? show("room") : joinRoom(r));

  const who = el("div", "room-who");
  who.appendChild(avatar(r.host_nick, r.host_id));
  const about = el("div", "grow");
  about.appendChild(el("div", "room-name", r.name));

  // Everything else about the room on one line, in the order a player asks
  // for it. It is allowed to run out of room and be cut; the name is not.
  const meta = el("div", "room-meta");
  meta.appendChild(statusBadge(r.status));
  const bits = [r.host_nick];
  if (r.description) bits.push(r.description);
  if (r.needs_password) bits.push(t("lobby.door.password"));
  if (r.privacy === "friends") bits.push(t("lobby.door.friends"));
  if (r.privacy === "invite") bits.push(t("lobby.door.invite"));
  if (r.id === state.room_id) bits.push(t("lobby.youarehere"));
  meta.appendChild(el("span", "rest", bits.join(" · ")));
  about.appendChild(meta);
  who.appendChild(about);
  card.appendChild(who);

  card.appendChild(seatCell(r));
  card.appendChild(mmrCell(r));
  card.appendChild(pingCell(r));
  card.appendChild(roomActions(r));
  return card;
}

// seatCell draws the ten playing slots as ten marks beside the count.
//
// Somebody scanning the lobby is asking one question - is there room for me -
// and a bar answers it in the time the eye takes to pass over it. The number
// stays beside it for the times they want to be exact.
function seatCell(r) {
  const cell = el("div", "room-seats");
  const taken = r.seats || 0;
  cell.appendChild(el("span", "n", t("lobby.of10", { n: taken })));
  const ticks = el("div", "ticks" + (taken >= 10 ? " full" : ""));
  for (let i = 0; i < 10; i++) ticks.appendChild(el("i", i < taken ? "on" : ""));
  cell.appendChild(ticks);
  return cell;
}

// mmrCell separates the number from what it means, because the two are read
// at different moments: the figure tells a player whether they belong here,
// the label tells them whether it is a floor they must clear or an average
// they are being compared against.
function mmrCell(r) {
  const cell = el("div", "room-mmr rcol-hide");
  if (r.min_mmr) {
    cell.appendChild(document.createTextNode(t("lobby.mmr.plus", { n: r.min_mmr }) + " "));
    cell.appendChild(el("span", "kind", t("lobby.mmr.min")));
  } else if (r.avg_mmr) {
    cell.appendChild(document.createTextNode(String(r.avg_mmr) + " "));
    cell.appendChild(el("span", "kind", t("lobby.mmr.avg")));
  } else {
    cell.appendChild(document.createTextNode(t("lobby.mmr.any")));
  }
  return cell;
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
  const grade = !ms ? "" : ms < 60 ? "good" : ms < 140 ? "fair" : "poor";
  const cell = el("div", "room-ping rcol-hide " + grade);
  cell.title = ms ? t("lobby.ping.explain") : t("lobby.ping.none");
  const bars = el("span", "bars");
  for (let i = 0; i < 3; i++) bars.appendChild(el("i"));
  cell.appendChild(bars);
  cell.appendChild(document.createTextNode(
    ms ? t("lobby.ping.value", { n: ms }) : t("lobby.ping.unknown")));
  return cell;
}

// The last column: one button, and a dot saying whether a match is running
// in there. The dot is the only thing on the row that can be read without
// looking directly at it.
function roomActions(r) {
  const acts = el("div", "room-actions");
  const mine = r.id === state.room_id;
  const b = el("button", mine || (r.joinable && !state.room_id) ? "primary" : "",
    mine ? t("room.open") : (r.seats || 0) >= 10 ? t("room.full")
      : inGame(r) ? t("room.ingame") : t("room.join"));
  b.disabled = !mine && (!r.joinable || !!state.room_id);
  b.title = mine ? "" : state.room_id ? t("room.join.busy") : r.joinable ? "" : t("room.join.closed");
  b.onclick = (e) => { e.stopPropagation(); mine ? show("room") : joinRoom(r); };
  acts.appendChild(b);

  const dot = el("span", "livedot " + (inGame(r) ? "game" : "open"));
  dot.title = t(inGame(r) ? "lobby.live.game" : "lobby.live.open");
  acts.appendChild(dot);
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
  $("room-face").appendChild(avatar(r.host_nick, r.host_id));
  $("room-name").textContent = r.name;

  // One line of everything else about the room, in the order somebody asks
  // for it: who is running it, how good the people in it are, how full it is.
  const bits = [t("room.meta.host", { host: r.host_nick })];
  if (r.avg_mmr) bits.push(t("room.meta.mmr", { mmr: r.avg_mmr }));
  if (r.min_mmr) bits.push(t("lobby.mmr.plus", { n: r.min_mmr }));
  bits.push(t("room.meta.seats", { seats: r.seats }));
  if (r.description) bits.push(r.description);
  $("room-sub").textContent = bits.join(" · ");

  const badge = $("room-status");
  badge.className = statusClass(r.status);
  badge.textContent = statusLabel(r.status);

  const iAmHost = !!state.is_host;
  for (const e of document.querySelectorAll(".hostonly")) e.classList.toggle("hidden", !iAmHost);
  // Only fill the box when it is not being typed in, or every poll would
  // overwrite what the host is halfway through writing.
  if (document.activeElement !== $("describe")) $("describe").value = r.description || "";
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
  if (!specs.length) {
    sbox.appendChild(el("span", "what", t("room.spectators.none")));
  } else {
    for (const m of specs) sbox.appendChild(slotCard(m.slot, m, false, true));
  }

  const net = [];
  if (state.virtual_ip) net.push(t("net.you", { ip: state.virtual_ip }));
  if (state.host_ip) net.push(t("net.host", { ip: state.host_ip }));
  if (state.relay_ms) net.push(t("net.relay", { n: state.relay_ms }));
  $("netinfo").textContent = net.join(" · ");

  drawStepper(r);
  drawNetBanner();
}

// drawStepper is the whole of getting from a seat to a game, as three steps
// with one button under them.
//
// It replaced a row of buttons that were sometimes disabled. The commonest
// failure in the two-PC test was two players sitting in a room, neither of
// them on its network, with nothing on the screen saying which of the three
// things had not happened. A numbered list cannot be misread that way, and
// the button always says the next thing to do rather than everything that
// could be done.
function drawStepper(r) {
  const mine = (r.members || []).find((m) => m.player_id === state.player_id);
  const seated = !!mine;
  const side = mine && !mine.spectator
    ? t(mine.slot < 5 ? "room.team.radiant" : "room.team.dire") : "";

  step("seat", seated, false, seated && !mine.spectator
    ? t("step.seat.at", { side: side, slot: mine.slot + 1 })
    : t("step.seat.watching"));
  step("net", !!state.connected, seated && !state.connected,
    state.connected && state.virtual_ip
      ? t("net.you", { ip: state.virtual_ip }) + " · " + t("net.host", { ip: state.host_ip || "?" })
      : t("step.net.not"));
  step("game", !!state.dota_running, !!state.connected && !state.dota_running,
    t("step.game.detail"));

  const b = $("btn-step");
  if (!state.connected) {
    b.textContent = t(state.is_host ? "step.go.host" : "step.go.connect");
    b.disabled = false;
    b.onclick = () => act(() => api("/api/connect", {}));
  } else if (!state.dota_running) {
    b.textContent = t("step.go.launch");
    b.disabled = false;
    b.onclick = () => act(() => api("/api/play", { mode: Number($("mode").value), team: myTeam() }));
  } else {
    b.textContent = t("step.go.running");
    b.disabled = true;
    b.onclick = null;
  }
}

// A step is done, happening now, or still ahead. The detail line under it is
// the evidence for whichever of those it is.
function step(name, done, now, detail) {
  const e = $("step-" + name);
  e.className = "step" + (done ? " done" : now ? " now" : "");
  const nth = name === "seat" ? "1" : name === "net" ? "2" : "3";
  $("step-" + name + "-mark").textContent = done ? "✓" : nth;
  const d = $("step-" + name + "-detail");
  if (d) d.textContent = detail;
}

// drawDoor fills the host's door controls from the room.
//
// The password is never filled in, because the coordinator never sends it
// back and should not: what is on screen would then be a guess at a secret.
// An empty box means "leave it as it is"; typing in one changes it.
function drawDoor(r) {
  const door = r.privacy || "public";
  // A password is a second lock on an otherwise open door, so the segment
  // shows "anyone" for a password room and the box below carries the secret.
  segment("door", door === "password" ? "public" : door);
  if (document.activeElement !== $("doormmr")) {
    $("doormmr").value = r.min_mmr ? String(r.min_mmr) : "";
  }
  $("doorpass").placeholder = r.needs_password
    ? t("door.password.keep") : t("door.password.placeholder");
  $("doornow").textContent = t("door.now", { door: t("door." + door) });
}

// A segmented control remembers its answer in the markup: which button
// carries .active is the value, so nothing has to be kept beside it.
function segment(id, value) {
  for (const b of $(id).querySelectorAll("button")) {
    b.classList.toggle("active", b.dataset.door === value);
  }
}
function segmentValue(id) {
  for (const b of $(id).querySelectorAll("button")) {
    if (b.classList.contains("active")) return b.dataset.door;
  }
  return "public";
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

// canTakeSeat answers whether clicking this empty seat would do anything.
//
// Which slot you sit in is which team you are on, so this is how a player
// picks a side. The refusals mirror the coordinator's exactly, because a card
// that invites a click and then shows an error is worse than one that does
// not invite it: slot 0 is the host's for the room's whole life, the host
// does not move out of it, and a locked room is a match already running.
function canTakeSeat(index, spectator) {
  if (spectator || index === 0 || state.is_host) return false;
  if (!state.room || state.room.status === "locked_in_game") return false;
  return (state.room.members || [])
    .some((m) => m.player_id === state.player_id && !m.spectator);
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
    if (canTakeSeat(index, spectator)) {
      card.classList.add("takeable");
      card.title = t("room.slot.take.note");
      body.appendChild(el("div", "slot-name", t("room.slot.take")));
      card.onclick = () => act(() => api("/api/rooms/slot", { slot: index }));
    } else {
      body.appendChild(el("div", "slot-name muted", t("room.slot.empty")));
    }
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
// The rail groups people by where they are, because that is the only
// question it answers: who can I play with right now. A flat list sorted by
// presence made the reader work that out for themselves every time.
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
    $("friendcount").textContent = "";
    return;
  }

  const waiting = list.incoming || [];
  const friends = list.friends || [];
  const online = friends.filter((f) => f.online).length;
  $("friendcount").textContent = friends.length
    ? t("friends.count", { on: online, all: friends.length }) : "";

  if (waiting.length) {
    box.appendChild(el("div", "friend-group", t("friends.requests")));
    for (const f of waiting) box.appendChild(requestRow(f));
  }

  if (!friends.length) {
    box.appendChild(el("p", "muted small friend-empty", t("friends.none")));
    return;
  }

  const groups = [
    ["friends.group.inroom", friends.filter((f) => f.online && f.room_id)],
    ["friends.group.online", friends.filter((f) => f.online && !f.room_id)],
    ["friends.group.offline", friends.filter((f) => !f.online)],
  ];
  for (const [key, people] of groups) {
    if (!people.length) continue;
    box.appendChild(el("div", "friend-group", t(key)));
    for (const f of people) box.appendChild(friendRow(f));
  }
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

  // The row is the button. A "Message" button beside every name cost the
  // rail most of its width, and now that a conversation opens as a tab in
  // the dock rather than as a dialog, clicking the person is the whole
  // gesture.
  row.classList.add("clickable");
  row.title = t("friends.message");
  row.onclick = () => openConversation(f);

  const acts = el("div", "acts");
  if (state.room_id) {
    const inv = el("button", "", t("friends.invite"));
    inv.onclick = (e) => {
      e.stopPropagation();
      act(() => api("/api/friends/invite", { target_id: f.player_id }));
    };
    acts.appendChild(inv);
  } else if (f.room_id) {
    // Going where a friend already is, in one click. The room may be behind
    // a door, and the coordinator says so rather than this guessing.
    const room = (state.rooms || []).find((r) => r.id === f.room_id);
    if (room && room.joinable) {
      const go = el("button", "primary", t("friends.join"));
      go.onclick = (e) => { e.stopPropagation(); joinRoom(room); };
      acts.appendChild(go);
    }
  }
  if (acts.children.length) row.appendChild(acts);
  row.classList.toggle("off", !f.online);
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

// openConversation puts a friend in a tab of the dock rather than in a dialog
// over the lobby. Talking to somebody is not a thing you stop doing other
// things to do.
function openConversation(f) {
  if (!dmTabs.some((d) => d.player_id === f.player_id)) {
    dmTabs.push({ player_id: f.player_id, display_name: f.display_name || f.player_id });
  }
  drawTabs();
  showChat("dm:" + f.player_id);
  openDock();
  $("chatinput").focus();
  loadConversation();
}

function closeConversation(id) {
  dmTabs = dmTabs.filter((d) => d.player_id !== id);
  delete dmLogs[id];
  drawTabs();
  if (chatTab === "dm:" + id) showChat("lobby");
}

function dmOpen() {
  return chatTab.startsWith("dm:") ? chatTab.slice(3) : "";
}

async function loadConversation(send) {
  const id = dmOpen();
  if (!id) return;
  try {
    const out = await api("/api/friends/messages", { target_id: id, send: send || "" });
    dmLogs[id] = out.messages || [];
    if (dmOpen() === id) drawLog();
  } catch (e) {
    banner(e.message);
  }
}

// ------------------------------------------------- the chat dock (D56)

// renderChatDock is called on every poll. It redraws the tab strip, notices
// anything that arrived in a tab the reader is not looking at, and redraws
// the open log.
function renderChatDock(s) {
  // A friend with unread messages gets a tab whether or not anybody opened
  // one. Somebody writing to you is the thing most worth interrupting for,
  // and a message that waits behind a button nobody pressed is a message
  // that was not delivered.
  for (const f of (s.friends && s.friends.friends) || []) {
    if (f.unread && !dmTabs.some((d) => d.player_id === f.player_id)) {
      dmTabs.push({ player_id: f.player_id, display_name: f.display_name || f.player_id });
    }
  }
  drawTabs();

  notice("lobby", signature(s.lobby_chat || []));
  if (s.room_id) notice("room", signature(s.room_chat || []));
  for (const f of (s.friends && s.friends.friends) || []) noticeDM(f);
  if (dmOpen()) loadConversation();
  drawLog();
}

function signature(msgs) {
  return msgs.length ? msgs[msgs.length - 1].id + ":" + msgs.length : "0";
}

// notice decides whether something new turned up in a tab, and what to do
// about it. The first sighting of a tab is not new - it is the backlog that
// was already there when the window opened, and announcing it would make
// every start-up ring.
function notice(tab, sig) {
  const first = !(tab in seen);
  const changed = seen[tab] !== sig;
  seen[tab] = sig;
  if (!first && changed) flag(tab);
}

// A conversation is counted, not signed: reading it sets the count back to
// zero, so "different from last time" would ring on the way down as well as
// on the way up. Only a count that grew is a message that arrived.
function noticeDM(f) {
  const tab = "dm:" + f.player_id;
  const now = f.unread || 0;
  const first = !(tab in seen);
  const before = seen[tab] || 0;
  seen[tab] = now;
  if (!first && now > before) flag(tab);
}

function flag(tab) {
  if (tab === chatTab && dockIsOpen()) return;
  const btn = document.querySelector('.chattab[data-chat="' + cssSafe(tab) + '"]');
  if (btn && tab !== chatTab) btn.classList.add("unread");
  openDock();
  ping();
}

// cssSafe hands the id to the browser's own escaper rather than guessing
// at the grammar. Ids are hex today, and a selector that works only
// because of what ids happen to look like breaks when that changes.
function cssSafe(v) {
  return window.CSS && CSS.escape ? CSS.escape(String(v)) : String(v);
}

// ping is the sound. The audio device is only ever created from a real click
// or keystroke, because a browser asked to make noise before anybody has
// touched the page refuses and says so in the console - and a console that
// says things is a console nobody reads.
function ping() {
  if (!audio) return;
  try {
    const osc = audio.createOscillator();
    const gain = audio.createGain();
    osc.connect(gain);
    gain.connect(audio.destination);
    osc.type = "sine";
    osc.frequency.value = 720;
    gain.gain.setValueAtTime(0.0001, audio.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.07, audio.currentTime + 0.01);
    gain.gain.exponentialRampToValueAtTime(0.0001, audio.currentTime + 0.18);
    osc.start();
    osc.stop(audio.currentTime + 0.2);
  } catch (_) {
    // A machine that will not make a sound is not a machine with a problem.
  }
}

function armAudio() {
  if (audio) return;
  try { audio = new (window.AudioContext || window.webkitAudioContext)(); } catch (_) { /* none */ }
}
document.addEventListener("pointerdown", armAudio, { once: true });
document.addEventListener("keydown", armAudio, { once: true });

// --- the tab strip -------------------------------------------------------

function drawTabs() {
  const strip = $("tabstrip");
  // The three fixed tabs live in the markup; only the conversations are
  // drawn, so a redraw every two seconds cannot lose a tab's own state.
  for (const gone of strip.querySelectorAll(".chattab.dm")) gone.remove();

  for (const d of dmTabs) {
    const id = "dm:" + d.player_id;
    const tab = el("button", "chattab dm");
    tab.dataset.chat = id;
    tab.appendChild(el("span", "", d.display_name));
    const shut = el("span", "x", "\u00d7");
    shut.title = t("chat.close");
    shut.onclick = (e) => { e.stopPropagation(); closeConversation(d.player_id); };
    tab.appendChild(shut);
    tab.onclick = () => { showChat(id); openDock(); };
    strip.appendChild(tab);
  }
  for (const tab of strip.querySelectorAll(".chattab")) {
    tab.classList.toggle("active", tab.dataset.chat === chatTab);
    if (tab.dataset.chat === chatTab) tab.classList.remove("unread");
  }
}

function showChat(which) {
  chatTab = which;
  drawTabs();
  // Party is present and honest about itself: parties are not built, and a
  // tab that silently does nothing is worse than one that says so.
  $("chatform").classList.toggle("hidden", which === "party");
  // Only a private conversation is labelled. On the lobby and room tabs the
  // lit tab already says where the words go, and a second label saying the
  // same thing is furniture.
  const dm = which.indexOf("dm:") === 0;
  $("chatto").classList.toggle("hidden", !dm);
  $("chatto").textContent = dm ? t("chat.to", { where: tabName(which) }) : "";
  drawLog();
}

function tabName(which) {
  if (which === "lobby") return t("chat.tab.lobby");
  if (which === "room") return t("chat.tab.room");
  if (which === "party") return t("chat.tab.party");
  const who = dmTabs.find((d) => "dm:" + d.player_id === which);
  return who ? who.display_name : "";
}

// --- the log -------------------------------------------------------------

function drawLog() {
  const log = $("chatlog");
  const msgs = chatTab === "lobby" ? (state.lobby_chat || [])
    : chatTab === "room" ? (state.room_chat || [])
      : chatTab === "party" ? null
        : dmLogs[dmOpen()] || [];

  const counted = chatTab === "lobby" && state.online;
  $("presence").textContent = counted ? t("chat.inlobby", { n: state.online }) : "";

  if (msgs === null) {
    log.dataset.sig = "party";
    log.textContent = "";
    log.appendChild(el("p", "muted small", t("chat.party.soon")));
    return;
  }

  // Only redraw when something changed, or the log scrolls away from under
  // the reader every two seconds.
  const dm = chatTab.indexOf("dm:") === 0;
  const sig = chatTab + "|" + (dm
    ? msgs.length + ":" + (msgs.length ? msgs[msgs.length - 1].at : "")
    : signature(msgs));
  if (log.dataset.sig === sig) return;
  log.dataset.sig = sig;

  const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  log.textContent = "";
  for (const m of msgs) {
    const line = el("div", "msg" + (m.system ? " system" : ""));
    line.appendChild(el("span", "at", clock(m.at)));

    if (m.system) {
      line.appendChild(el("span", "who", t("chat.system")));
      line.appendChild(el("span", "said", m.text));
      log.appendChild(line);
      continue;
    }
    // A private message carries who sent it but not their name - the server
    // does not repeat what both ends already know. There are only two people
    // in a conversation, so anything not from them is from me.
    const mine = dm ? m.from_id !== dmOpen() : m.player_id === state.player_id;
    const name = dm
      ? (mine ? (state.nick || t("chat.me")) : tabName(chatTab))
      : m.nick;
    line.appendChild(el("span", "who" + (mine ? " self" : ""), name));
    line.appendChild(el("span", "said", dm ? m.body : m.text));
    log.appendChild(line);
  }
  if (atBottom) log.scrollTop = log.scrollHeight;
}

// The clock beside a line, in the reader's own zone and to the minute. A
// chat log is read for order and recency; the second something was said has
// never been the question.
function clock(at) {
  if (!at) return "";
  const d = new Date(at);
  if (isNaN(d.getTime())) return "";
  return String(d.getHours()).padStart(2, "0") + ":" + String(d.getMinutes()).padStart(2, "0");
}

// --- open and shut -------------------------------------------------------

function dockIsOpen() { return !$("chatdock").classList.contains("collapsed"); }
function openDock() { $("chatdock").classList.remove("collapsed"); }
function shutDock() {
  $("chatdock").classList.add("collapsed");
  $("chatmenu").classList.add("hidden");
}

// The + on the tab strip: who is there to talk to. Friends, because a
// private message to a stranger is the first thing a lobby gets abused for.
function drawChatMenu() {
  const menu = $("chatmenu");
  menu.textContent = "";
  const friends = ((state.friends && state.friends.friends) || [])
    .filter((f) => !dmTabs.some((d) => d.player_id === f.player_id));
  if (!friends.length) {
    menu.appendChild(el("p", "muted small pad", t("chat.nobody")));
    return;
  }
  for (const f of friends) {
    const row = el("button", "menurow");
    row.appendChild(avatar(f.display_name || f.player_id, f.player_id, "sm"));
    row.appendChild(el("span", "grow", f.display_name || f.player_id));
    row.onclick = () => { menu.classList.add("hidden"); openConversation(f); };
    menu.appendChild(row);
  }
}

// ------------------------------------------------------------- settings

// Everything about this installation rather than about a room. It is the
// screen somebody opens when the network is not working, so the three facts
// that explain that sit above the button that tests them.
function renderSettings(s) {
  const face = $("set-face");
  face.textContent = "";
  face.appendChild(avatar(s.nick || s.username, s.player_id, "lg"));
  $("set-name").textContent = s.nick || t("status.dash");
  const bits = [];
  if (s.username) bits.push(t("profile.signedin", { username: s.username }));
  else bits.push(t("profile.local"));
  bits.push(s.mmr ? t("status.mmr", { n: s.mmr }) : t("status.nomm"));
  $("set-sub").textContent = bits.join(" · ");

  $("btn-password").classList.toggle("hidden", !s.signed_in);
  $("btn-signout").classList.toggle("hidden", !s.signed_in);

  $("fact-adapter").textContent = s.adapter || t("settings.adapter.none");
  $("fact-relay").textContent = s.relay_ms
    ? t("net.relay", { n: s.relay_ms })
    : t(s.connected ? "settings.relay.unknown" : "settings.relay.off");
  $("fact-service").textContent = t(s.service ? "status.service.up" : "status.service.down");
  $("fact-version").textContent = s.version || t("status.dash");
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
// The three fixed tabs. Conversation tabs get their handler in drawTabs,
// where they are made.
document.querySelectorAll("#tabstrip .chattab").forEach((tab) => {
  tab.onclick = () => { showChat(tab.dataset.chat); openDock(); };
});

// The dock opens by any route into it and shuts only when asked. Clicking
// into the box is a person about to type; that is the moment to make room.
$("chatmin").onclick = () => (dockIsOpen() ? shutDock() : openDock());
$("chatinput").onfocus = openDock;
$("chatadd").onclick = () => {
  const menu = $("chatmenu");
  if (!menu.classList.contains("hidden")) { menu.classList.add("hidden"); return; }
  drawChatMenu();
  menu.classList.remove("hidden");
  openDock();
};
document.addEventListener("click", (e) => {
  if (!$("chatmenu").contains(e.target) && e.target !== $("chatadd")
    && !$("chatadd").contains(e.target)) {
    $("chatmenu").classList.add("hidden");
  }
});

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

// Search, filter and sort all run against the list already on screen, so
// they are instant rather than waiting for the next two-second poll.
$("search").oninput = (e) => { query = e.target.value; renderRooms(state.rooms || []); };

function setFilter(name) {
  filter = name;
  for (const c of document.querySelectorAll("#filters .chip")) {
    c.classList.toggle("active", c.dataset.filter === name);
  }
  renderRooms(state.rooms || []);
}
document.querySelectorAll("#filters .chip").forEach((chip) => {
  chip.onclick = () => setFilter(chip.dataset.filter);
});

// Every heading sorts. A room list is read for one thing at a time - who has
// space, who is closest, who is at my level - and which one it is changes
// between one glance and the next.
$("roomhead").querySelectorAll("button").forEach((b) => {
  b.onclick = () => toggleSort(b.dataset.sort);
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

// --- the create dialog ----------------------------------------------------

// The door is chosen before the room exists (D41). A room opened public and
// locked a second later is a second in which anybody can walk in.
function openCreate() {
  if (needName("namegate.why.create")) return;
  $("createerr").textContent = "";
  $("roomname").value = "";
  $("newpass").value = "";
  $("newmmr").value = "";
  $("newpasson").checked = false;
  segment("newdoor", "public");
  drawCreateDoor();
  $("creategate").classList.remove("hidden");
  $("roomname").focus();
}

// A password is a second lock on an otherwise open door rather than a fourth
// kind of door, so the box only exists while that door is chosen, and only
// while the box beside it is ticked.
function drawCreateDoor() {
  const open = segmentValue("newdoor") === "public";
  $("newpasscheck").classList.toggle("hidden", !open);
  $("newpassfield").classList.toggle("hidden", !(open && $("newpasson").checked));
}

$("btn-create").onclick = openCreate;
$("createcancel").onclick = () => $("creategate").classList.add("hidden");
$("newpasson").onchange = drawCreateDoor;
for (const b of $("newdoor").querySelectorAll("button")) {
  b.onclick = () => { segment("newdoor", b.dataset.door); drawCreateDoor(); };
}

$("createform").onsubmit = (e) => {
  e.preventDefault();
  if (needName("namegate.why.create")) return;
  const door = segmentValue("newdoor");
  const pass = $("newpasson").checked ? $("newpass").value : "";
  if (door === "public" && $("newpasson").checked && !pass) {
    $("createerr").textContent = t("door.password.needed");
    return;
  }
  act(async () => {
    try {
      await api("/api/rooms/create", {
        name: $("roomname").value,
        privacy: pass ? "password" : door,
        password: pass,
        min_mmr: Number($("newmmr").value) || 0,
      });
    } catch (err) {
      $("createerr").textContent = err.message;
      throw err;
    }
    $("creategate").classList.add("hidden");
    show("room");
  });
};

// --- the host's own controls ---------------------------------------------

$("btn-roomsettings").onclick = () => {
  if (state.room) drawDoor(state.room);
  $("roomsetgate").classList.remove("hidden");
};
$("roomsetclose").onclick = () => $("roomsetgate").classList.add("hidden");

for (const b of $("door").querySelectorAll("button")) {
  b.onclick = () => segment("door", b.dataset.door);
}

$("doorform").onsubmit = (e) => {
  e.preventDefault();
  const pass = $("doorpass").value;
  act(() => api("/api/rooms/privacy", {
    privacy: pass ? "password" : segmentValue("door"),
    password: pass,
    min_mmr: Number($("doormmr").value) || 0,
  }));
};

$("describeform").onsubmit = (e) => {
  e.preventDefault();
  act(() => api("/api/rooms/describe", { description: $("describe").value }));
};

// --- inviting -------------------------------------------------------------

// One word, two things: tell them to come, and let them through the door.
// Doing only the first is how somebody is invited and then refused (D41).
$("btn-invite").onclick = () => {
  drawInvites();
  $("invitegate").classList.remove("hidden");
};
$("inviteclose").onclick = () => $("invitegate").classList.add("hidden");

function drawInvites() {
  const box = $("invitelist");
  box.textContent = "";
  const friends = ((state.friends && state.friends.friends) || [])
    .filter((f) => !state.room_id || f.room_id !== state.room_id);
  if (!friends.length) {
    box.appendChild(el("p", "muted small", t("room.invite.nobody")));
    return;
  }
  for (const f of friends) {
    const row = el("div", "friend");
    const port = el("div", "who-av");
    port.appendChild(avatar(f.display_name || f.player_id, f.player_id, "sm"));
    port.appendChild(el("span", "presence-dot " + (f.online ? "on" : "off")));
    row.appendChild(port);
    const who = el("div", "grow");
    who.appendChild(el("div", "name", f.display_name || f.player_id));
    who.appendChild(el("div", "where", whereabouts(f)));
    row.appendChild(who);
    const go = el("button", "tiny primary", t("room.invite.send"));
    go.onclick = () => act(async () => {
      await api("/api/invite", { target_id: f.player_id });
      go.disabled = true;
      go.textContent = t("room.invite.sent");
    });
    row.appendChild(go);
    box.appendChild(row);
  }
}

// --- chat and friends -----------------------------------------------------

// One box, whichever tab is open. A private message goes down a different
// road from a room line, but a person typing should not have to know that.
$("chatform").onsubmit = (e) => {
  e.preventDefault();
  if (needName("namegate.why.chat")) return;
  const text = $("chatinput").value.trim();
  if (!text) return;
  $("chatinput").value = "";
  if (dmOpen()) { loadConversation(text); return; }
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

// --- the terms ------------------------------------------------------------

// Shown as text, in a <pre>, exactly as the server sent them. They are an
// agreement somebody is about to accept; rendering them as markup would mean
// the words on screen could differ from the words on file.
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

// --- leaving, and the network --------------------------------------------

$("btn-leave").onclick = () => act(async () => {
  await api("/api/rooms/leave", {});
  show("lobby");
});
$("btn-tolobby").onclick = () => show("lobby");

$("btn-lock").onclick = () => act(() => api("/api/rooms/status", { status: "locked_in_game" }));
$("btn-reopen").onclick = () => act(() => api("/api/rooms/status", { status: "open_to_new_players" }));
$("btn-open").onclick = () => act(() => api("/api/rooms/status", { status: "open" }));

// The team is not asked for any more: it is which seat the player is sitting
// in. Slots 1-5 are Radiant and 6-10 are Dire, which is what the room screen
// already shows, so a dropdown that could disagree with the seat was one
// place too many for the same fact to live (D57).
//
// myTeam reads the side out of the slot. A spectator, and anybody the room
// has not told us about yet, is Radiant - the game requires a side and that
// is the one it defaults to.
function myTeam() {
  const me = ((state.room && state.room.members) || [])
    .find((m) => m.player_id === state.player_id && !m.spectator);
  return me && me.slot >= 5 ? "bad" : "good";
}

// --- settings -------------------------------------------------------------

$("btn-diag").onclick = () => act(() => api("/api/diagnose", {}));
$("btn-editprofile").onclick = () => $("mebtn").onclick();
$("btn-password").onclick = () => $("p-password").onclick();
$("btn-signout").onclick = () => $("p-signout").onclick();
$("btn-showterms").onclick = () => $("readterms").onclick();

// ----------------------------------------------------------------- poll

// uiStamp is only ever set by an app started with -dev-ui. It changes when a
// file of the interface changes on disk, and the window reloads itself - the
// whole point of scripts/live.sh. The first value is remembered rather than
// acted on, or every start-up would reload once for nothing.
let uiStamp = null;

async function refresh() {
  try {
    state = await api("/api/state");
    if (state.ui_stamp) {
      if (uiStamp !== null && uiStamp !== state.ui_stamp) {
        location.reload();
        return;
      }
      uiStamp = state.ui_stamp;
    }
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
  drawSortHeads();
  showChat("lobby");
  refresh();
  setInterval(refresh, POLL_MS);
});
