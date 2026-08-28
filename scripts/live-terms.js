// Prove the terms cannot be accepted without being read, and can be once they
// have been (D61).
//
// Nothing else on the ladder can see this. check.sh proves the script parses,
// smoke.sh renders the page once and reads the DOM, and preview.sh photographs
// it - but the gate is a relationship between a scroll position and a
// disabled attribute, and it has two opposite ways to fail. Left too strict
// and nobody can ever create an account, which is the whole product; left too
// loose and the button that records somebody's consent means nothing.
//
//   node live-terms.js <app-url> <cdp-port>
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
const is = (what, got, want) => (got === want ? ok(what) : bad(what, JSON.stringify(got)));

(async () => {
  const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
  const page = list.find((t) => t.type === "page");
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));

  let id = 0;
  const call = (m, p) => rpc(ws, ++id, m, p);
  const read = async (expr) =>
    (await call("Runtime.evaluate", { expression: expr, returnByValue: true })).result.value;

  const errors = [];
  ws.addEventListener("message", (ev) => {
    const m = JSON.parse(ev.data);
    if (m.method === "Runtime.exceptionThrown") {
      errors.push(m.params.exceptionDetails.text);
    }
  });
  await call("Runtime.enable");
  await call("Page.navigate", { url: URL_ });
  await sleep(4000);

  // --- the sign-up side of the front door ---------------------------------
  await read('gateMode("signup"); $("namegate").classList.remove("hidden"); 1');
  await sleep(300);
  is("the sign-up form offers the terms",
    await read('!$("readterms").classList.contains("hidden")'), true);

  // The link lives inside the checkbox's own label. Opening the terms must
  // not tick the box it is there to explain.
  await read('$("readterms").click(); 1');
  await sleep(1500);
  is("reading them does not tick the box", await read('$("a-terms").checked'), false);
  is("the terms opened", await read('!$("termsgate").classList.contains("hidden")'), true);
  is("accept is refused before it is read", await read('$("termsok").disabled'), true);
  is("and it is offered at all", await read('!$("termsok").classList.contains("hidden")'), true);

  // --- read it --------------------------------------------------------------
  await read('const b = $("termstext"); b.scrollTop = b.scrollHeight; termsRead(); 1');
  await sleep(300);
  is("accept is allowed at the end", await read('$("termsok").disabled'), false);
  is("and the page says so", await read('$("termspct").textContent.indexOf("100") >= 0'), true);

  await read('$("termsok").click(); 1');
  await sleep(500);
  is("accepting closes them", await read('$("termsgate").classList.contains("hidden")'), true);
  is("and ticks the box", await read('$("a-terms").checked'), true);

  // --- from settings there is nothing to accept -----------------------------
  await read('$("namegate").classList.add("hidden"); show("settings"); $("btn-showterms").click(); 1');
  await sleep(1500);
  is("settings offers no accept button",
    await read('$("termsok").classList.contains("hidden")'), true);
  is("and the text arrived", await read('$("termstext").querySelectorAll("h3").length > 3'), true);

  if (errors.length) bad("the console said nothing", errors.join(" | "));
  else ok("the console said nothing");

  ws.close();
  process.exit(failed);
})().catch((e) => { console.error("terms check failed:", e.message); process.exit(1); });
