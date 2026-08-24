// LobbyBaz - prototype UI.
//
// The page is a renderer. It polls one endpoint, draws what came back, and
// posts actions. It holds no state of its own beyond which screen is showing
// and what the player is halfway through typing, so a refresh never loses
// anything and there is no second copy of the truth to drift.
//
// No user-facing text is written in this file. Every string is t("some.key"),
// resolved from strings/<lang>.json by i18n.js, and lobbyapp/ui_test.go fails
// the build if a key is missing or a quoted sentence appears here (D44).

const TOKEN = new URLSearchParams(location.search).get("t") || "";
const POLL_MS = 2000;

let state = {};
let screen = "lobby";
let busy = false;

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

// act runs an action, shows any error in the banner, and refreshes at once
// rather than waiting for the next poll - a button that appears to do
// nothing for two seconds gets pressed again.
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

// ----------------------------------------------------------------- render

function render() {
  const s = state;

  // Header pills.
  pill("p-service", s.service, t(s.service ? "status.service.up" : "status.service.down"));
  const tun = s.connected ? "ok" : (s.tunnel === "connecting" ? "wait" : null);
  pill("p-tunnel", tun, t(s.connected ? "status.tunnel.on"
    : s.tunnel === "connecting" ? "status.tunnel.connecting" : "status.tunnel.off"));
  $("p-online").textContent = t("status.online", { n: s.online ?? 0 });

  $("menick").textContent = s.nick || t("status.dash");
  $("memmr").textContent = s.mmr ? t("status.mmr", { n: s.mmr }) : t("status.nomm");

  // First run asks for a name and nothing else.
  $("namegate").classList.toggle("hidden", !!s.named);

  // Problems worth interrupting for.
  banner(s.service_error || s.coordinator_error || s.tunnel_error ||
    (s.room_gone ? t("err.room_gone") : "") ||
    (s.removed ? t("err.removed") : "") ||
    (s.build_warning || ""));

  renderUpdate(s.update);

  const inRoom = !!s.room_id && !!s.room;
  $("roomtab").disabled = !inRoom;
  if (!inRoom && screen === "room") show("lobby");

  renderRooms(s.rooms || []);
  renderChat("lobbylog", s.lobby_chat || []);

  if (inRoom) {
    renderRoom(s.room);
    renderChat("roomlog", s.room_chat || []);
  }
  renderDiag();
}

function pill(id, ok, text) {
  const el = $(id);
  el.textContent = text;
  el.className = "pill " + (ok === "wait" ? "wait" : ok ? "ok" : "bad");
}

function renderUpdate(u) {
  const el = $("update");
  if (!u) { el.classList.add("hidden"); return; }
  el.classList.remove("hidden");
  el.textContent = "";
  const line = document.createElement("span");
  if (u.error) {
    line.textContent = t("update.failed", { version: u.version, error: u.error });
    el.appendChild(line);
  } else if (u.ready) {
    line.textContent = t("update.ready", { version: u.version });
    el.appendChild(line);
    const b = document.createElement("button");
    b.textContent = t("update.install");
    b.onclick = () => act(() => api("/api/update", {}));
    el.appendChild(b);
  } else {
    line.textContent = t("update.downloading", { version: u.version });
    el.appendChild(line);
  }
}

// ------------------------------------------------------------- room list

function renderRooms(rooms) {
  const box = $("roomlist");
  box.innerHTML = "";
  if (!rooms.length) {
    const p = document.createElement("p");
    p.className = "muted pad";
    p.textContent = t("lobby.empty");
    box.appendChild(p);
    return;
  }
  for (const r of rooms) box.appendChild(roomCard(r));
}

// roomMeta is the one line under a room's name: how full it is, whose PC is
// hosting, and the average MMR when anybody has declared one.
function roomMeta(r) {
  return r.avg_mmr
    ? t("room.meta.mmr", { seats: r.seats, host: r.host_nick, mmr: r.avg_mmr })
    : t("room.meta", { seats: r.seats, host: r.host_nick });
}

function roomCard(r) {
  const el = document.createElement("div");
  el.className = "room";

  const players = (r.members || [])
    .filter((m) => !m.spectator)
    .map((m) => `<span class="tag ${m.is_host ? "host" : ""}">${esc(m.nick)}${
      m.mmr ? " &middot; " + m.mmr : ""}</span>`)
    .join("");

  el.innerHTML = `
    <div>
      <div class="room-title">
        <strong>${esc(r.name)}</strong>
        ${statusBadge(r.status)}
      </div>
      <div class="room-meta">${esc(roomMeta(r))}</div>
      <div class="room-players">${players}</div>
    </div>
    <div class="room-actions"></div>`;

  const actions = el.querySelector(".room-actions");
  const mine = r.id === state.room_id;

  if (mine) {
    const open = document.createElement("button");
    open.textContent = t("room.open");
    open.className = "primary";
    open.onclick = () => show("room");
    actions.appendChild(open);
  } else {
    const join = document.createElement("button");
    join.textContent = t("room.join");
    join.className = "primary";
    join.disabled = !r.joinable || !!state.room_id;
    join.title = state.room_id ? t("room.join.busy")
      : r.joinable ? "" : t("room.join.closed");
    join.onclick = () => act(() => api("/api/rooms/join", { room_id: r.id }));
    actions.appendChild(join);

    const spec = document.createElement("button");
    spec.textContent = t("room.spectate");
    spec.className = "ghost";
    spec.disabled = !!state.room_id;
    spec.title = t("room.spectate.note");
    spec.onclick = () => act(() => api("/api/rooms/spectate", { room_id: r.id }));
    actions.appendChild(spec);
  }
  return el;
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
  return `<span class="${statusClass(status)}">${esc(statusLabel(status))}</span>`;
}

// ------------------------------------------------------------------ room

function renderRoom(r) {
  $("room-name").textContent = r.name;
  $("room-sub").textContent = roomMeta(r);
  const badge = $("room-status");
  badge.className = statusClass(r.status);
  badge.textContent = statusLabel(r.status);

  const iAmHost = !!state.is_host;
  $("hostcontrols").classList.toggle("hidden", !iAmHost);

  // Ten playing slots, always all ten, so an empty seat is visible as a
  // space somebody could take rather than as an absence.
  const seated = {};
  for (const m of r.members || []) if (!m.spectator) seated[m.slot] = m;

  const box = $("slots");
  box.innerHTML = "";
  for (let i = 0; i < 10; i++) box.appendChild(slotCard(i, seated[i], iAmHost));

  const specs = (r.members || []).filter((m) => m.spectator);
  const sbox = $("spectators");
  sbox.innerHTML = "";
  if (!specs.length) {
    const p = document.createElement("p");
    p.className = "muted small";
    p.textContent = t("room.spectators.none");
    sbox.appendChild(p);
  }
  for (const m of specs) sbox.appendChild(slotCard(m.slot, m, false, true));

  // Network facts, shown plainly. During testing these are the numbers
  // somebody will be asked about.
  const bits = [];
  if (state.virtual_ip) bits.push(t("net.you", { ip: state.virtual_ip }));
  if (state.host_ip) bits.push(t("net.host", { ip: state.host_ip }));
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
  const el = $("netbanner");
  el.hidden = false;
  if (state.connected) {
    el.className = "netbanner ok";
    el.textContent = state.virtual_ip
      ? t("net.on", { ip: state.virtual_ip })
      : t("net.on.noip");
    return;
  }
  if (state.connect_error) {
    el.className = "netbanner bad";
    el.textContent = t("net.failed", { error: state.connect_error });
    return;
  }
  if (state.tunnel === "connecting") {
    el.className = "netbanner wait";
    el.textContent = t("net.connecting");
    return;
  }
  el.className = "netbanner bad";
  el.textContent = t("net.off");
}

function slotCard(index, member, canKick, spectator) {
  const el = document.createElement("div");
  const mine = member && member.player_id === state.player_id;
  el.className = "slot" + (member ? "" : " empty") + (mine ? " you" : "");

  const label = spectator ? "S" + (index + 1) : index + 1;
  el.innerHTML = `<div class="slot-num">${label}</div><div class="slot-body"></div>`;
  const body = el.querySelector(".slot-body");

  if (!member) {
    body.innerHTML = `<div class="slot-name muted">${esc(t("room.slot.empty"))}</div>`;
    return el;
  }

  const name = mine ? t("room.slot.you", { name: member.nick }) : member.nick;
  const mmr = member.mmr ? t("status.mmr", { n: member.mmr }) : t("status.nomm");
  const sub = member.is_host ? t("room.slot.host", { sub: mmr }) : mmr;
  body.innerHTML = `
    <div class="slot-name">${esc(name)}</div>
    <div class="slot-sub">${esc(sub)}</div>`;

  if (canKick && !member.is_host && !mine) {
    const b = document.createElement("button");
    b.textContent = t("room.kick");
    b.title = t("room.kick.note");
    b.onclick = () => act(() => api("/api/rooms/kick", { target: member.player_id }));
    el.appendChild(b);
  }
  return el;
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
  log.innerHTML = msgs.map((m) => {
    if (m.system) return `<div class="msg system">${esc(m.text)}</div>`;
    const self = m.player_id === state.player_id;
    return `<div class="msg"><span class="who${self ? " self" : ""}">${
      esc(m.nick)}:</span> ${esc(m.text)}</div>`;
  }).join("");
  if (atBottom) log.scrollTop = log.scrollHeight;
}

// ----------------------------------------------------------- diagnostics

function renderDiag() {
  $("btn-diag").disabled = !!state.diag_running;
  $("btn-diag").textContent = t(state.diag_running ? "checks.running" : "checks.run");

  const checks = state.diagnostics;
  if (!checks) return;

  const box = $("diaglist");
  box.innerHTML = checks.map((c) => `
    <div class="check ${c.ok ? "ok" : "bad"}">
      <div class="mark">${c.ok ? "✓" : "✗"}</div>
      <div class="body">
        <div class="name">${esc(c.name)}</div>
        ${c.detail ? `<div class="detail">${esc(c.detail)}</div>` : ""}
      </div>
      ${c.ms ? `<div class="ms">${esc(t("checks.ms", { n: c.ms }))}</div>` : ""}
    </div>`).join("");

  if (state.diag_at) {
    const when = new Date(state.diag_at);
    $("diagwhen").textContent = t("checks.when", {
      time: when.toLocaleTimeString(I18n.lang),
    });
  }
}

// --------------------------------------------------------------- screens

function show(name) {
  screen = name;
  for (const el of document.querySelectorAll(".screen")) el.classList.add("hidden");
  $("screen-" + name).classList.remove("hidden");
  for (const tab of document.querySelectorAll(".tab")) {
    tab.classList.toggle("active", tab.dataset.screen === name);
  }
}

document.querySelectorAll(".tab").forEach((tab) => {
  tab.onclick = () => show(tab.dataset.screen);
});

// ---------------------------------------------------------------- events

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

function wireChat(formID, inputID, channel) {
  $(formID).onsubmit = (e) => {
    e.preventDefault();
    const text = $(inputID).value.trim();
    if (!text) return;
    $(inputID).value = "";
    act(() => api("/api/chat", { channel, text }));
  };
}
wireChat("lobbychatform", "lobbychatinput", "lobby");
wireChat("roomchatform", "roomchatinput", "room");

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
  refresh();
  setInterval(refresh, POLL_MS);
});
