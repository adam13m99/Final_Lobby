// Drive the running app over the DevTools Protocol and assert what the page
// does *while it changes*.
//
//   node ui-assert.js <app-url> <cdp-port>
//
// This is the rung the harness was missing. check.sh proves every module
// builds and its tests pass; smoke.sh proves the API works and that the page
// renders once. Neither can see what happens on the second render, and every
// interface bug this project has actually shipped lives exactly there: a list
// rebuilt twice a second under the reader's pointer, a row left behind in the
// document when its replacement was inserted, a render guard that was never
// once true because a live measurement was inside it.
//
// Each check below is one expression evaluated in the page. It returns
// {ok, why}. The page is real, the coordinator behind it is real, and the
// data is the seeded lobby from sandbox.sh.
const [, , URL_, PORT] = process.argv;

const rpc = (ws, id, method, params) =>
  new Promise((resolve, reject) => {
    const onMsg = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id !== id) return;
      ws.removeEventListener("message", onMsg);
      m.error ? reject(new Error(m.error.message)) : resolve(m.result);
    };
    ws.addEventListener("message", onMsg);
    ws.send(JSON.stringify({ id, method, params: params || {} }));
  });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Every check stops the poll first. These run inside one page, one after
// another, and a poll landing in the middle of one would overwrite the state
// it just set up - which would make a green run mean nothing.
const STOP = "for (let i = 1; i < 5000; i++) clearInterval(i);";

const CHECKS = [
  ["the lobby draws each room exactly once, however often it changes", `
    ${STOP}
    const want = state.rooms.length;
    renderRooms(state.rooms);
    for (const seats of [9, 8, 7, 6, 5]) {
      state.rooms[0].seats = seats;
      renderRooms(state.rooms);
    }
    const rows = document.querySelectorAll("#roomlist [data-room]").length;
    return ({ ok: rows === want, why: rows + " rows in the document for " + want + " rooms" })
  `],

  ["a poll that changes nothing leaves the room list alone", `
    ${STOP}
    renderRooms(state.rooms);
    const before = document.querySelector("#roomlist [data-room]");
    before.dataset.witness = "kept";
    renderRooms(state.rooms);
    renderRooms(state.rooms);
    const after = document.querySelector("#roomlist [data-room]");
    return ({ ok: !!after && after.dataset.witness === "kept",
       why: "the row was thrown away and rebuilt by a poll that changed nothing" })
  `],

  ["a new host ping repaints a row rather than rebuilding it", `
    ${STOP}
    renderRooms(state.rooms);
    const row = document.querySelector("#roomlist [data-room]");
    row.dataset.witness = "kept";
    const was = row.querySelector(".room-ping").textContent;
    const r = state.rooms.find((x) => x.id === row.dataset.room);
    r.host_relay_ms = (r.host_relay_ms || 0) + 37;
    renderRooms(state.rooms);
    const now = document.querySelector("#roomlist [data-room]");
    if (!now || now.dataset.witness !== "kept") {
      return ({ ok: false, why: "a number that moves by itself rebuilt the whole row" });
    } else {
      return ({ ok: now.querySelector(".room-ping").textContent !== was,
         why: "the row was kept but the new ping was never painted into it" });
    }
  `],

  ["the room draws fourteen seats and no more", `
    ${STOP}
    show("room");
    render();
    const seats = document.querySelectorAll("#slots .slot[data-seat]");
    const ids = new Set(Array.from(seats).map((s) => s.dataset.seat));
    return ({ ok: seats.length === 14 && ids.size === 14,
       why: seats.length + " seat cards, " + ids.size + " of them distinct" })
  `],

  ["a new seat ping repaints a seat rather than rebuilding the boards", `
    ${STOP}
    show("room");
    render();
    const card = document.querySelector("#slots .slot.taken");
    if (!card) {
      return ({ ok: false, why: "nobody is seated in the sandbox room" });
    } else {
      card.dataset.witness = "kept";
      const who = (state.room.members || []).find((m) => !m.spectator);
      who.relay_ms = (who.relay_ms || 0) + 21;
      render();
      const again = document.querySelector("#slots .slot.taken");
      return ({ ok: !!again && again.dataset.witness === "kept",
         why: "a new ping rebuilt twenty seat cards" });
    }
  `],

  ["a poll that changes nothing leaves the seats alone", `
    ${STOP}
    show("room");
    render();
    const card = document.querySelector("#slots .slot");
    card.dataset.witness = "kept";
    render();
    render();
    const again = document.querySelector("#slots .slot");
    return ({ ok: !!again && again.dataset.witness === "kept",
       why: "the seat boards were rebuilt by a poll that changed nothing" })
  `],

  ["the friends rail survives a poll that changes nothing", `
    ${STOP}
    render();
    const row = document.querySelector("#friendlist .friend");
    if (!row) {
      return ({ ok: true, why: "no friends in the sandbox, nothing to keep" });
    } else {
      row.dataset.witness = "kept";
      render();
      const again = document.querySelector("#friendlist .friend");
      return ({ ok: !!again && again.dataset.witness === "kept",
         why: "the friends rail was rebuilt by a poll that changed nothing" });
    }
  `],

  ["every dialog can be closed with Escape", `
    ${STOP}
    const gates = ["creategate", "roomsetgate", "invitegate", "profilegate",
                   "passgate", "termsgate"];
    const stuck = [];
    for (const id of gates) {
      const gate = document.getElementById(id);
      gate.classList.remove("hidden");
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      if (!gate.classList.contains("hidden")) stuck.push(id);
      gate.classList.add("hidden");
    }
    return ({ ok: stuck.length === 0, why: "Escape does not close " + stuck.join(", ") })
  `],
];

(async () => {
  const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
  const page = list.find((t) => t.type === "page");
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));

  let id = 0;
  const call = (m, p) => rpc(ws, ++id, m, p);

  const noise = [];
  ws.addEventListener("message", (ev) => {
    const m = JSON.parse(ev.data);
    if (m.method === "Runtime.consoleAPICalled" && m.params.type !== "log") {
      noise.push(m.params.args.map((a) => a.value || a.description).join(" "));
    }
    if (m.method === "Runtime.exceptionThrown") {
      noise.push(m.params.exceptionDetails.text);
    }
  });

  await call("Runtime.enable");
  await call("Page.enable");
  await call("Emulation.setDeviceMetricsOverride",
    { width: 1440, height: 820, deviceScaleFactor: 1, mobile: false });
  await call("Page.navigate", { url: URL_ });
  await sleep(3500);

  let bad = 0;
  for (const [name, expr] of CHECKS) {
    let out;
    try {
      const res = await call("Runtime.evaluate", { expression: `(() => {${expr}})()`, returnByValue: true });
      out = res.exceptionDetails
        ? { ok: false, why: res.exceptionDetails.text + " " + (res.exceptionDetails.exception || {}).description }
        : res.result.value;
    } catch (e) {
      out = { ok: false, why: e.message };
    }
    if (out && out.ok) {
      console.log("  OK    " + name);
    } else {
      bad++;
      console.log("  FAIL  " + name);
      console.log("        " + ((out && out.why) || "no answer from the page"));
    }
  }

  if (noise.length) {
    bad++;
    console.log("  FAIL  the console stayed quiet throughout");
    console.log("        " + noise.slice(0, 5).join(" | "));
  } else {
    console.log("  OK    the console stayed quiet throughout");
  }

  ws.close();
  process.exit(bad ? 1 : 0);
})().catch((e) => { console.error("ui-assert failed:", e.message); process.exit(1); });
