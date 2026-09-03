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
// Five seats for people who want to watch rather than play. The coordinator
// allocates the same five (ipam.ObserverSlots); the admins' three are a
// separate range and are not drawn here.
// Four watching seats, two on each board (D68). Five could not be split
// evenly between two sides, and the coordinator hands out four.
const WATCH_SLOTS = 4;
const OBS_PER_SIDE = WATCH_SLOTS / 2;

let state = {};
let screen = "lobby";
let chatTab = "lobby";
let filter = "all";
let sortKey = "players";
let sortDir = "desc";
let query = "";
let busy = false;
let authMode = "signin";
// Why the gate opened, if it opened for a reason. Kept so that switching
// between the two tabs does not lose it.
let gateWhy = "";

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
    report("");
    await refresh();
  } catch (e) {
    report(e.message);
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
// What the reader has already seen and put away. Kept by its exact text: a
// dismissed strip stays down while the condition is unchanged, and a new
// sentence is a new problem and comes back up.
let bannerShut = "";

// Two different things want that one strip, and only one of them is about
// something the person just did (D83).
//
// `standing` is a condition the app keeps rediscovering by itself: the service
// is down, the tunnel tore, the room is gone. render() rewrites it on every
// poll, and writing an empty one means something - the condition ended.
//
// `report` is the reply to a button somebody just pressed. It used to live in
// the same place, so a poll that found nothing wrong wiped it about two
// seconds after it appeared, which is the whole of "some errors are not shown
// properly". A report is cleared by the next action, never by a poll.
let standingMsg = "", standingRetry = false, reportMsg = "";

function report(msg) {
  reportMsg = msg || "";
  paintBanner();
}

function banner(msg, retry) {
  standingMsg = msg || "";
  standingRetry = !!retry;
  paintBanner();
}

function paintBanner() {
  const msg = reportMsg || standingMsg;
  const retry = !reportMsg && standingRetry;
  const b = $("banner");
  if (!msg) {
    bannerShut = "";
    b.textContent = "";
    b.classList.add("hidden");
    return;
  }
  if (msg === bannerShut) {
    b.classList.add("hidden");
    return;
  }
  // Rebuilt only when the sentence itself changes, so the buttons on it stay
  // clickable and keep the keyboard's place.
  if (b.dataset.msg !== msg + (retry ? "!" : "")) {
    b.dataset.msg = msg + (retry ? "!" : "");
    b.textContent = "";
    b.appendChild(el("span", "glyph", "\u26A0"));
    b.appendChild(el("span", "grow", msg));
    if (retry) {
      const again = el("button", "act", t("banner.retry"));
      again.onclick = () => act(() => api("/api/connect", {}));
      b.appendChild(again);
    }
    const shut = el("button", "shut", "\u00D7");
    shut.title = t("banner.dismiss");
    shut.onclick = () => {
      bannerShut = msg;
      b.classList.add("hidden");
    };
    b.appendChild(shut);
  }
  b.classList.remove("hidden");
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
  gateWhy = why;
  gateMode(authMode);
  $("nameerr").textContent = "";
  $("namegate").classList.remove("hidden");
  ($("accountform").classList.contains("hidden") ? $("nameinput") : $("a-user")).focus();
  return true;
}

// gateMode switches the gate between its three shapes: a name, signing in, or
// creating an account. All three live in the same card and the card does not
// move between them, so switching tabs does not replay the entrance.
function gateMode(mode) {
  authMode = mode;
  const accounts = !!state.accounts;
  const signup = accounts && mode === "signup";
  $("nickonly").classList.toggle("hidden", accounts);
  $("accountform").classList.toggle("hidden", !accounts);
  $("authtabs").classList.toggle("hidden", !accounts);
  for (const f of document.querySelectorAll(".signup-only")) {
    f.classList.toggle("hidden", !signup);
  }
  for (const f of document.querySelectorAll(".signin-only")) {
    f.classList.toggle("hidden", signup);
  }
  $("authfields").classList.toggle("pair", signup);
  for (const tab of document.querySelectorAll(".modetabs .chattab")) {
    tab.classList.toggle("active", tab.dataset.mode === mode);
  }
  $("a-pass").setAttribute("autocomplete",
    signup ? "new-password" : "current-password");

  // Naming yourself and signing back in are different errands and the card
  // says which one it is. The reason the gate opened - somebody pressed Join
  // with no name - outranks the standing blurb, but only on the side where it
  // makes sense: it is about picking a name, not about remembering one.
  const naming = !accounts || signup;
  $("gatetitle").textContent = t(naming ? "auth.signup.title" : "auth.signin.title");
  $("namewhy").textContent = t(naming
    ? (gateWhy || "auth.signup.blurb") : "auth.signin.blurb");
  $("gatego").textContent = t(!accounts ? "namegate.submit"
    : signup ? "auth.signup" : "auth.signin");
  // Once now, rather than on the next poll: a card that opens blank and fills
  // in two seconds later looks like a card that is still loading.
  drawGateStatus(state);
}

// The card's own two live facts: whether the server is answering, and how many
// people are already inside. Both are readable before signing in, which is the
// point - the door should say whether it is worth opening.
function drawGateStatus(s) {
  const up = !s.coordinator_error;
  $("authnet").classList.toggle("off", !up);
  $("authnettext").textContent = !up ? t("auth.relay.down")
    : s.relay_ms ? t("auth.relay", { n: s.relay_ms }) : t("auth.relay.up");
  $("authfoot").textContent = s.online === undefined ? ""
    : t("auth.foot", { n: s.online || 0, r: (s.rooms || []).length });
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

// pressable makes a div that acts like a button act like one for somebody who
// is not holding a mouse.
//
// A room row, an empty seat and a friend are all whole-row targets: the click
// is on the row rather than on a button inside it, because the row is what a
// player is aiming at. That is right, and it left every one of them reachable
// only by pointer - no tab stop, no Enter, nothing announced. This gives them
// the three things a button has and nothing else: a stop in the tab order,
// the role, and the two keys.
//
// Buttons nested inside these rows already stop their own clicks; they stop
// keys here too, or Enter on a Kick button would also take its seat.
// pressable makes something that is not a button behave like one for a
// keyboard. The optional second argument is what Enter should do, for the
// things whose pointer gesture is not a single click - a room row joins on a
// double click (D90), and a keyboard has no such thing.
function pressable(e, go) {
  e.tabIndex = 0;
  e.setAttribute("role", "button");
  e.onkeydown = (ev) => {
    if (ev.key !== "Enter" && ev.key !== " ") return;
    if (ev.target !== e) return;
    ev.preventDefault();
    if (go) go(ev);
    else if (e.onclick) e.onclick(ev);
  };
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
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 290;
  // The greens are reserved. Your own face is a fixed green so that you can
  // be found in a list of ten without reading a name, and that only works if
  // nobody else's hash can land on one.
  return h < 100 ? h : h + 70;
}

// redraw says whether a panel has to be rebuilt at all (D71).
//
// Every list on this screen used to be emptied and refilled on every poll,
// whether or not anything in it had changed. Two seconds is short enough that
// it was visible, and it is what the owner reported as the app "glitching": a
// scrolled list jumped back to the top under the reader, a hovered row lost
// its highlight, and a click that arrived on the wrong side of a rebuild
// landed on an element that no longer existed.
//
// The chat log has had this guard since it was written, for exactly this
// reason, and the comment there says so. Everything else has it now. The
// signature has to name every input the panel draws from, including the ones
// that are not in its argument - which is why several of them reach into
// state for the player's own id or for whether this PC is connected.
// The numbers that change by themselves. Nothing structural depends on them,
// and everything that draws them draws them into a node of its own.
const LIVE_KEYS = new Set(["relay_ms", "host_relay_ms"]);
function steady(key, value) {
  return LIVE_KEYS.has(key) ? undefined : value;
}

// grade turns a measurement into the one word the colour is saying. Zero is
// not a good connection, it is no reading, and the two must never look the
// same (D54).
function grade(ms) {
  return !ms ? "" : ms < 60 ? "good" : ms < 140 ? "fair" : "poor";
}

function redraw(node, sig) {
  if (node.dataset.sig === sig) return false;
  node.dataset.sig = sig;
  return true;
}

function avatar(name, id, cls) {
  const e = el("div", "avatar" + (cls ? " " + cls : ""), initials(name));
  // Your own face is always the same green, whoever you are (D68). Everybody
  // else keeps their id's colour, so the same person is recognisable across
  // screens; what this buys is the other half - finding yourself in a list of
  // ten without reading a single name.
  if (id && id === state.player_id) {
    e.className += " me";
    e.title = name || "";
    return e;
  }
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

  // A tunnel that tore down is the one of these the player can do something
  // about, so that one gets the button.
  //
  // The service reports a teardown in its own words - "lease expired locally"
  // - which is accurate, untranslated, and tells nobody what happened or what
  // to do. The app names a key for the reasons it knows and the raw text is
  // the fallback for the ones it does not (D77).
  const tunnelTrouble = s.tunnel_error
    ? (s.tunnel_error_key ? t(s.tunnel_error_key) : s.tunnel_error)
    : "";
  const trouble = s.service_error || s.coordinator_error || tunnelTrouble ||
    (s.build_warning || "");
  banner(trouble, !!tunnelTrouble && trouble === tunnelTrouble);

  // The room closing under you, and being removed from one, are events rather
  // than conditions: the app is told once, on one poll, and never again. They
  // used to be written into the standing strip alongside the conditions
  // above, which meant the most important sentence the app can say - the room
  // you were sitting in is gone - was on screen for about two seconds and
  // then wiped by the next poll finding nothing wrong (D83). They go through
  // the same channel as a failed button now, and stay until the person does
  // something.
  if (s.room_gone) report(t("err.room_gone"));
  if (s.removed) report(t("err.removed"));

  // Terms that changed after somebody signed up are terms they have not
  // agreed to. Shown only where all three facts are known: signed in, a
  // version in force, and this account not on it.
  $("termsmoved").classList.toggle("hidden",
    !(s.signed_in && s.terms_version && s.terms_accepted === false));

  // The front door carries live numbers, so it is redrawn while it is open.
  if (!$("namegate").classList.contains("hidden")) drawGateStatus(s);

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

// The rail's two lights (D42.5, moved there by D68). Being in a room and
// being on its network are different states, and this is the permanent
// reminder of which one you are in. The service light above it answers the
// other half: whether this PC can join a room network at all.
function renderConnection(s) {
  const svc = $("rs-service");
  svc.className = "rs" + (s.service ? " up" : " down");
  $("servicetext").textContent = t(s.service ? "rail.service.up" : "rail.service.down");

  let cls = "bad", key = "rail.tunnel.off";
  if (s.connected) {
    cls = "ok"; key = "rail.tunnel.on";
  } else if (s.tunnel === "connecting") {
    cls = "wait"; key = "rail.tunnel.wait";
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
  if (!redraw(box, JSON.stringify(ads))) return;
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
  drawCreateButton();
  const shown = sortRooms(visible(rooms));
  $("roomcount").textContent = t("lobby.shown", { shown: shown.length, all: rooms.length });

  // Which room is mine and who I am both change what a row looks like, and
  // neither of them is in the list itself.
  //
  // The signature is per row, not per list: rebuilding forty rows because one
  // of them gained a player is what made the lobby flicker, and it threw away
  // whichever row the pointer or the keyboard happened to be on.
  if (shown.length) {
    reconcileRooms(box, shown);
    return;
  }
  if (!redraw(box, "empty:" + rooms.length)) return;
  box.textContent = "";
  {
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
}

// reconcileRooms puts the rows in the right order, makes the ones that have
// genuinely changed, and leaves the rest of them alone.
//
// A row that is kept still has its ping repainted: that is the one part of it
// that moves on its own, and it is a leaf with nothing in it to lose.
function reconcileRooms(box, shown) {
  delete box.dataset.sig;
  const have = new Map();
  for (const node of Array.from(box.children)) {
    if (node.dataset && node.dataset.room) have.set(node.dataset.room, node);
    else box.removeChild(node);
  }

  const want = [];
  for (const r of shown) {
    // Whether I am in *any* room matters to every row, not just to mine:
    // joining one disables the button on all the others.
    const sig = JSON.stringify(
      [r, r.id === state.room_id, !!state.room_id, state.player_id], steady);
    const old = have.get(r.id);
    let node = old;
    if (!old || old.dataset.sig !== sig) {
      node = roomCard(r);
      node.dataset.room = r.id;
      node.dataset.sig = sig;
      // The row this one replaces has to leave the document, not just this
      // map. Deleting it from the map alone is what put a second copy of the
      // owner s own room in the lobby the moment they created one: the new
      // node was inserted, the old one was no longer anybody s to remove, and
      // every further change to that row left another orphan behind it.
      if (old) box.replaceChild(node, old);
    } else {
      paintRoomPing(node, r);
    }
    have.delete(r.id);
    want.push(node);
  }
  for (const dead of have.values()) box.removeChild(dead);

  // Only the rows that actually moved are touched. insertBefore on a node
  // already in place would still be a move, and a move resets a transition.
  let i = 0;
  for (const node of want) {
    if (box.children[i] !== node) box.insertBefore(node, box.children[i] || null);
    i++;
  }
}

function paintRoomPing(row, r) {
  const old = row.querySelector(".room-ping");
  if (old) row.replaceChild(pingCell(r), old);
}

// One row per room: who is hosting it, one line of everything else, and the
// three numbers a player chooses on. The whole row opens the room; only the
// button in the last column joins it.
function roomCard(r) {
  const mine = r.id === state.room_id;
  // Two ways in, at the owner's word (D90): the Join button on one click, the
  // row itself on two. A single click on the row does nothing on purpose -
  // it is a list somebody drags a pointer down while reading, and a list that
  // joins a room when the pointer lands on it is a trap.
  const go = () => (mine ? show("room") : joinRoom(r));
  const card = pressable(el("div", "room" + (mine ? " here" : "")), go);
  card.ondblclick = go;

  const who = el("div", "room-who");
  who.appendChild(avatar(r.host_nick, r.host_id));
  const about = el("div", "grow");
  about.appendChild(el("div", "room-name", r.name));

  // Everything else about the room on one line, in the order a player asks
  // for it. It is allowed to run out of room and be cut; the name is not.
  const meta = el("div", "room-meta");
  meta.appendChild(statusBadge(r.status));
  // What game this room is playing, beside whether it is open (D81). It is
  // its own element rather than one more entry in the run-on line below,
  // because that line is allowed to run out of space and be cut - and a mode
  // somebody is scanning for must not be the thing that gets cut.
  meta.appendChild(el("div", "room-mode", modeName(r.game_mode)));
  // Which door this room has, as a mark rather than as words (D88). It was
  // three phrases on the end of the run-on line below - the line that is
  // allowed to run out of space and be cut - so the fact that a room wanted a
  // password was the first thing to disappear on a narrow window. A door is
  // one of the things somebody scans the lobby *for*, so by rule it gets its
  // own element beside the badge and is never cut (D81).
  //
  const bits = [r.host_nick];
  if (r.description) bits.push(r.description);
  meta.appendChild(el("span", "rest", bits.join(" · ")));
  // The room you are in used to say "You are here" at the end of its meta
  // line. It is the green row now (D81) - the whole row answers the question,
  // in the colour this app already uses for "this is on and it is yours", and
  // it answers it from across the screen instead of at the end of a line that
  // might have been cut.
  about.appendChild(meta);
  who.appendChild(about);
  card.appendChild(who);

  card.appendChild(seatCell(r));
  card.appendChild(mmrCell(r));
  card.appendChild(pingCell(r));
  // The door is a column of its own now, next to the button and the live dot
  // (D89). It began beside the game-mode badge inside the meta line, which
  // solved the thing that was actually wrong - the door used to be words on
  // the end of a line that gets cut - but left it in a different place on
  // every row, so it could only be read one room at a time. In a column it
  // reads down the list.
  card.appendChild(doorMark(r));
  card.appendChild(roomActions(r));
  return card;
}

// doorMark draws the room's door, or nothing at all for a room anybody may
// walk into. The shapes themselves are in the stylesheet, drawn rather than
// typed: a glyph would come from whichever font the machine happens to have,
// and on Windows the obvious ones arrive as colour emoji in a product that is
// otherwise entirely monochrome.
function doorMark(r) {
  const wrap = el("span", "doors");
  const add = (kind, key) => {
    const m = el("span", "door " + kind);
    m.title = t(key);
    m.setAttribute("aria-label", t(key));
    m.setAttribute("role", "img");
    wrap.appendChild(m);
  };
  // A password and a members-only door are different things and a room can
  // have both, so these are not exclusive.
  if (r.privacy === "friends") add("friends", "lobby.door.friends");
  if (r.privacy === "invite") add("invite", "lobby.door.invite");
  if (r.needs_password) add("lock", "lobby.door.password");
  return wrap;
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
  const cell = el("div", "room-ping rcol-hide " + grade(ms));
  cell.title = ms ? t("lobby.ping.explain") : t("lobby.ping.none");
  // Five bars, not three. Three cannot show "fair" as anything but "not
  // good", and the meter is the only part of the row read without looking
  // straight at it.
  const bars = el("span", "bars");
  const lit = !ms ? 0 : ms < 40 ? 5 : ms < 60 ? 4 : ms < 100 ? 3 : ms < 140 ? 2 : 1;
  for (let i = 0; i < 5; i++) bars.appendChild(el("i", i < lit ? "on" : ""));
  cell.appendChild(bars);
  cell.appendChild(document.createTextNode(
    ms ? t("lobby.ping.value", { n: ms }) : t("lobby.ping.unknown")));
  return cell;
}

// The last column: one button, and a dot saying whether a match is running
// in there. The dot is the only thing on the row that can be read without
// looking directly at it.
// Create room is live whether or not you are in one (D90). It was switched
// off, on the grounds that the coordinator refuses a create from somebody who
// is already seated - which it still does. The interface does the leaving now,
// exactly as it does for Join, and asks the same question first.
function drawCreateButton() {
  $("btn-create").disabled = false;
}

// Being in a room no longer stops you joining another one (D89). One person
// is still in one room at a time (D82) - that has not changed and cannot,
// because the coordinator enforces it - but the interface now does the
// leaving for you instead of refusing and telling you to go and do it
// yourself. GameRanger did this and it is the reason its lobby felt free to
// move around in.
function roomActions(r) {
  const acts = el("div", "room-actions");
  const mine = r.id === state.room_id;
  const b = el("button", mine || r.joinable ? "primary" : "",
    mine ? t("room.open") : (r.seats || 0) >= 10 ? t("room.full")
      : inGame(r) ? t("room.ingame") : t("room.join"));
  b.disabled = !mine && !r.joinable;
  b.title = mine || r.joinable ? "" : t("room.join.closed");
  b.onclick = (e) => { e.stopPropagation(); mine ? show("room") : joinRoom(r); };
  acts.appendChild(b);

  const dot = el("span", "livedot " + (inGame(r) ? "game" : "open"));
  dot.title = t(inGame(r) ? "lobby.live.game" : "lobby.live.open");
  acts.appendChild(dot);
  return acts;
}

// joinRoom asks for the password only when the room actually has one, so an
// open room is one click and a locked one is honest about why it is asking.
async function joinRoom(r) {
  if (needName("namegate.why.join")) return;
  if (r.id === state.room_id) { show("room"); return; }

  if (state.room_id && !(await askLeave())) return;

  let password = "";
  if (r.needs_password) {
    password = window.prompt(t("lobby.door.ask")) || "";
    if (!password) return;
  }
  enterRoom(r.id, password);
}

// askLeave is the one question in front of both of the ways out of a room you
// are in: joining another, and creating one. The host gets a second sentence
// because leaving closes their room at once, for everybody in it, with no
// grace (D84) - which is a different size of consequence and has to be said.
function askLeave() {
  return askGate("room.switch.title",
    state.is_host ? "room.switch.host" : "room.switch.player",
    "room.switch.go", "room.switch.no");
}

// enterRoom is every way into a room: the lobby row, the button on it, a
// friend's room, an invitation. It leaves the room you are in first if there
// is one, because the coordinator refuses a join from somebody who is already
// seated - the rule is the coordinator's and is not going anywhere, so the
// interface keeps it by doing the two steps rather than by disabling things.
//
// Joining puts you in the room you joined. It used to leave you in the lobby
// looking at a row that had quietly become yours, with nothing saying so but
// a colour, and the one thing anybody wants after joining is to see who is in
// there.
function enterRoom(id, password) {
  act(async () => {
    if (state.room_id && state.room_id !== id) {
      await api("/api/rooms/leave", {});
      state.room_id = "";
    }
    await api("/api/rooms/join", { room_id: id, password: password || "" });
    show("room");
  });
}

// The three that are not "open" each get their own class, and each of those
// classes now has a colour (D89). They shared one for a long time and it did
// not matter, because none of them had ever been given a single line of
// styling: in game, closed and needs-a-player were all painted in the green
// that means open.
function statusClass(status) {
  if (status === "locked_in_game") return "badge locked";
  if (status === "closed") return "badge shut";
  if (status === "open_to_new_players") return "badge replace";
  return "badge";
}

function statusLabel(status) {
  // One status the room does not store: the coordinator derives it from what
  // the host's own machine is doing (D69). There was a second, "Host away",
  // for a room counting down because its host had gone quiet - since D84 that
  // room is closed instead, and there is nothing left to label.
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
  const face = $("room-face");
  if (redraw(face, JSON.stringify([r.host_id, r.host_nick]))) {
    face.textContent = "";
    face.appendChild(avatar(r.host_nick, r.host_id));
  }
  $("room-name").textContent = r.name;

  // Who is hosting, how full it is and what the addresses are all moved into
  // the stat strip below the rule (D68). What is left is the host's own
  // sentence, which has no number to sit beside and goes in the footer.
  $("room-desc").textContent = r.description || "";

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

  // Observers are numbered in their own range, so an observer in seat 0 and
  // the host in slot 0 are not the same seat and must not share a key.
  const watching = {};
  for (const m of r.members || []) if (m.spectator && m.seat !== "admin") watching[m.slot] = m;

  // Slots 0-4 are Radiant and 5-9 are Dire, which is how the game itself
  // divides them. Drawing all ten in one list hid the only structural fact
  // about a room: which five you would be joining. The watching seats belong
  // to a side too now (D68): the first two to Radiant, the last two to Dire.
  const box = $("slots");
  // Twenty seat cards rebuilt every two seconds is the worst of these: it is
  // the thing under the pointer while somebody is choosing where to sit. Who
  // is in them, who I am, and whether the room will accept a seat change at
  // all are the whole of what a seat draws from.
  const sig = JSON.stringify([r.members || [], r.status, r.host_id,
    state.player_id, iAmHost], steady);
  if (redraw(box, sig)) {
    box.textContent = "";
    box.appendChild(teamColumn("radiant", "room.team.radiant", 0, seated, watching, iAmHost));
    box.appendChild(teamColumn("dire", "room.team.dire", 5, seated, watching, iAmHost));
  }
  // Always, whether or not the boards were rebuilt: this is the number that
  // moved on its own, and repainting it is why they no longer have to be.
  paintSeatPings(box, seated, watching);

  drawStats(r);
  drawAction(r);
  drawNetBanner();
}

// drawStats is the band's second tier: the five facts somebody checks when a
// match will not start, labelled, in the order they check them.
//
// They were one run-on line at the very bottom of the screen. That is the
// last place a person looks and the first thing they need, and the line said
// "10.87.0.7 · 10.87.0.2 · 37 ms" with nothing saying which was which.
function drawStats(r) {
  const box = $("roomstats");
  // Four of the five cells come from this PC rather than from the room.
  const sig = JSON.stringify([r.host_nick, r.seats, r.game_mode, state.virtual_ip,
    state.host_ip, state.connected, state.tunnel, !!state.relay_ms, state.is_host]);
  if (!redraw(box, sig)) {
    const pill = box.querySelector(".mspill");
    if (pill) pill.textContent = t("checks.ms", { n: state.relay_ms });
    return;
  }
  box.textContent = "";

  // Each cell carries its own key as a class - room.stat.you becomes
  // "stat-room-stat-you" - so that a cell can be hidden from the stylesheet
  // without touching what builds it. That is what the owner asked for: hide,
  // do not remove (D88).
  const cell = (labelKey, fill) => {
    if (box.children.length) box.appendChild(el("span", "statrule"));
    const c = el("div", "stat stat-" + labelKey.replace(/\./g, "-"));
    c.appendChild(el("div", "k", t(labelKey)));
    const v = el("div", "v");
    fill(v);
    c.appendChild(v);
    box.appendChild(c);
  };

  cell("room.stat.host", (v) => { v.textContent = r.host_nick; });
  cell("room.stat.players", (v) => {
    v.appendChild(el("span", "", String(r.seats || 0)));
    v.appendChild(el("span", "faint", t("room.stat.of10")));
  });
  // What game this is. It sits with the room's own facts rather than with
  // the addresses below it, because it is the second thing anybody asks
  // about a lobby after who is in it, and because it was previously visible
  // to exactly one person: the host, inside a dialog.
  cell("room.stat.mode", (v) => { v.textContent = modeName(r.game_mode); });
  cell("room.stat.you", (v) => {
    v.textContent = state.virtual_ip || t("status.dash");
  });
  cell("room.stat.hostaddr", (v) => {
    v.textContent = state.host_ip || t("status.dash");
  });

  // The one cell that is also a control. Getting on and off a room's network
  // is the first thing to try when Dota cannot find the host, and this is
  // where somebody is already looking when that happens.
  cell("room.stat.net", (v) => {
    const on = !!state.connected;
    const wait = !on && state.tunnel === "connecting";
    v.appendChild(el("span", "netdot " + (on ? "ok" : wait ? "wait" : "off")));
    v.appendChild(el("span", on ? "netword ok" : "netword",
      t(on ? "room.net.on" : wait ? "room.net.wait" : "room.net.off")));
    if (on && state.relay_ms) {
      v.appendChild(el("span", "mspill", t("checks.ms", { n: state.relay_ms })));
    }
    // A host never leaves their own room's network: their machine is the
    // game, and dropping it ends the match for everybody in it.
    if (on && !state.is_host) {
      const off = el("button", "netlink", t("room.net.leave"));
      off.title = t("room.net.leave.note");
      off.onclick = () => act(() => api("/api/disconnect", {}));
      v.appendChild(off);
    } else if (!on && !wait) {
      const go = el("button", "netlink", t("room.net.join"));
      go.onclick = () => act(() => api("/api/connect", {}));
      v.appendChild(go);
    }
  });
}

// drawAction is the room's single button (D68).
//
// It mirrors GameRanger: Create Game when the room is yours, Join Game when
// it is not, in the same place either way. One click brings the tunnel up and
// opens Dota, because those were two deliberate clicks and the second one was
// the one people forgot.
//
// The three steps it replaced are not gone - they are the guide behind the
// (i), and the network cell in the band above says which of them this PC has
// actually done. That was the thing the stepper was for: the commonest
// failure in the two-PC test was two players in a room, neither on its
// network, with nothing on screen saying so.
function drawAction(r) {
  const b = $("btn-step");
  const mine = (r.members || []).find((m) => m.player_id === state.player_id);
  const watching = mine && mine.spectator;

  // A watching seat is a seat, and Dota has a spectator side to sit on
  // (D81). The host in the gallery still starts the match - their PC is the
  // server whether or not they are playing on it - and everybody else in the
  // gallery goes in to watch. Both arrive with +jointeam spec, which is what
  // leaves all ten playing slots for players.
  let key = state.is_host ? "room.go.create" : watching ? "room.go.watch" : "room.go.join";
  let why = "";
  let off = false;

  if (state.dota_running) {
    key = "room.go.running"; off = true;
  } else if (!mine) {
    off = true; why = t("room.go.needseat");
  }
  // A locked room does not stop the nine people already seated in it from
  // starting Dota, and that is the whole flow: the host presses Create Game,
  // the room locks because the host is now in a match (D69), and everybody
  // else presses Join Game. Locking decides who may come in, and they are
  // already in.

  b.textContent = t(key);
  b.disabled = off;
  b.title = why;
  // No mode here. It used to read the host's own dropdown at the moment of
  // the click, which meant the room's game mode lived in one person's window
  // and was decided a fraction of a second before Dota started. It belongs to
  // the room now (D80), and the app asks the coordinator for it.
  b.onclick = off ? null : () => act(() => api("/api/playnow", { team: myTeam() }));
}

// The guide strip: the same three steps, one line, closed by default and
// remembered per installation. A returning player has learned the flow; a
// band of instructions they cannot dismiss is the app talking over them.
function drawGuide() {
  const on = guideOpen();
  $("guide").classList.toggle("hidden", !on);
  $("btn-guide").classList.toggle("on", on);
}

function guideOpen() {
  try {
    return window.localStorage.getItem("guide") === "open";
  } catch (e) {
    // A browser with site data switched off, or a private window. The guide
    // simply does not remember; nothing else about the room depends on it.
    return false;
  }
}

function setGuide(on) {
  try {
    window.localStorage.setItem("guide", on ? "open" : "shut");
  } catch (e) { /* see guideOpen */ }
  drawGuide();
}


// modeName turns a Dota game mode number into the words for it.
//
// The menu in the markup is the only list: every option carries the mode's
// number and its string key, so this reads the answer out of the same place
// the host chose it from rather than keeping a second copy that can drift.
// The numbers are Valve's own DOTA_GameMode values and reach a real command
// line, which is why protocol/gamemode and this menu are checked against each
// other by a test rather than by hand.
function modeName(id) {
  const opt = $("mode").querySelector('option[value="' + Number(id || 0) + '"]');
  return opt ? t(opt.dataset.t) : t("status.dash");
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
  // The mode the room is actually playing, not whatever this dropdown was
  // last left on. A host who opens room settings after somebody else's
  // change - or after a reconnect - must see the room's answer.
  if (document.activeElement !== $("mode")) {
    $("mode").value = String(r.game_mode || 1);
  }
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

// drawNetBanner is what the three numbered steps above it cannot say.
//
// It used to repeat them - a green line for "connected" directly under a
// green tick reading "connected", and an amber one under step 2 while step 2
// already said the same thing. Two of the room screen's scarcest inches
// spent telling somebody what they had just read.
//
// A failure is different. Nothing in the stepper can carry the reason a
// connection was refused, and that reason is the whole of what a player
// needs when the thing they pressed did not work.
function drawNetBanner() {
  const e = $("netbanner");
  if (state.connect_error) {
    e.hidden = false;
    e.className = "netbanner bad";
    e.textContent = t("net.failed", { error: state.connect_error });
    return;
  }
  // The room is frozen while the host is in a match (D69), and saying so is
  // the difference between a rule and a screen that has stopped responding.
  // Second to a failure, which is the more urgent of the two.
  const r = state.room;
  if (r && r.host_in_game) {
    e.hidden = false;
    e.className = "netbanner";
    e.textContent = t("room.locked.note");
    return;
  }
  e.hidden = true;
  e.textContent = "";
}

// teamColumn draws one side: a heading in that side's colour, how many of
// its five seats are taken, the seats themselves, and the two watching seats
// that belong to this side (D68).
//
// The watching seats used to be a panel of their own across the full width.
// Putting them on a board says the thing that is actually true of them: an
// observer sits with a team, and the two boards are then the whole room.
function teamColumn(side, titleKey, first, seated, watching, canKick) {
  const col = el("div", "team " + side);
  const head = el("div", "team-head");
  head.appendChild(el("span", "swatch"));
  head.appendChild(el("span", "", t(titleKey)));

  let taken = 0;
  for (let i = first; i < first + 5; i++) if (seated[i]) taken++;
  const obsFirst = side === "radiant" ? 0 : OBS_PER_SIDE;
  let obs = 0;
  for (let i = obsFirst; i < obsFirst + OBS_PER_SIDE; i++) if (watching[i]) obs++;

  head.appendChild(el("div", "head-gap"));
  head.appendChild(el("span", "n",
    t("room.team.count", { n: taken }) + " · " + t("room.team.obs", { n: obs })));
  col.appendChild(head);

  for (let i = first; i < first + 5; i++) {
    col.appendChild(slotCard(i, seated[i], canKick));
  }

  const obshead = el("div", "obs-head");
  obshead.appendChild(el("span", "swatch"));
  obshead.appendChild(el("span", "", t("room.watch")));
  obshead.appendChild(el("div", "head-gap"));
  obshead.appendChild(el("span", "n", t("room.team.obs", { n: obs })));
  col.appendChild(obshead);
  for (let i = obsFirst; i < obsFirst + OBS_PER_SIDE; i++) {
    col.appendChild(slotCard(i, watching[i], canKick, true));
  }
  return col;
}


// canTakeSeat answers whether clicking this empty seat would do anything.
//
// Which slot you sit in is which team you are on, so this is how a player
// picks a side. The refusals mirror the coordinator's exactly, because a card
// that invites a click and then shows an error is worse than one that does
// not invite it.
//
// The host is a player here like anybody else (D64), and since D79 every seat
// on the screen is open to everybody in the room, the gallery included. Both
// of those were rules the host alone was refused by, and both of them meant
// the person who had opened the room was the one person who could not sit
// where they wanted in it.
//
// So for anybody in the room, every empty seat is takeable until the match
// starts. Somebody not in the room yet can still only arrive in the gallery:
// there is no door that seats an arrival in a chosen playing slot, and
// offering one that quietly puts them somewhere else is worse than not
// offering it.
function canTakeSeat(spectator) {
  if (!state.room || state.room.status === "locked_in_game") return false;
  return !!seated() || !!spectator;
}

// seated: am I in this room at all, and where? Returns the member record, so
// a caller can tell a move from an arrival and a playing seat from a watching
// one - the two arrive through different doors on the coordinator.
function seated() {
  return ((state.room && state.room.members) || [])
    .find((m) => m.player_id === state.player_id);
}

function slotCard(index, member, canKick, spectator) {
  const card = el("div");
  const mine = member && member.player_id === state.player_id;
  card.className = "slot" + (spectator ? " watch" : "")
    + (member ? " taken" : " empty") + (mine ? " you" : "");
  // Observers are numbered in their own range, and shown that way: O1 and O2
  // on Radiant, O3 and O4 on Dire. A "1" on both kinds of seat in the same
  // column would be two different seats with the same name.
  card.dataset.seat = (spectator ? "o" : "p") + index;
  card.appendChild(el("div", "slot-num",
    spectator ? t("room.watch.seat", { n: index + 1 }) : String(index + 1)));

  if (member) card.appendChild(avatar(member.nick, member.player_id, "sm"));

  const body = el("div", "slot-body");
  card.appendChild(body);
  if (!member) {
    // The label says what the seat is, not what to do with it (D68). The
    // affordance is the row lighting up under the pointer; eight rows that
    // each read "Sit here" is the instruction printed eight times.
    body.appendChild(el("div", "slot-name muted", t("room.slot.empty")));
    if (canTakeSeat(spectator)) {
      card.classList.add("takeable");
      pressable(card);
      card.title = t(spectator ? "room.watch.take.note" : "room.slot.take.note");
      // Already in the room: this is a move, and a move keeps the address, so
      // nothing about the network notices (D74, D79). Not in it yet: this is
      // an arrival, and arrivals come through their own doors.
      card.onclick = () => act(() => (seated()
        ? api("/api/rooms/slot", { slot: index, watching: !!spectator })
        : api("/api/rooms/spectate", { room_id: state.room_id })));
    }
    return card;
  }

  const mmr = member.mmr ? t("status.mmr", { n: member.mmr }) : t("status.nomm");
  const name = el("div", "slot-name");
  name.appendChild(document.createTextNode(member.nick));
  if (mine) name.appendChild(el("span", "mine", " " + t("room.slot.mine")));
  body.appendChild(name);
  body.appendChild(el("div", "slot-sub",
    member.is_host ? t("room.slot.host", { sub: mmr }) : mmr));

  // Each player's own distance from the relay, which is the number that
  // decides how the game will feel for them. It is theirs, not the reader's:
  // everybody in a room reaches everybody else through the relay, so a poor
  // one here is a poor one for that person alone.
  card.appendChild(seatPing(member));

  if (canKick && !member.is_host && !mine) {
    const b = el("button", "", t("room.kick"));
    b.title = t("room.kick.note");
    b.onclick = (e) => {
      e.stopPropagation();
      act(() => api("/api/rooms/kick", { target: member.player_id }));
    };
    card.appendChild(b);
  }
  return card;
}

// A player who has not reported a measurement yet gets nothing rather than a
// zero. Zero milliseconds is not a good connection, it is no reading, and
// the two must never look the same.
function seatPing(member) {
  const e = el("span", "seat-ping");
  paintPing(e, member.relay_ms || 0);
  return e;
}

function paintPing(e, ms) {
  e.className = "seat-ping " + grade(ms);
  e.textContent = ms ? t("lobby.ping.value", { n: ms }) : "";
  if (ms) e.title = t("room.ping.explain");
  else e.removeAttribute("title");
}

// Twenty seats, each with a number that changes on its own. Painted in place
// so that a new measurement is a new number rather than a new board.
function paintSeatPings(box, seated, watching) {
  for (const card of box.querySelectorAll(".slot[data-seat]")) {
    const e = card.querySelector(".seat-ping");
    if (!e) continue;
    const key = card.dataset.seat;
    const who = (key[0] === "o" ? watching : seated)[Number(key.slice(1))];
    paintPing(e, (who && who.relay_ms) || 0);
  }
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
  // An invitation names a room, and the name comes from the lobby list rather
  // than from the invitation, so the rooms belong in the signature too.
  const sig = JSON.stringify([list, why, state.accounts, state.signed_in,
    (state.rooms || []).map((r) => [r.id, r.name])]);
  if (!redraw(box, sig)) return;
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

  invitationRows(box, list);

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

// invitationRows draws the rooms friends have asked this player into.
//
// The coordinator has stored these since T7 and the app has fetched them
// since T11; nothing ever drew them, so being invited to a room looked
// exactly like not being invited to one. They sit above the friends
// themselves because an invitation is time-limited in a way a friend is not:
// the room it names is filling up while it is being ignored.
function invitationRows(box, list) {
  const invites = (list && list.invitations) || [];
  if (!invites.length) return;

  const friends = (list.friends || []).concat(list.incoming || [], list.outgoing || []);
  box.appendChild(el("div", "friend-group", t("friends.invited")));
  for (const inv of invites) {
    const who = friends.find((f) => f.player_id === inv.from_id);
    const room = (state.rooms || []).find((r) => r.id === inv.room_id);

    const row = el("div", "friend invite");
    const port = el("div", "who-av");
    port.appendChild(avatar(who ? who.display_name : inv.from_id, inv.from_id, "sm"));
    row.appendChild(port);

    const body = el("div", "who");
    body.appendChild(el("div", "name",
      t("friends.invited.by", { who: who ? who.display_name : inv.from_id })));
    // A room this player cannot see is a private one they have just been let
    // into. Saying "a room" is the truth; inventing a name would not be.
    body.appendChild(el("div", "where", room ? room.name : t("friends.invited.hidden")));
    row.appendChild(body);

    const acts = el("div", "acts");
    acts.style.opacity = "1";
    if (room && room.joinable) {
      const go = el("button", "primary tiny", t("friends.join"));
      go.onclick = async () => {
        await act(() => api("/api/friends/invitations/seen", {}));
        joinRoom(room);
      };
      acts.appendChild(go);
    }
    const no = el("button", "tiny", t("friends.invited.dismiss"));
    no.onclick = () => act(() => api("/api/friends/invitations/seen", {}));
    acts.appendChild(no);
    row.appendChild(acts);
    box.appendChild(row);
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
  if (!f.online) return f.last_seen ? t("friends.lastseen", { when: ago(f.last_seen) }) : t("friends.offline");
  if (f.in_game) return t("friends.ingame");
  if (f.room_id) {
    const room = (state.rooms || []).find((r) => r.id === f.room_id);
    return room ? t("friends.inroom", { room: room.name }) : t("friends.inroomhidden");
  }
  return t("friends.online");
}

// ago turns a timestamp into how long ago that was, in the coarsest unit
// that is still true.
//
// "Last seen 2h ago" is what somebody wants from a friends list - whether it
// is worth waiting for that person. A clock time would make them work out the
// difference, and a date would make them work out whether it was today.
function ago(stamp) {
  const then = new Date(stamp).getTime();
  if (!then || isNaN(then)) return "";
  const mins = Math.floor((Date.now() - then) / 60000);
  if (mins < 1) return t("ago.now");
  if (mins < 60) return t("ago.minutes", { n: mins });
  const hours = Math.floor(mins / 60);
  if (hours < 24) return t("ago.hours", { n: hours });
  const days = Math.floor(hours / 24);
  if (days === 1) return t("ago.yesterday");
  if (days < 30) return t("ago.days", { n: days });
  return t("ago.ages");
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
  pressable(row);
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
    report(e.message);
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
  if (!redraw(strip, JSON.stringify([dmTabs, chatTab]))) return;
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
  bits.push(t("profile.mmrweek"));
  $("set-sub").textContent = bits.join(" · ");
  // Host capable is a fact, not a boast: the service is answering and it can
  // find Dota on this disk. Both come from the service, neither is a guess.
  $("set-hostable").classList.toggle("hidden", !(s.service && s.dota_path));

  $("btn-password").classList.toggle("hidden", !s.signed_in);
  $("btn-signout").classList.toggle("hidden", !s.signed_in);
  $("set-build").textContent = t("settings.build", { v: s.version || t("status.dash") });

  $("fact-adapter").textContent = s.adapter || t("settings.adapter.none");
  $("fact-relay").textContent = s.relay_ms
    ? t("checks.ms", { n: s.relay_ms })
    : t(s.connected ? "settings.relay.unknown" : "settings.relay.off");
  // "good" is only said where there is a number to call good. A quality word
  // beside a blank is the sort of thing people quote back down the phone.
  const quick = s.relay_ms > 0 && s.relay_ms <= 60;
  $("fact-relayq").textContent = quick ? t("settings.relay.good") : "";
  $("fact-service").textContent = t(s.service ? "status.service.up" : "status.service.down");
  $("fact-servicedot").classList.toggle("up", !!s.service);

  $("fact-dota").textContent = s.dota_path || t("settings.dota.none");
  $("set-gamenote").textContent = t(!s.service ? "settings.game.unknown"
    : s.dota_path ? "settings.game.found" : "settings.game.missing");

  $("fact-version").textContent = t("settings.version", { v: s.version || t("status.dash") });

  // The field is only written from the server's copy when nobody is typing
  // into it. A poll lands every couple of seconds, and a poll that rewrites a
  // half-typed line is how a settings field ends up impossible to edit.
  const opts = $("set-opts");
  if (document.activeElement !== opts) opts.value = s.launch_options || "";

  // The five switches, unless one is in flight. A poll that lands between the
  // click and the answer would flip the box back under the finger that just
  // moved it, and then flip it again a moment later.
  if (!notifySaving) {
    const on = s.notify || {};
    for (const k of NOTIFY_KEYS) $("nt-" + k).checked = !!on[k];
  }
}

// The five things LobbyBaz will interrupt somebody for (D66). The names are
// the field names on the wire and the second half of the checkbox ids, so
// adding a sixth is one string here, one line of markup and one field in
// session.Notify - and nothing to keep in step by hand.
const NOTIFY_KEYS = [
  "room_opens", "friend_online", "tunnel_drops", "room_full", "match_starts",
];
let notifySaving = false;

// ----------------------------------------------------------- diagnostics

function renderDiag() {
  $("btn-diag").disabled = !!state.diag_running;
  $("btn-diag").textContent = t(state.diag_running ? "checks.running" : "checks.run");
  // Three checks take a few seconds and print nothing while they run.
  $("diagbar").classList.toggle("hidden", !state.diag_running);

  const checks = state.diagnostics;
  if (!checks) return;

  const box = $("diaglist");
  if (!redraw(box, JSON.stringify([checks, state.diag_at]))) return;
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
      // Hours and minutes. Seconds and an AM in a card header are three
      // extra things to read and none of them answer the question.
      time: new Date(state.diag_at).toLocaleTimeString(I18n.lang,
        { hour: "2-digit", minute: "2-digit" }),
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
  if (!redraw(box, JSON.stringify(rooms))) return;
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
      if (!pick.value) { report(t("mod.host.nobody")); return; }
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
  if (!redraw(box, JSON.stringify(ads))) return;
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
  if (!redraw(box, JSON.stringify(staff))) return;
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
    report("");
  } catch (e) {
    modRecord = null;
    report(e.message);
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
  drawByThem(r);
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

// What this person has done, as opposed to what was done to them.
//
// The record has always shown the second: every sanction, label and kick
// applied to the account on screen. For staff the first is the one that
// matters - a head admin reviewing an admin needs their actions, not their
// punishments - and the endpoint that answers it (GET /api/admin/log?actor=)
// shipped with T8 and had nothing calling it.
function drawByThem(r) {
  const row = $("mod-byrow");
  const box = $("mod-bythem");
  // Only worth asking for somebody who could have done anything. A player's
  // answer is always empty, and an empty panel is a question.
  const staff = state.role === "head_admin" && (r.role || "") !== "";
  row.classList.toggle("hidden", !staff);
  if (!staff) {
    box.textContent = "";
    return;
  }
  box.textContent = "";
  box.appendChild(el("p", "muted small", t("mod.bythem.loading")));
  const who = r.player_id;
  api("/api/admin/log?actor=" + encodeURIComponent(who)).then((got) => {
    // The moderator may have looked somebody else up while this was in the
    // air. Drawing the answer to a question nobody is asking any more would
    // put one person's actions under another person's name.
    if (!modRecord || modRecord.player_id !== who) return;
    box.textContent = "";
    drawInto(box, got.actions || []);
  }).catch((e) => {
    box.textContent = "";
    box.appendChild(el("p", "muted small", e.message));
  });
}

function drawActions(actions) {
  drawInto($("mod-actions"), actions);
}

function drawInto(box, actions) {
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
  // The search box and the filter chips act on the room list and on nothing
  // else. Leaving them across the top of the room, events and settings
  // screens put controls there that could not do anything, which is a
  // question every player asks once and gets no answer to.
  const lobby = name === "lobby";
  $("search").parentNode.classList.toggle("hidden", !lobby);
  $("filters").classList.toggle("hidden", !lobby);
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
  if (!reason) { report(t("mod.reason.required")); return; }
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
  if (!title && !body) { report(t("mod.banner.empty")); return; }
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
    report(t("pass.done"));
    await refresh();
  } catch (err) {
    $("passerr").textContent = err.message;
  }
};

// The terms moved under somebody who had already agreed to the old ones. The
// banner used to offer accepting them without reading them, which is exactly
// the thing the modal below was built to stop.
$("termsread").onclick = () => openTerms("accept");

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
async function openCreate() {
  if (needName("namegate.why.create")) return;
  // Asked before the form rather than after it, so that nobody fills in a
  // room and is then told what it costs. Leaving happens on submit, not here:
  // somebody who says yes and then closes the dialog has not left anything.
  if (state.room_id && !(await askLeave())) return;
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
      // The coordinator refuses a create from somebody already seated, so the
      // leaving happens here, at the last possible moment (D90).
      if (state.room_id) {
        await api("/api/rooms/leave", {});
        state.room_id = "";
      }
      await api("/api/rooms/create", {
        name: $("roomname").value,
        privacy: pass ? "password" : door,
        password: pass,
        min_mmr: Number($("newmmr").value) || 0,
        game_mode: Number($("newmode").value) || 0,
      });
    } catch (err) {
      // Said once, inside the dialog the person is looking at. Rethrowing
      // would also raise the top strip, behind the overlay, where it would sit
      // unread until the dialog closed and then read as a fresh failure.
      $("createerr").textContent = err.message;
      return;
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

// Saved the moment it changes, with no Save button beside it. The door below
// has one because a password is typed and a half-typed password must not be
// sent; a dropdown has no half-chosen state.
$("mode").onchange = () => act(() => api("/api/rooms/mode", {
  game_mode: Number($("mode").value),
}));

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
      await api("/api/friends/invite", { target_id: f.player_id });
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

// Three doors lead here and they want different things on the way out.
// Somebody signing up needs the checkbox ticked; somebody whose terms moved
// under them needs the acceptance recorded against their account; somebody
// reading them from Settings out of curiosity needs neither. The button along
// the bottom follows, and it is inert until the text has been scrolled to the
// end - consent to a wall nobody read is not consent.
let termsPurpose = "read";

function openTerms(purpose) {
  termsPurpose = purpose;
  $("termsok").classList.toggle("hidden", purpose === "read");
  $("termsver").textContent = state.terms_version
    ? t("terms.version", { v: state.terms_version }) : "";
  const box = $("termstext");
  box.textContent = "";
  box.appendChild(el("p", null, t("auth.termsloading")));
  box.scrollTop = 0;
  $("termsgate").classList.remove("hidden");
  termsRead();
  api("/api/terms")
    .then((got) => drawTerms(got.text || ""))
    .catch((err) => {
      box.textContent = "";
      box.appendChild(el("p", null, err.message));
      termsRead();
    });
}

function shutTerms() { $("termsgate").classList.add("hidden"); }

// How far down the reader has got, as a percentage and as a verdict. The two
// per cent of slack absorbs sub-pixel rounding, which otherwise leaves a
// document that has plainly been read to the end sitting at 99.
function termsRead() {
  const box = $("termstext");
  const room = box.scrollHeight - box.clientHeight;
  const pct = room <= 1 ? 100 : Math.min(100, Math.round((box.scrollTop / room) * 100));
  const done = pct >= 98;
  $("termsfill").style.width = pct + "%";
  $("termspct").textContent = t("terms.pct", { n: pct });
  $("termsstate").textContent = t(done ? "terms.readend" : "terms.scroll");
  $("termsstate").classList.toggle("done", done);
  $("termsok").disabled = !done;
}

// The terms arrive as markdown, because a markdown file is what the owner
// edits. This turns the handful of shapes that file actually uses into
// elements, and it builds nodes rather than markup: the text is a document
// somebody types into, and typing into it must never reach the page.
function drawTerms(text) {
  const box = $("termstext");
  box.textContent = "";
  for (const block of String(text).split(/\n\s*\n/)) {
    const lines = block.split("\n").map((l) => l.trim()).filter(Boolean);
    if (!lines.length) continue;
    const head = lines[0];
    // The title and the version line are both in the header two inches
    // above, and a document that introduces itself twice reads as a draft.
    if (head.startsWith("# ")) continue;
    if (/^\*\*Version .*\*\*$/.test(head) && lines.length === 1) continue;
    if (head.startsWith("#")) {
      box.appendChild(inline(el("h3"), head.replace(/^#+\s*/, "")));
      lines.shift();
      if (!lines.length) continue;
    }
    if (lines[0].startsWith("> ")) {
      const quote = el("blockquote");
      quote.appendChild(inline(el("p"), join(lines, /^>\s?/)));
      box.appendChild(quote);
      continue;
    }
    if (lines[0].startsWith("- ")) {
      const list = el("ul");
      for (const line of lines) {
        if (line.startsWith("- ")) list.appendChild(inline(el("li"), line.slice(2)));
        // A wrapped bullet is a continuation of the one above it, not a new
        // one. Markdown says so by indenting; we have already trimmed.
        else if (list.lastChild) inline(list.lastChild, " " + line);
      }
      box.appendChild(list);
      continue;
    }
    box.appendChild(inline(el("p"), join(lines, null)));
  }
  termsRead();
}

// A paragraph wrapped at 78 characters is one paragraph, not nine.
function join(lines, strip) {
  return lines.map((l) => (strip ? l.replace(strip, "") : l)).join(" ");
}

// Bold is the only inline mark the terms use, and so the only one honoured.
function inline(node, text) {
  String(text).split("**").forEach((part, i) => {
    if (!part) return;
    node.appendChild(i % 2 ? el("strong", null, part) : document.createTextNode(part));
  });
  return node;
}

$("termstext").onscroll = termsRead;
$("termsclose").onclick = shutTerms;
$("termsnot").onclick = shutTerms;
$("termsok").onclick = () => {
  if (termsPurpose === "signup") {
    $("a-terms").checked = true;
    shutTerms();
    return;
  }
  act(async () => {
    await api("/api/auth/terms", { version: state.terms_version });
    shutTerms();
    report(t("terms.accepted"));
  });
};

// The sign-up checkbox's own link. It sits inside the label, so the click has
// to be stopped or opening the terms also ticks the box it is gating.
$("readterms").onclick = (e) => {
  e.preventDefault();
  openTerms("signup");
};

// --- leaving, and the network --------------------------------------------

$("btn-leave").onclick = () => act(async () => {
  await api("/api/rooms/leave", {});
  show("lobby");
});
$("btn-tolobby").onclick = () => show("lobby");

// The guide, from the (i) and from its own Hide. Drawn once at start-up
// because it is remembered rather than derived from anything on the server.
$("btn-guide").onclick = () => setGuide(!guideOpen());
$("btn-guidehide").onclick = () => setGuide(false);
drawGuide();

$("btn-lock").onclick = () => act(() => api("/api/rooms/status", { status: "locked_in_game" }));
$("btn-reopen").onclick = () => act(() => api("/api/rooms/status", { status: "open_to_new_players" }));
$("btn-open").onclick = () => act(() => api("/api/rooms/status", { status: "open" }));

// The team is not asked for any more: it is which seat the player is sitting
// in. Slots 1-5 are Radiant and 6-10 are Dire, which is what the room screen
// already shows, so a dropdown that could disagree with the seat was one
// place too many for the same fact to live (D57).
//
// myTeam reads the side out of the seat, and the gallery is a side of its own
// (D81): somebody in a watching seat goes in as "spec", which is the team
// Dota keeps for people who are not playing.
//
// The host included. Their machine runs the listen server whether or not they
// are playing on it, so a host who has sat down to watch still starts the
// match - and starting it as a spectator is what leaves all ten playing slots
// for the ten people who came to play, instead of nine.
//
// Anybody the room has not told us about yet is Radiant: the game requires a
// side and that is the one it defaults to.
function myTeam() {
  const me = ((state.room && state.room.members) || [])
    .find((m) => m.player_id === state.player_id);
  if (!me) return "good";
  if (me.spectator) return "spec";
  return me.slot >= 5 ? "bad" : "good";
}

// --- settings -------------------------------------------------------------

$("btn-diag").onclick = () => act(() => api("/api/diagnose", {}));
$("btn-editprofile").onclick = () => $("mebtn").onclick();
$("btn-password").onclick = () => $("p-password").onclick();
$("btn-signout").onclick = () => $("p-signout").onclick();
// From Settings this is usually curiosity, and there is nothing to accept.
// It is the third door onto the same text when the terms have moved.
$("btn-showterms").onclick = () =>
  openTerms(state.signed_in && state.terms_accepted === false ? "accept" : "read");

// Launch options. The refusal is the useful half: the app parses the text by
// the same rules the service does, so a typo is answered here, beside the
// field, and not four clicks later when the match will not start.
$("btn-saveopts").onclick = () => act(async () => {
  await api("/api/launchoptions", { options: $("set-opts").value });
  $("set-optsnote").textContent = t("settings.opts.saved");
  $("set-optsnote").classList.add("done");
});
$("set-opts").oninput = () => {
  $("set-optsnote").textContent = t("settings.opts.note");
  $("set-optsnote").classList.remove("done");
};

// Notifications. The whole set is sent on every change rather than the one
// switch that moved: there is then no way for two saves crossing to leave
// half of somebody's choice stored.
for (const k of NOTIFY_KEYS) {
  $("nt-" + k).onchange = () => act(async () => {
    const body = {};
    for (const j of NOTIFY_KEYS) body[j] = $("nt-" + j).checked;
    notifySaving = true;
    try {
      await api("/api/notifications", body);
    } finally {
      notifySaving = false;
    }
  });
}

// ----------------------------------------------------------------- poll

// uiStamp is only ever set by an app started with -dev-ui. It changes when a
// file of the interface changes on disk, and the window reloads itself - the
// whole point of scripts/live.sh. The first value is remembered rather than
// acted on, or every start-up would reload once for nothing.
let uiStamp = null;

// askGate puts one question in front of somebody and answers with true or
// false. It exists because the two other ways of asking are both wrong here:
// window.confirm is a different typeface and, inside a desktop shell, a
// different window; and a bespoke dialog per question is how an interface
// ends up with four of them that behave differently.
//
// It resolves false if the card is dismissed any of the ways a card can be -
// Escape, the backdrop, Cancel - because all three of those mean no.
let askSettle = null;

function askGate(titleKey, whyKey, goKey, noKey) {
  $("asktitle").textContent = t(titleKey);
  $("askwhy").textContent = t(whyKey);
  $("askyes").textContent = t(goKey);
  $("askno").textContent = t(noKey || "profile.cancel");
  $("askgate").classList.remove("hidden");
  $("askyes").focus();
  return new Promise((resolve) => { askSettle = resolve; });
}

// Closing is not conditional on there being a question outstanding. A card
// that is on the screen has to go away when its button is pressed, whatever
// put it there - which is also the only reason the Escape check can reach it.
function askClose(answer) {
  $("askgate").classList.add("hidden");
  const settle = askSettle;
  askSettle = null;
  if (settle) settle(answer);
}

$("askyes").onclick = () => askClose(true);
$("askno").onclick = () => askClose(false);

// --- the friends rail, folded away ---------------------------------------

// The rail is the widest thing on the screen that is not the room list, and
// on a small window it is the difference between reading a room and squinting
// at one. It folds (D89). The choice is remembered, because a preference that
// has to be set again every time the app starts is not a preference.
//
// Below 1100px the stylesheet takes the decision away: there is no room, the
// column is nought whatever this says, and both arrows are hidden. The class
// is still kept honest so that widening the window puts back what the person
// last chose rather than a default.
// Not a dotted name: anything shaped like one in this file is taken for an
// i18n key by the catalogue tests, and a browser storage key is not one.
const RAIL_KEY = "lobbybaz-rail-shut";

function setRail(shut) {
  $("shell").classList.toggle("rail-shut", shut);
  try { localStorage.setItem(RAIL_KEY, shut ? "1" : "0"); } catch (e) { /* private mode */ }
}

$("railhide").onclick = () => setRail(true);
$("railshow").onclick = () => setRail(false);

try {
  if (localStorage.getItem(RAIL_KEY) === "1") $("shell").classList.add("rail-shut");
} catch (e) { /* private mode */ }

// --- getting out of a dialog ---------------------------------------------

// Every dialog in here had exactly one way out: find its own button and click
// it. On a desktop application Escape is the way out, and clicking the dark
// area around a card is the other one, and a card that ignores both reads as
// stuck rather than as modal.
//
// Each gate names the control Escape should press rather than being hidden
// directly, so a dialog that has cleaning up to do still does it - the terms
// remember how far they were read, the profile form puts its fields back.
// Two are deliberately absent: the name gate, because there is no application
// behind it to go back to, and no gate is dismissed while a request it
// started is still running.
const DISMISS = [
  ["askgate", "askno"],
  ["passgate", "pw-cancel"],
  ["profilegate", "p-cancel"],
  ["invitegate", "inviteclose"],
  ["roomsetgate", "roomsetclose"],
  ["creategate", "createcancel"],
  ["termsgate", "termsclose"],
];

function topGate() {
  for (const [gate, close] of DISMISS) {
    if (!$(gate).classList.contains("hidden")) return [gate, close];
  }
  return null;
}

document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape" || e.defaultPrevented) return;
  // The chat's friend menu hangs off the tab strip rather than being a gate,
  // and it is in front of everything: it goes first.
  const menu = $("chatmenu");
  if (!menu.classList.contains("hidden")) {
    menu.classList.add("hidden");
    return;
  }
  const top = topGate();
  if (!top) return;
  e.preventDefault();
  $(top[1]).click();
});

// The backdrop only. A click that started inside the card and ended outside it
// - a drag off the end of a text selection - is not somebody asking to leave.
for (const [gate, close] of DISMISS) {
  $(gate).addEventListener("mousedown", (e) => {
    if (e.target !== $(gate)) return;
    const up = (ev) => {
      document.removeEventListener("mouseup", up);
      if (ev.target === $(gate)) $(close).click();
    };
    document.addEventListener("mouseup", up);
  });
}

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
    report(t("err.dead", { error: e.message }));
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
