// Prove the chat dock actually reacts to a message arriving (D56).
//
// Nothing else can. smoke.sh renders the page once and reads the DOM, and the
// dock opens on a *change* between two polls - so a single snapshot always
// shows it minimised whatever the code does. This drives the live page over
// the DevTools Protocol, sends a private message to it from somebody else,
// and looks again.
//
//   node live-chat.js <app-url> <cdp-port>
// env: COORD, SENDER_SESSION, TARGET_ID
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
let failed = 0;
const ok = (what) => console.log("  OK    " + what);
const bad = (what, saw) => { failed = 1; console.log("  FAIL  " + what + "\n        saw " + saw); };

(async () => {
  const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
  const page = list.find((t) => t.type === "page");
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));

  let id = 0;
  const call = (m, p) => rpc(ws, ++id, m, p);
  const read = async (expr) =>
    (await call("Runtime.evaluate", { expression: expr, returnByValue: true })).result.value;

  await call("Runtime.enable");
  await call("Page.enable");
  await call("Page.navigate", { url: URL_ });
  await sleep(3500);

  const before = await read('document.getElementById("chatdock").className');
  if (/collapsed/.test(before)) ok("the dock is minimised while nothing is happening");
  else bad("the dock is minimised while nothing is happening", before);

  // Somebody writes to this player. The window is not looking at them, has no
  // tab open for them, and has never seen them say anything.
  const res = await fetch(process.env.COORD + "/v1/friends/messages", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-LobbyBaz-Session": process.env.SENDER_SESSION,
    },
    body: JSON.stringify({ target_id: process.env.TARGET_ID, body: "are you playing?" }),
  });
  if (res.ok) ok("a friend sends a private message");
  else bad("a friend sends a private message", res.status + " " + (await res.text()));

  // Two polls, because the count has to change between one and the next.
  await sleep(5000);

  const after = await read('document.getElementById("chatdock").className');
  if (!/collapsed/.test(after)) ok("and the dock opens itself");
  else bad("and the dock opens itself", after);

  const tabs = await read('document.getElementById("tabstrip").textContent');
  if (/Shadow Fiend/.test(tabs)) ok("with a tab for the person who wrote");
  else bad("with a tab for the person who wrote", JSON.stringify(tabs));

  ws.close();
  process.exit(failed);
})().catch((e) => { console.error("  FAIL  live chat check: " + e.message); process.exit(1); });
