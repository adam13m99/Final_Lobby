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

  // ---- the seats that lie inside their board (D86) ---------------------
  //
  // Both of these encode an owner decision that a stylesheet cannot argue
  // for itself: a seat is a rounded row with daylight round it rather than a
  // band across the board, and nothing on this screen moves. The lobby is
  // deliberately not in either check - it keeps its travelling light, its
  // halo and the pulse on Create room, which the owner has kept.

  ["a seat is a rounded row with daylight round it, not a band across the board", `
    ${STOP}
    show("room");
    render();
    const seats = Array.from(document.querySelectorAll("#slots .slot[data-seat]"));
    const board = seats.length ? seats[0].closest(".team") : null;
    if (!board) {
      return ({ ok: false, why: "no seats on the room screen" });
    } else {
      const mine = seats.filter((s) => s.closest(".team") === board);
      const b = board.getBoundingClientRect();
      let why = "";
      for (const s of mine) {
        const r = s.getBoundingClientRect();
        if (r.left - b.left < 3 || b.right - r.right < 3) {
          why = why || ("seat " + s.dataset.seat + " runs to the edge of its board");
        }
        if (parseFloat(getComputedStyle(s).borderTopLeftRadius) < 4) {
          why = why || ("seat " + s.dataset.seat + " has square corners");
        }
      }
      for (let i = 1; i < mine.length; i++) {
        const a = mine[i - 1].getBoundingClientRect(), c = mine[i].getBoundingClientRect();
        if (c.top - a.bottom < 2) why = why || ("seats " + (i - 1) + " and " + i + " are touching");
      }
      return ({ ok: mine.length >= 5 && !why,
         why: why || (mine.length + " seats found on the first board") });
    }
  `],

  ["nothing inside a room animates itself", `
    ${STOP}
    show("room");
    render();
    const root = document.getElementById("screen-room");
    const moving = [];
    for (const el of [root].concat(Array.from(root.querySelectorAll("*")))) {
      const a = getComputedStyle(el).animationName;
      if (a && a !== "none") moving.push((el.id || el.className || el.tagName) + ": " + a);
    }
    return ({ ok: moving.length === 0,
       why: moving.length + " animated: " + moving.slice(0, 4).join(", ") })
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

  // The game mode menu, in the running page (D80). The list is markup so
  // that every mode is translated like every other label, and a Go test binds
  // it to protocol/gamemode - but a label that never reached the screen would
  // pass that test and show a host the raw key. This is the check that the
  // words arrived.
  ["both game mode menus show mode names, not keys", `
    ${STOP}
    const bad = [];
    for (const id of ["mode", "newmode"]) {
      const opts = [...document.getElementById(id).options];
      if (opts.length !== 12) bad.push("#" + id + " offers " + opts.length + " modes, want 12");
      for (const o of opts) {
        if (!o.textContent.trim() || o.textContent.includes(".")) {
          bad.push("#" + id + " option " + o.value + " reads " + JSON.stringify(o.textContent));
        }
      }
    }
    const ap = document.getElementById("newmode").querySelector('option[value="1"]');
    if (!ap || ap.textContent !== "All Pick") bad.push("mode 1 is not All Pick");
    return ({ ok: bad.length === 0, why: bad.join("; ") })
  `],

  // The host changes the mode and every other screen in the room is told,
  // because the mode is the room's rather than this window's. The saving is
  // the coordinator's job and smoke.sh covers it; what is checked here is
  // that the band above the seats redraws when the number changes, which the
  // render guard would otherwise skip.
  ["changing the game mode redraws the band", `
    ${STOP}
    if (!state.room) return ({ ok: true, why: "not in a room in this sandbox" });
    state.room.game_mode = 1;
    drawStats(state.room);
    const before = document.getElementById("roomstats").textContent;
    state.room.game_mode = 23;
    drawStats(state.room);
    const after = document.getElementById("roomstats").textContent;
    return ({ ok: after.includes("Turbo") && !before.includes("Turbo"),
       why: "the facts band still reads " + JSON.stringify(after) })
  `],

  // A watching seat is a side of its own (D81). The host in the gallery still
  // starts the match - their PC is the server either way - and both they and
  // everybody else watching go in with +jointeam spec, which is what leaves
  // all ten playing slots for players. Before this, the button was simply
  // switched off for anybody watching.
  ["a watcher goes into the match on the spectator side", `
    ${STOP}
    show("room");
    if (!state.room) return ({ ok: true, why: "not in a room in this sandbox" });
    const me = (state.room.members || []).find((m) => m.player_id === state.player_id);
    if (!me) return ({ ok: true, why: "not seated in this sandbox" });
    const was = { spectator: me.spectator, slot: me.slot };
    me.spectator = false; me.slot = 7;
    const dire = myTeam();
    me.spectator = true; me.slot = 0;
    const spec = myTeam();
    drawAction(state.room);
    const btn = document.getElementById("btn-step");
    const live = !btn.disabled && !!btn.onclick;
    me.spectator = was.spectator; me.slot = was.slot;
    return ({ ok: dire === "bad" && spec === "spec" && live,
       why: "seat 7 gives " + dire + ", the gallery gives " + spec +
            ", and the button is " + (live ? "live" : "switched off for a watcher") })
  `],

  // The room you are in is the green row now, not a row with "You are here"
  // written at the end of a line that is allowed to be cut (D81). And every
  // row says which Dota game it is playing (D80, D81).
  ["your own room is the green row, and every row names its game", `
    ${STOP}
    renderRooms(state.rooms);
    const rows = [...document.querySelectorAll("#roomlist [data-room]")];
    if (!rows.length) return ({ ok: false, why: "no rooms in the sandbox lobby" });
    const bad = [];
    for (const row of rows) {
      const mode = row.querySelector(".room-mode");
      if (!mode || !mode.textContent.trim()) bad.push("a row names no game mode");
      if (row.textContent.includes("You are here")) bad.push("a row still says You are here");
    }
    const mine = rows.find((n) => n.dataset.room === state.room_id);
    if (state.room_id) {
      if (!mine) bad.push("the room I am in is not in the list");
      else if (!mine.classList.contains("here")) bad.push("the room I am in is not marked");
    }
    const marked = rows.filter((n) => n.classList.contains("here")).length;
    if (marked > 1) bad.push(marked + " rows are marked as mine");
    // The sandbox seeds three rooms with three different modes, so a lobby
    // printing one room's mode against every row fails here rather than
    // looking correct.
    const modes = new Set(rows.map((n) => (n.querySelector(".room-mode") || {}).textContent));
    if (rows.length > 1 && modes.size < 2) {
      bad.push("every row reads " + [...modes][0] + "; the seeded rooms play different games");
    }
    return ({ ok: bad.length === 0, why: bad.join("; ") })
  `],

  // One person, one room (D82). The Join button on every row already said so;
  // Create did not, so the interface was enforcing half a rule and the
  // coordinator was enforcing none of it.
  ["Create room is switched off while you are in one", `
    ${STOP}
    const was = state.room_id;
    state.room_id = "";
    renderRooms(state.rooms);
    const free = !document.getElementById("btn-create").disabled;
    state.room_id = (state.rooms[0] || {}).id || "r-test";
    renderRooms(state.rooms);
    const btn = document.getElementById("btn-create");
    const shut = btn.disabled && !!btn.title;
    // And pressing it anyway takes you to the room you are in rather than
    // opening a dialog the coordinator will refuse.
    openCreate();
    const dialog = !document.getElementById("creategate").classList.contains("hidden");
    state.room_id = was;
    renderRooms(state.rooms);
    return ({ ok: free && shut && !dialog,
       why: "in no room the button is " + (free ? "live" : "off") +
            ", in a room it is " + (shut ? "off with a reason" : "still live") +
            (dialog ? ", and it opened the dialog anyway" : "") })
  `],

  // Bug one (D83). All four notices used to be handed `grid-area: strip`
  // directly, and grid stacks what shares an area: three of them measured
  // top=66 h=50, the same fifty pixels, and only the last one drawn could be
  // read. Raise every notice at once and insist they are still four
  // rectangles.
  ["notices stack down the page instead of on top of each other", `
    ${STOP}
    banner("Could not reach the coordinator: connection timed out", true);
    renderUpdate({ version: "2026.09.01-0900", ready: true });
    renderAds([{ id: "b1", title: "Maintenance", body: "Relay restarts at 02:00." }]);
    document.getElementById("termsmoved").classList.remove("hidden");
    const box = (id) => document.getElementById(id).getBoundingClientRect();
    const ids = ["banner", "termsmoved", "update", "banners"];
    const bad = [];
    for (const id of ids) {
      if (box(id).height < 8) bad.push(id + " is not on screen");
    }
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const a = box(ids[i]), b = box(ids[j]);
        if (a.bottom > b.top + 1 && b.bottom > a.top + 1) {
          bad.push(ids[i] + " and " + ids[j] + " overlap");
        }
      }
    }
    // And the whole column is above the stage, not printed over the lobby.
    if (box("banners").bottom > box("stage").top + 1) bad.push("the notices sit over the stage");
    return ({ ok: bad.length === 0, why: bad.join("; ") })
  `],

  // Bug two (D83). An error raised by something the person pressed used to
  // live in the same variable render() rewrites on every poll, so a poll that
  // found nothing wrong - about two seconds later - wiped it. The person saw a
  // flash and no explanation.
  ["an error from something you did survives the next poll", `
    ${STOP}
    delete state.service_error; delete state.coordinator_error;
    delete state.tunnel_error; delete state.room_gone;
    delete state.removed; delete state.build_warning;
    report("room: that slot is taken");
    const up = () => {
      const b = document.getElementById("banner");
      return b.classList.contains("hidden") ? "" : b.textContent;
    };
    const shown = up().indexOf("that slot is taken") >= 0;
    render();
    render();
    const survived = up().indexOf("that slot is taken") >= 0;
    // The next thing you do clears it - it is a reply, not a condition.
    report("");
    const cleared = up() === "";

    // The same for the room closing under you, which is the one the owner
    // actually loses: it arrives on a single poll and is never repeated, so
    // if the poll after it can wipe it, nobody ever reads it.
    state.room_gone = true;
    render();
    delete state.room_gone;
    render();
    render();
    const gone = up().indexOf(t("err.room_gone")) >= 0;
    report("");

    return ({ ok: shown && survived && cleared && gone,
      why: (shown ? "" : "it never appeared; ") +
           (survived ? "" : "a poll wiped it; ") +
           (cleared ? "" : "the next action did not clear it; ") +
           (gone ? "" : "the room closing under you was wiped by the next poll") })
  `],

  // ---- a dialog you cannot answer (D87) --------------------------------
  //
  // The room-creation dialog was cut off at both ends in a short window: its
  // card had no ceiling, and neither the card nor the overlay behind it
  // scrolled, so the Cancel and Create buttons sat below the bottom edge with
  // no way to reach them. From the outside that is a Create room button that
  // does nothing. The app's own minimum window is 640px tall and Windows
  // display scaling shrinks the CSS viewport further, so it was reachable on
  // a supported machine and no screenshot at 1440x820 could ever show it.
  //
  // The test is not "is the foot inside the box" but "can the pointer land on
  // the button", which is the thing the person was actually unable to do.

  ["the create dialog can still be answered in a short window", `
    ${STOP}
    const g = document.getElementById("creategate");
    state.room_id = "";
    openCreate();
    // The tallest the dialog gets: the password row is only drawn once the
    // box is ticked, and that is the state the owner hit it in.
    document.getElementById("newpasson").checked = true;
    drawCreateDoor();
    // .gate is fixed with inset:0, so bottom has to give way before height
    // means anything. 480px is a 640px window at 133% display scaling.
    g.style.bottom = "auto";
    g.style.height = "480px";
    const foot = g.querySelector(".gate-foot");
    const btn = foot.querySelector("button[type=submit]");
    const gr = g.getBoundingClientRect();
    const fr = foot.getBoundingClientRect();
    const br = btn.getBoundingClientRect();
    const inside = fr.bottom <= gr.bottom + 1 && fr.top >= gr.top - 1;
    const hit = document.elementFromPoint(br.left + br.width / 2, br.top + br.height / 2);
    const clickable = !!hit && (hit === btn || btn.contains(hit) || hit.contains(btn));
    // And the head must not have been pushed off the top either.
    const head = g.querySelector(".gate-head").getBoundingClientRect();
    const titled = head.top >= gr.top - 1;
    g.style.bottom = "";
    g.style.height = "";
    g.classList.add("hidden");
    return ({ ok: inside && clickable && titled,
       why: (inside ? "" : "the buttons are outside the window; ") +
            (clickable ? "" : "nothing can click the Create button; ") +
            (titled ? "" : "the title is above the top edge") })
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
