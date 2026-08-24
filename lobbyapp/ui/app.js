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

// ----------------------------------------------------------------- render

function render() {
  const s = state;

  pill("p-service", s.service, t(s.service ? "status.service.up" : "status.service.down"));
  $("p-online").textContent = t("status.online", { n: s.online ?? 0 });
  $("menick").textContent = s.nick || t("status.dash");
  $("memmr").textContent = s.mmr ? t("status.mmr", { n: s.mmr }) : t("status.nomm");

  renderConnection(s);

  // First run asks for a name and nothing else.
  $("namegate").classList.toggle("hidden", !!s.named);

  banner(s.service_error || s.coordinator_error || s.tunnel_error ||
    (s.room_gone ? t("err.room_gone") : "") ||
    (s.removed ? t("err.removed") : "") ||
    (s.build_warning || ""));

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
    box.appendChild(el("p", "muted pad",
      t(rooms.length ? "lobby.nomatch" : "lobby.empty")));
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
  if (!ms) {
    const cell = el("span", "room-ping unknown", t("lobby.ping.unknown"));
    cell.title = t("lobby.ping.none");
    return cell;
  }
  const grade = ms < 60 ? "good" : ms < 140 ? "fair" : "poor";
  const cell = el("span", "room-ping " + grade, t("lobby.ping.value", { n: ms }));
  cell.title = t("lobby.ping.explain");
  return cell;
}

function roomCard(r) {
  const card = el("div", "room");

  // Column 1: name, door, status, the host's sentence, and who is in it.
  const about = el("div");
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
    who.appendChild(el("span", "tag" + (m.is_host ? " host" : ""),
      m.mmr ? t("lobby.player.rated", { name: m.nick, mmr: m.mmr }) : m.nick));
  }
  about.appendChild(who);
  card.appendChild(about);

  // Columns 2-4: the numbers a player chooses a room on.
  const count = el("span", "room-count");
  count.appendChild(el("span", "", String(r.seats)));
  count.appendChild(el("small", "", t("lobby.of10")));
  card.appendChild(count);

  card.appendChild(el("span", "room-mmr muted small",
    r.min_mmr ? t("lobby.mmr.floor", { n: r.min_mmr })
      : r.avg_mmr ? t("lobby.mmr.average", { n: r.avg_mmr })
        : t("lobby.mmr.any")));

  card.appendChild(pingCell(r));
  card.appendChild(roomActions(r));
  return card;
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

  const spec = el("button", "ghost tiny", t("room.spectate"));
  spec.disabled = !!state.room_id;
  spec.title = t("room.spectate.note");
  spec.onclick = () => act(() => api("/api/rooms/spectate", { room_id: r.id }));
  acts.appendChild(spec);
  return acts;
}

// joinRoom asks for the password only when the room actually has one, so an
// open room is one click and a locked one is honest about why it is asking.
function joinRoom(r) {
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

  const seated = {};
  for (const m of r.members || []) if (!m.spectator) seated[m.slot] = m;

  const box = $("slots");
  box.textContent = "";
  for (let i = 0; i < 10; i++) box.appendChild(slotCard(i, seated[i], iAmHost));

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

function slotCard(index, member, canKick, spectator) {
  const card = el("div");
  const mine = member && member.player_id === state.player_id;
  card.className = "slot" + (member ? "" : " empty") + (mine ? " you" : "");
  card.appendChild(el("div", "slot-num", spectator ? "S" + (index + 1) : String(index + 1)));

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
    box.appendChild(el("p", "muted small friend-empty",
      why ? t("friends.unavailable") : t("friends.loading")));
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
  const dot = el("span", "dot" + (f.online ? (f.in_game ? " wait" : " ok") : ""));
  row.appendChild(dot);

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

$("nameform").onsubmit = async (e) => {
  e.preventDefault();
  const mmr = $("mmrinput").value;
  try {
    await api("/api/profile", {
      nick: $("nameinput").value,
      mmr: mmr === "" ? null : Number(mmr),
    });
    $("nameerr").textContent = "";
    await refresh();
  } catch (err) {
    $("nameerr").textContent = err.message;
  }
};

$("mebtn").onclick = () => {
  $("p-nick").value = state.nick || "";
  $("p-mmr").value = state.mmr || "";
  $("mmrnote").textContent = state.mmr_locked_until
    ? t("profile.mmr.locked", {
        date: new Date(state.mmr_locked_until).toLocaleDateString(I18n.lang),
      })
    : t("profile.mmr.free");
  $("profileerr").textContent = "";
  $("profilegate").classList.remove("hidden");
};
$("p-cancel").onclick = () => $("profilegate").classList.add("hidden");

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
  const name = $("roomname").value;
  act(async () => {
    await api("/api/rooms/create", { name });
    $("roomname").value = "";
    show("room");
  });
};

$("describeform").onsubmit = (e) => {
  e.preventDefault();
  act(() => api("/api/rooms/describe", { description: $("describe").value }));
};

$("chatform").onsubmit = (e) => {
  e.preventDefault();
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
