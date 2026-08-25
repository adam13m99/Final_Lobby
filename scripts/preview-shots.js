// Drive headless Chrome over the DevTools Protocol to photograph the app.
//
// --dump-dom cannot click, and every screen after the lobby is behind a
// toolbar button. This connects properly, calls the page's own show() to move
// between screens, and writes a PNG per screen.
//
//   node shots.js <app-url> <out-dir> <cdp-port>
const [, , URL_, OUT, PORT] = process.argv;
const fs = require("fs");
const path = require("path");

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

(async () => {
  const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
  const page = list.find((t) => t.type === "page");
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));

  let id = 0;
  const call = (m, p) => rpc(ws, ++id, m, p);

  const errors = [];
  ws.addEventListener("message", (ev) => {
    const m = JSON.parse(ev.data);
    if (m.method === "Runtime.consoleAPICalled" && m.params.type !== "log") {
      errors.push(m.params.args.map((a) => a.value || a.description).join(" "));
    }
    if (m.method === "Runtime.exceptionThrown") {
      errors.push(m.params.exceptionDetails.text + " " +
        (m.params.exceptionDetails.exception || {}).description);
    }
  });

  await call("Runtime.enable");
  await call("Page.enable");
  await call("Emulation.setDeviceMetricsOverride",
    { width: Number(process.env.WIDE || 1440), height: Number(process.env.TALL || 820), deviceScaleFactor: 1, mobile: false });

  await call("Page.navigate", { url: URL_ });
  await sleep(3500);

  const shots = JSON.parse(process.env.SHOTS || '[["lobby",""]]');
  for (const [name, script] of shots) {
    if (script) {
      await call("Runtime.evaluate", { expression: script, awaitPromise: true });
      await sleep(900);
    }
    const { data } = await call("Page.captureScreenshot", { format: "png" });
    fs.writeFileSync(path.join(OUT, name + ".png"), Buffer.from(data, "base64"));
    console.log("shot", name);
  }

  if (errors.length) console.log("CONSOLE:", errors.join(" | "));
  ws.close();
})().catch((e) => { console.error("shots failed:", e.message); process.exit(1); });
