//   node web/conn_test.mjs
//
// Drives the page's connection status out of web/player.js: the card over the
// canvas, the page title and the HUD readouts (the "connection status" section,
// which touches no DOM), and the reconnect policy of the real Net class. Both
// are lifted from the shipped file and run in a vm context, so the test cannot
// drift from the code. The DOM wiring is driven end to end by
// web/page_test.mjs; the checks at the end pin the markup and styles it uses.

import { readFileSync } from "node:fs";
import vm from "node:vm";

const read = (name) => readFileSync(new URL("./" + name, import.meta.url), "utf8");
const src = read("player.js");

let pass = 0, fail = 0;
function check(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g === w) { pass++; console.log(`  ok   ${name}`); }
  else { fail++; console.log(`  FAIL ${name}\n         got  ${g}\n         want ${w}`); }
}

// lift returns the source from one marker up to the next, or null.
function lift(text, from, to) {
  const s = text.indexOf(from);
  const e = s < 0 ? -1 : text.indexOf(to, s + from.length);
  return e < 0 ? null : text.slice(s, e);
}

const status = lift(src, "// connection status:", "// end of connection status");
let S = null;
if (status) {
  const ctx = { console };
  vm.createContext(ctx);
  vm.runInContext(status + "\n;globalThis.__S = { ConnState, fpsLabel, stateLabel, geomLabel };", ctx);
  S = ctx.__S;
}

const CONNECTING = { text: "Connecting…" };
const LOST = { text: "Connection lost. Reconnecting…" };
const RETRY = { text: "Reconnecting…" };
const waiting = (device) => ({ text: "Waiting for video from " + device + "…" });
const ended = (reason) => ({ text: "Session ended", detail: reason, button: "Reconnect" });

// ---------------------------------------------------------------------------
// 1. the card: every state the page can be in, and every way between them
// ---------------------------------------------------------------------------
console.log("status card");
check("player.js has the connection status section", S !== null, true);
if (S) {
  const c = new S.ConnState();
  check("before the socket first opens", c.card(), CONNECTING);
  c.closed();
  check("a first attempt that fails is still connecting", c.card(), CONNECTING);
  c.hello("Pixel 7");
  check("open, no frame yet: waiting for video from the device", c.card(), waiting("Pixel 7"));
  c.video();
  check("the first frame hides the card", c.card(), null);

  c.closed();
  check("closed without a bye: connection lost, reconnecting", c.card(), LOST);
  c.hello("Pixel 7");
  check("reopened: waiting again, the last socket's frames do not count", c.card(), waiting("Pixel 7"));
  c.video();
  check("hidden as soon as frames arrive again", c.card(), null);

  c.ended("Quit from the browser.");
  check("a bye: session ended, the reason, and a Reconnect button", c.card(), ended("Quit from the browser."));
  c.video();
  check("a frame still in flight after the bye does not hide it", c.card(), ended("Quit from the browser."));
  c.closed();
  check("the socket closing after the bye leaves it ended", c.card(), ended("Quit from the browser."));
  c.reconnect();
  check("Reconnect: reconnecting until the socket opens again", c.card(), RETRY);
  c.closed();
  check("a failed retry is still reconnecting", c.card(), RETRY);
  c.hello("Pixel 7");
  check("open: waiting for video", c.card(), waiting("Pixel 7"));
  c.video();
  check("frame: hidden", c.card(), null);
  c.closed();
  check("a later drop is a lost connection again", c.card(), LOST);
  c.hello("Pixel 7");
  c.video();

  console.log("Reconnect before the ended socket closes");
  c.ended("scterm stopped.");
  c.reconnect();
  check("the old socket still open: not live", c.card(), RETRY);
  c.video();
  check("a frame still arriving on it does not make it live", c.card(), RETRY);
  c.closed();
  check("its close keeps reconnecting", c.card(), RETRY);
  c.hello("Pixel 7");
  check("the new socket's hello: waiting for video", c.card(), waiting("Pixel 7"));
  c.video();
  check("and its first frame: hidden", c.card(), null);

  console.log("device name");
  check("the title names the device", c.title(), "Pixel 7 · scterm");
  c.hello("");
  check("a hello without a name keeps the known one", c.title(), "Pixel 7 · scterm");
  c.closed();
  check("so does a lost connection", c.title(), "Pixel 7 · scterm");
  const blank = new S.ConnState();
  check("the title before any hello is plain scterm", blank.title(), "scterm");
  blank.hello("");
  check("no name at all: waiting for the device", blank.card(), waiting("the device"));
  blank.ended(undefined);
  check("a bye without a reason still ends the session", blank.card(), ended(""));
}

// ---------------------------------------------------------------------------
// 2. HUD readouts
// ---------------------------------------------------------------------------
console.log("HUD readouts");
if (S) {
  check("fps shows the rate while frames arrive", S.fpsLabel(59.94, 16), "59.9 fps");
  check("fps still shows the rate just under 2 s", S.fpsLabel(30, 1999), "30.0 fps");
  check("fps says idle after 2 s without a frame", S.fpsLabel(30, 2000), "idle");
  check("fps says idle before the first frame", S.fpsLabel(0, Infinity), "idle");

  const OK = { text: "connected", cls: "ok" };
  check("state: connected when nothing went wrong", S.stateLabel(true, Infinity, Infinity), OK);
  check("state: red for a decode error in the last 5 s", S.stateLabel(true, 4999, Infinity),
    { text: "connected · decode errors", cls: "bad" });
  check("state: back to connected 5 s after it", S.stateLabel(true, 5000, Infinity), OK);
  check("state: red for a lost frame in the last 5 s", S.stateLabel(true, Infinity, 10),
    { text: "connected · frames lost", cls: "bad" });
  check("state: back to connected 5 s after it", S.stateLabel(true, Infinity, 5000), OK);
  check("state: control disabled outranks both", S.stateLabel(false, 0, 0),
    { text: "control disabled", cls: "warn" });

  check("geometry: the canvas", S.geomLabel({ canvasW: 1280, canvasH: 720, videoW: 1280, videoH: 720 }),
    "1280×720");
  check("geometry: the canvas and a different video size",
    S.geomLabel({ canvasW: 640, canvasH: 360, videoW: 1280, videoH: 720 }), "640×360 ← 1280×720");
  check("geometry: the video size before the first frame",
    S.geomLabel({ canvasW: 0, canvasH: 0, videoW: 1280, videoH: 720 }), "1280×720");
  check("geometry: nothing known yet", S.geomLabel({ canvasW: 0, canvasH: 0, videoW: 0, videoH: 0 }), "—");
}

// ---------------------------------------------------------------------------
// 3. reconnect policy (the real Net class)
// ---------------------------------------------------------------------------
console.log("reconnect policy");
{
  const netSrc = lift(src, "class Net", "class View");
  if (!netSrc) throw new Error("cannot locate class Net in player.js");

  class FakeWS {
    constructor(url) {
      this.url = url;
      this.readyState = FakeWS.CONNECTING;
      FakeWS.last = this;
      FakeWS.count++;
    }
    send() {}
    close() { this.readyState = FakeWS.CLOSED; }
  }
  FakeWS.CONNECTING = 0;
  FakeWS.OPEN = 1;
  FakeWS.CLOSING = 2;
  FakeWS.CLOSED = 3;
  FakeWS.count = 0;

  const timers = [];
  const states = [];
  const nctx = {
    WebSocket: FakeWS,
    location: { protocol: "http:", host: "127.0.0.1:6969" },
    setState: (text) => states.push(text),
    setTimeout: (fn, ms) => timers.push({ fn, ms }),
    clearTimeout() {},
    console,
  };
  vm.createContext(nctx);
  vm.runInContext(netSrc + "\n;globalThis.__Net = Net;", nctx);

  const open = (ws) => { ws.readyState = FakeWS.OPEN; ws.onopen(); };
  const close = (ws) => { ws.readyState = FakeWS.CLOSED; ws.onclose({ code: 1000 }); };
  const say = (ws, msg) => ws.onmessage({ data: JSON.stringify(msg) });

  const net = new nctx.__Net();
  const byes = [];
  let closes = 0;
  net.on("bye", (msg) => byes.push(msg.reason));
  net.onClose = () => closes++;
  net.connect();
  let ws = FakeWS.last;
  open(ws);
  close(ws);
  check("closed without a bye: the page is told", closes, 1);
  check("and a retry is scheduled after the first backoff", timers.map((t) => t.ms), [250]);
  timers.shift().fn();
  check("the retry opens a new socket", FakeWS.count, 2);

  ws = FakeWS.last;
  open(ws);
  say(ws, { op: "bye", reason: "scterm stopped." });
  check("the bye still reaches the page's handler", byes, ["scterm stopped."]);
  close(ws);
  check("after a bye the page is still told the socket closed", closes, 2);
  check("but nothing is retried", timers.length, 0);
  check("and no socket is opened", FakeWS.count, 2);
  check("the HUD says the session ended", states[states.length - 1], "session ended");

  check("Net can reconnect on request", typeof net.reconnect, "function");
  if (typeof net.reconnect === "function") {
    net.reconnect();
    check("Reconnect opens a socket at once", FakeWS.count, 3);
    ws = FakeWS.last;
    close(ws);
    check("a Reconnect that fails keeps retrying, from the first backoff", timers.map((t) => t.ms), [250]);
    timers.shift().fn();
    ws = FakeWS.last;
    open(ws);
    say(ws, { op: "bye", reason: "scterm stopped." });
    ws.readyState = FakeWS.CLOSING;
    net.reconnect();
    check("Reconnect while the old socket is still closing opens nothing yet", FakeWS.count, 4);
    close(ws);
    check("its close then retries", timers.map((t) => t.ms), [250]);
  }
}

// ---------------------------------------------------------------------------
// 4. the markup and styles the wiring relies on
// ---------------------------------------------------------------------------
console.log("markup");
{
  check("Reconnect never reloads the page", src.includes("location.reload"), false);

  const html = read("index.html");
  const card = /<div id="conn"([^>]*)>([^]*?)<\/div>/.exec(html);
  check("index.html has the status card", card !== null, true);
  check("announced politely as a status",
    card !== null && /role="status"/.test(card[1]) && /aria-live="polite"/.test(card[1]), true);
  check("with a Reconnect button, hidden until needed",
    card !== null && /<button id="conn-retry" type="button" hidden>/.test(card[2]), true);

  const css = read("player.css");
  const rule = /#conn \{([^}]*)\}/.exec(css);
  check("the card takes no pointer events", rule !== null && /pointer-events: none/.test(rule[1]), true);
  check("its button does", /#conn-retry \{[^}]*pointer-events: auto/.test(css), true);
  check("the motion respects prefers-reduced-motion",
    /@media \(prefers-reduced-motion: reduce\) \{[^}]*#conn[^}]*transition: none/.test(css), true);
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
