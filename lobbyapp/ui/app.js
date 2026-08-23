// LobbyBaz - prototype UI.
//
// The page is a renderer. It polls one endpoint, draws what came back, and
// posts actions. It holds no state of its own beyond which screen is showing
// and what the player is halfway through typing, so a refresh never loses
// anything and there is no second copy of the truth to drift.

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
  pill("p-service", s.service, s.service ? "service running" : "service down");
  const tun = s.connected ? "ok" : (s.tunnel === "connecting" ? "wait" : null);
  pill("p-tunnel", tun, s.connected ? "tunnel connected"
    : s.tunnel === "connecting" ? "connecting" : "tunnel off");
  $("p-online").textContent = (s.online ?? 0) + " online";

  $("menick").textContent = s.nick || "…";
  $("memmr").textContent = s.mmr ? s.mmr + " MMR" : "no MMR set";

  // First run asks for a name and nothing else.
  $("namegate").classList.toggle("hidden", !!s.named);

  // Problems worth interrupting for.
  banner(s.service_error || s.coordinator_error || s.tunnel_error ||
    (s.room_gone ? "That room has closed." : "") ||
    (s.removed ? "You are no longer in that room." : "") ||
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
  if (u.error) {
    el.innerHTML = `<span>An update (${esc(u.version)}) could not be downloaded: ${esc(u.error)}</span>`;
  } else if (u.ready) {
    el.innerHTML = `<span>Version ${esc(u.version)} is ready. Installing takes a few seconds and reopens the app.</span>`;
    const b = document.createElement("button");
    b.textContent = "Install now";
    b.onclick = () => act(() => api("/api/update", {}));
    el.appendChild(b);
  } else {
    el.innerHTML = `<span>Downloading version ${esc(u.version)}…</span>`;
  }
}

// ------------------------------------------------------------- room list

function renderRooms(rooms) {
  const box = $("roomlist");
  if (!rooms.length) {
    box.innerHTML = `<p class="muted pad">No rooms yet. Create one and the other player will see it.</p>`;
    return;
  }
  box.innerHTML = "";
  for (const r of rooms) box.appendChild(roomCard(r));
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
      <div class="room-meta">
        ${r.seats}/10 players &middot; host ${esc(r.host_nick)}${
          r.avg_mmr ? " &middot; average " + r.avg_mmr + " MMR" : ""}
      </div>
      <div class="room-players">${players}</div>
    </div>
    <div class="room-actions"></div>`;

  const actions = el.querySelector(".room-actions");
  const mine = r.id === state.room_id;

  if (mine) {
    const open = document.createElement("button");
    open.textContent = "Open";
    open.className = "primary";
    open.onclick = () => show("room");
    actions.appendChild(open);
  } else {
    const join = document.createElement("button");
    join.textContent = "Join";
    join.className = "primary";
    join.disabled = !r.joinable || !!state.room_id;
    join.title = state.room_id ? "Leave your current room first"
      : r.joinable ? "" : "This room is not accepting players";
    join.onclick = () => act(() => api("/api/rooms/join", { room_id: r.id }));
    actions.appendChild(join);

    const spec = document.createElement("button");
    spec.textContent = "Spectate";
    spec.className = "ghost";
    spec.disabled = !!state.room_id;
    spec.title = "Admin seat, outside the ten playing slots";
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
  if (status === "locked_in_game") return "In game";
  if (status === "open_to_new_players") return "Needs a player";
  if (status === "closed") return "Closed";
  return "Open";
}

function statusBadge(status) {
  return `<span class="${statusClass(status)}">${statusLabel(status)}</span>`;
}

// ------------------------------------------------------------------ room

function renderRoom(r) {
  $("room-name").textContent = r.name;
  $("room-sub").textContent =
    `${r.seats}/10 players · host ${r.host_nick}` +
    (r.avg_mmr ? ` · average ${r.avg_mmr} MMR` : "");
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
  sbox.innerHTML = specs.length ? "" :
    `<p class="muted small">Nobody is spectating.</p>`;
  for (const m of specs) sbox.appendChild(slotCard(m.slot, m, false, true));

  // Network facts, shown plainly. During testing these are the numbers
  // somebody will be asked about.
  const bits = [];
  if (state.virtual_ip) bits.push("you " + state.virtual_ip);
  if (state.host_ip) bits.push("host " + state.host_ip);
  if (state.adapter) bits.push(state.adapter);
  $("netinfo").textContent = bits.join("  ·  ");

  $("btn-connect").disabled = !!state.connected;
  $("btn-disconnect").disabled = !state.connected;
  $("btn-play").disabled = !state.connected;
  $("btn-play").title = state.connected ? "" : "Connect first";

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
  if (state.connected) {
    el.hidden = false;
    el.className = "netbanner ok";
    el.textContent = "You are on the room's network" +
      (state.virtual_ip ? " as " + state.virtual_ip : "") + ".";
    return;
  }
  if (state.connect_error) {
    el.hidden = false;
    el.className = "netbanner bad";
    el.textContent = "Could not get onto the room's network: " +
      state.connect_error + " Press Connect to try again.";
    return;
  }
  if (state.tunnel === "connecting") {
    el.hidden = false;
    el.className = "netbanner wait";
    el.textContent = "Getting you onto the room's network...";
    return;
  }
  el.hidden = false;
  el.className = "netbanner bad";
  el.textContent = "You are not on the room's network yet, so nobody can " +
    "reach you and you cannot reach the host. Press Connect.";
}

function slotCard(index, member, canKick, spectator) {
  const el = document.createElement("div");
  const mine = member && member.player_id === state.player_id;
  el.className = "slot" + (member ? "" : " empty") + (mine ? " you" : "");

  const label = spectator ? "S" + (index + 1) : index + 1;
  el.innerHTML = `<div class="slot-num">${label}</div><div class="slot-body"></div>`;
  const body = el.querySelector(".slot-body");

  if (!member) {
    body.innerHTML = `<div class="slot-name muted">Empty</div>`;
    return el;
  }
  body.innerHTML = `
    <div class="slot-name">${esc(member.nick)}${mine ? " (you)" : ""}</div>
    <div class="slot-sub">${member.is_host ? "Host · " : ""}${
      member.mmr ? member.mmr + " MMR" : "no MMR set"}</div>`;

  if (canKick && !member.is_host && !mine) {
    const b = document.createElement("button");
    b.textContent = "Kick";
    b.title = "Removes them and bars them from this room for 5 minutes";
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
  $("btn-diag").textContent = state.diag_running ? "Running…" : "Run checks";

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
      ${c.ms ? `<div class="ms">${c.ms} ms</div>` : ""}
    </div>`).join("");

  if (state.diag_at) {
    const when = new Date(state.diag_at);
    $("diagwhen").textContent = "Last run " + when.toLocaleTimeString() +
      ", and sent to the server.";
  }
}

// --------------------------------------------------------------- screens

function show(name) {
  screen = name;
  for (const el of document.querySelectorAll(".screen")) el.classList.add("hidden");
  $("screen-" + name).classList.remove("hidden");
  for (const t of document.querySelectorAll(".tab")) {
    t.classList.toggle("active", t.dataset.screen === name);
  }
}

document.querySelectorAll(".tab").forEach((t) => {
  t.onclick = () => show(t.dataset.screen);
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
    ? "changeable again " + new Date(state.mmr_locked_until).toLocaleDateString()
    : "changeable once a week";
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
    banner("The app stopped responding: " + e.message);
  }
}

refresh();
setInterval(refresh, POLL_MS);
