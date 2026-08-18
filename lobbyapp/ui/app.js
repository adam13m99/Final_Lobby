'use strict';

// The session token arrives in the URL. Keep it in memory and strip it from
// the address bar so it does not linger in browser history.
const TOKEN = new URLSearchParams(location.search).get('t') || '';
history.replaceState(null, '', location.pathname);

const $ = (id) => document.getElementById(id);

let state = {};
let busy = false;

async function api(path, body) {
  const res = await fetch(path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Lobby-Token': TOKEN },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || ('request failed: ' + res.status));
  return data;
}

let toastTimer;
function toast(msg, kind) {
  const t = $('toast');
  t.textContent = msg;
  t.className = 'toast ' + (kind || '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add('hidden'), kind === 'err' ? 8000 : 4000);
}

// act wraps a button action: it disables the UI, reports failures in plain
// language, and refreshes when done. Every button goes through it, so no
// action can leave the page in a stale state.
async function act(fn, okMsg) {
  if (busy) return;
  busy = true;
  document.body.style.cursor = 'progress';
  try {
    await fn();
    if (okMsg) toast(okMsg, 'ok');
    await refresh();
  } catch (e) {
    toast(e.message, 'err');
  } finally {
    busy = false;
    document.body.style.cursor = '';
  }
}

// --- rendering ----------------------------------------------------------

function renderPills() {
  const svc = $('pill-service');
  if (state.service) {
    svc.textContent = 'service running';
    svc.className = 'pill ok';
  } else {
    svc.textContent = 'service not running';
    svc.className = 'pill bad';
  }

  const tun = $('pill-tunnel');
  const s = state.tunnel || 'idle';
  tun.textContent = 'tunnel ' + s;
  tun.className = 'pill ' + (state.connected ? 'ok' : (s === 'connecting' ? 'warn' : ''));

  $('who').textContent = state.player ? (state.player + (state.nick && state.nick !== state.player ? ' (' + state.nick + ')' : '')) : '';
}

function show(view) {
  for (const v of ['view-setup', 'view-lobby', 'view-room']) {
    $(v).classList.toggle('hidden', v !== view);
  }
}

function renderRooms() {
  const box = $('rooms');
  const rooms = state.rooms || [];
  if (!rooms.length) {
    box.innerHTML = '<div class="empty">No rooms yet. Create one and tell your friend its name.</div>';
    return;
  }
  box.innerHTML = '';
  for (const r of rooms) {
    const el = document.createElement('div');
    el.className = 'room';

    const left = document.createElement('div');
    const name = document.createElement('div');
    name.className = 'name';
    name.textContent = r.name || r.id;
    const meta = document.createElement('div');
    meta.className = 'meta';
    const players = (r.players || []).join(', ') || 'empty';
    meta.textContent = players + ' · ' + r.free_slots + ' free · host ' + r.host_id;
    left.append(name, meta);

    const right = document.createElement('div');
    right.style.display = 'flex';
    right.style.gap = '8px';
    right.style.alignItems = 'center';

    const badge = document.createElement('span');
    badge.className = 'badge ' + (r.status === 'open' ? 'open' : 'locked');
    badge.textContent = r.status.replace(/_/g, ' ');
    right.append(badge);

    const joinable = r.status === 'open' || r.status === 'open_to_new_players';
    if (joinable && r.free_slots > 0) {
      const btn = document.createElement('button');
      btn.className = 'primary';
      btn.textContent = 'Join';
      btn.onclick = () => act(() => api('/api/rooms/join', { room_id: r.id }), 'Joined ' + (r.name || r.id));
      right.append(btn);
    }

    el.append(left, right);
    box.append(el);
  }
}

function renderRoom() {
  const room = state.room || {};
  $('room-name').textContent = room.name || state.room_id;
  $('room-sub').textContent = state.is_host ? 'You are the host' : 'Host: ' + (room.host_id || '—');

  const badge = $('room-status');
  badge.textContent = (room.status || '').replace(/_/g, ' ');
  badge.className = 'badge ' + (room.status === 'open' ? 'open' : 'locked');

  $('my-ip').textContent = state.virtual_ip || '—';
  $('host-ip').textContent = state.host_ip || '—';
  $('adapter').textContent = state.adapter || '—';

  $('btn-connect').textContent = state.connected ? 'Reconnect' : 'Connect';
  $('btn-play').disabled = !state.connected;
  $('btn-play').title = state.connected ? '' : 'Connect first';
  $('host-controls').classList.toggle('hidden', !state.is_host);

  const list = $('players');
  list.innerHTML = '';
  for (const p of (room.players || [])) {
    const li = document.createElement('li');
    const left = document.createElement('span');
    left.textContent = p;
    if (p === room.host_id) {
      const tag = document.createElement('span');
      tag.className = 'tag';
      tag.textContent = 'host';
      left.append(tag);
    }
    if (p === state.player) {
      const tag = document.createElement('span');
      tag.className = 'tag';
      tag.textContent = 'you';
      left.append(tag);
    }
    li.append(left);

    if (state.is_host && p !== state.player) {
      const btn = document.createElement('button');
      btn.className = 'danger';
      btn.textContent = 'Kick';
      btn.onclick = () => act(() => api('/api/rooms/kick', { target: p }), p + ' removed for 5 minutes');
      li.append(btn);
    }
    list.append(li);
  }
}

function render() {
  renderPills();

  if (state.service_error) $('foot').textContent = state.service_error;
  else if (state.coordinator_error) $('foot').textContent = 'Server: ' + state.coordinator_error;
  else if (state.tunnel_error) $('foot').textContent = 'Tunnel: ' + state.tunnel_error;
  else $('foot').textContent = 'Prototype client · leave the black window open while you play';

  if (!state.configured) { show('view-setup'); return; }
  if (state.room_id) { show('view-room'); renderRoom(); return; }
  show('view-lobby');
  renderRooms();
}

async function refresh() {
  try {
    const next = await api('/api/state');
    if (state.room_id && next.room_gone) toast('That room has closed.', 'err');
    state = next;
    render();
  } catch (e) {
    $('pill-service').textContent = 'app not responding';
    $('pill-service').className = 'pill bad';
  }
}

// --- wiring -------------------------------------------------------------

$('btn-setup').onclick = () => act(async () => {
  await api('/api/setup', {
    coordinator: $('in-coordinator').value.trim(),
    token: $('in-token').value.trim(),
    player: $('in-player').value.trim(),
    nick: $('in-nick').value.trim(),
  });
}, 'Ready to play');

$('btn-create').onclick = () => act(() =>
  api('/api/rooms/create', { name: $('in-roomname').value.trim() }), 'Room created');

$('btn-connect').onclick = () => act(async () => {
  await api('/api/connect', {});
  // Connecting is asynchronous inside the service; wait for it to land so
  // the button does not appear to do nothing.
  for (let i = 0; i < 30; i++) {
    await new Promise((r) => setTimeout(r, 500));
    const s = await api('/api/state');
    if (s.connected) return;
    if (s.tunnel_error) throw new Error(s.tunnel_error);
  }
  throw new Error('The tunnel did not come up. Check that the server is reachable.');
}, 'Connected');

$('btn-play').onclick = () => act(async () => {
  const r = await api('/api/play', {
    mode: parseInt($('in-mode').value, 10),
    team: $('in-team').value,
  });
  toast(r.role === 'host'
    ? 'Dota is starting. When the map has loaded, lock the room.'
    : 'Dota is starting and will connect to the host.', 'ok');
});

$('btn-leave').onclick = () => act(() => api('/api/rooms/leave', {}), 'Left the room');
$('btn-lock').onclick = () => act(() => api('/api/rooms/status', { status: 'locked_in_game' }), 'Room locked');
$('btn-open').onclick = () => act(() => api('/api/rooms/status', { status: 'open_to_new_players' }), 'Room reopened');

$('in-roomname').addEventListener('keydown', (e) => { if (e.key === 'Enter') $('btn-create').click(); });

refresh();
setInterval(() => { if (!busy) refresh(); }, 2500);
