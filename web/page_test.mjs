//   node web/page_test.mjs
//
// Runs the whole of web/player.js against a small stand-in DOM and socket, and
// drives the page's connection wiring end to end: the status card and the
// dimmed canvas, the HUD readouts, the title, the Reconnect button, and where
// keys go once the session has ended. The elements are the ones web/index.html
// declares, so a lookup of an element the page lacks fails here as it would in
// a browser.

import { readFileSync } from "node:fs";
import vm from "node:vm";

const read = (name) => readFileSync(new URL("./" + name, import.meta.url), "utf8");

let pass = 0, fail = 0;
function check(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g === w) { pass++; console.log(`  ok   ${name}`); }
  else { fail++; console.log(`  FAIL ${name}\n         got  ${g}\n         want ${w}`); }
}

const E = {};
const doc = {
  title: "scterm", hidden: false, activeElement: null,
  getElementById: (id) => E[id] || null,
  querySelectorAll: () => [],
  addEventListener() {},
  createElement: () => el("x"),
  body: { appendChild() {} },
};

// el is a stand-in element. Writes to textContent are counted, so a live
// region rewritten with what it already says shows up.
function el(id) {
  const cls = new Set();
  const listeners = {};
  let text = "";
  const e = {
    id, value: "", hidden: false, inert: false, style: {}, className: "", writes: 0,
    width: 480, height: 1080,
    classList: {
      add: (...c) => c.forEach((x) => cls.add(x)),
      remove: (...c) => c.forEach((x) => cls.delete(x)),
      contains: (c) => cls.has(c),
      toggle: (c, on) => { if (on === undefined) on = !cls.has(c); on ? cls.add(c) : cls.delete(c); return on; },
    },
    addEventListener(t, fn) { (listeners[t] ||= []).push(fn); },
    removeEventListener() {},
    fire(t, ev) { (listeners[t] || []).forEach((fn) => fn(ev || {})); },
    getContext: () => ({ drawImage() {}, putImageData() {} }),
    getBoundingClientRect: () => ({ left: 0, top: 0, width: 100, height: 40 }),
    setPointerCapture() {},
    focus() { doc.activeElement = e; },
    blur() { if (doc.activeElement === e) doc.activeElement = null; },
  };
  Object.defineProperty(e, "textContent", {
    get: () => text,
    set: (v) => { e.writes++; text = String(v); },
  });
  return e;
}

// The page's elements, hidden and with the text index.html gives them.
for (const m of read("index.html").matchAll(/<(\w+)([^>]*?)\sid="([^"]+)"([^>]*)>([^<]*)/g)) {
  const e = el(m[3]);
  e.hidden = /(^|\s)hidden(\s|\/|$)/.test(m[2] + m[4]);
  e.textContent = m[5].trim();
  e.writes = 0;
  E[m[3]] = e;
}

let now = 1000;
const timers = [];
const intervals = [];
const winListeners = {};
class FakeWS {
  constructor(url) { this.url = url; this.readyState = 0; this.sent = []; FakeWS.all.push(this); }
  send(s) { this.sent.push(JSON.parse(s)); }
  close() { this.readyState = 3; }
}
FakeWS.all = [];
FakeWS.CONNECTING = 0; FakeWS.OPEN = 1; FakeWS.CLOSING = 2; FakeWS.CLOSED = 3;

const ctx = {
  document: doc,
  window: {
    addEventListener(t, fn) { (winListeners[t] ||= []).push(fn); },
    removeEventListener(t, fn) { winListeners[t] = (winListeners[t] || []).filter((f) => f !== fn); },
    innerWidth: 1280, innerHeight: 720, devicePixelRatio: 1, document: doc,
  },
  location: { protocol: "http:", host: "127.0.0.1:6969" },
  WebSocket: FakeWS,
  performance: { now: () => now },
  setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length; },
  clearTimeout() {},
  setInterval: (fn, ms) => { intervals.push({ fn, ms }); return intervals.length; },
  requestAnimationFrame: () => 0,
  ImageData: class { constructor(d, w, h) { this.data = d; this.width = w; this.height = h; } },
  Blob: class { constructor(parts, opt) { this.parts = parts; this.opt = opt; } },
  createImageBitmap: () => Promise.reject(new Error("bad jpeg")),
  Uint8ClampedArray, ArrayBuffer, DataView, JSON, Math, Object, Array, Set, Map, WeakMap, Promise, String, Number,
  console,
};
vm.createContext(ctx);
vm.runInContext(read("player.js"), ctx);

const ws = () => FakeWS.all[FakeWS.all.length - 1];
const open = (w) => { w.readyState = 1; w.onopen(); };
const close = (w) => { w.readyState = 3; w.onclose({ code: 1000 }); };
const say = (w, m) => w.onmessage({ data: JSON.stringify(m) });
let seq = 0;
const frame = (w, format = 1, s) => {
  seq = s || seq + 1;
  const body = format === 1 ? 2 * 2 * 4 : 8;
  const buf = new ArrayBuffer(16 + body);
  const v = new DataView(buf);
  v.setUint8(0, 1); v.setUint32(1, seq); v.setUint16(5, 2); v.setUint16(7, 2); v.setUint16(9, 600); v.setUint8(11, format);
  w.onmessage({ data: buf });
};
const status = (w, extra) => say(w, Object.assign({ op: "status", control: true, videoW: 1080, videoH: 2400,
  canvasW: 0, canvasH: 0, audio: "off", devVolOk: false }, extra || {}));
const card = () => ({
  text: E["conn-text"].textContent, detail: E["conn-detail"].textContent,
  button: E["conn-retry"].hidden ? null : E["conn-retry"].textContent,
  off: E.conn.classList.contains("off"), dim: E.screen.classList.contains("dim"),
});
const flush = () => new Promise((r) => setTimeout(r, 0));
const tick = () => intervals.forEach((i) => i.fn());
const retryAfter = (n) => timers.slice(n).find((t) => t.ms === 250);
const keydown = (key, target) => {
  const ev = {
    type: "keydown", key, code: key, target, repeat: false, isComposing: false,
    altKey: false, ctrlKey: false, metaKey: false, shiftKey: false,
    prevented: false, preventDefault() { this.prevented = true; },
    getModifierState: () => false,
  };
  (winListeners.keydown || []).forEach((fn) => fn(ev));
  return ev;
};
const HIDDEN = { text: "", detail: "", button: null, off: true, dim: false };

console.log("page load");
check("the page has the card's elements", ["conn", "conn-text", "conn-detail", "conn-retry"].every((id) => E[id]), true);
check("Connecting… card, dimmed canvas", card(), { text: "Connecting…", detail: "", button: null, off: false, dim: true });
check("one socket", FakeWS.all.length, 1);

console.log("open and hello");
let w = ws();
open(w);
check("hello sent on open", w.sent.map((m) => m.op), ["hello"]);
say(w, { op: "hello", device: "Pixel 7", proto: 1 });
check("waiting card", card(), { text: "Waiting for video from Pixel 7…", detail: "", button: null, off: false, dim: true });
check("title", doc.title, "Pixel 7 · scterm");
check("#device", E.device.textContent, "Pixel 7");
const writes = E["conn-text"].writes;
say(w, { op: "hello", device: "Pixel 7", proto: 1 });
check("an unchanged card is not rewritten (it is a live region)", E["conn-text"].writes, writes);
status(w);
check("#device kept after status", E.device.textContent, "Pixel 7");
check("#geom shows the video size before a frame", E.geom.textContent, "1080×2400");
check("#fps idle with no frame", E.fps.textContent, "idle");
check("#state connected", [E.state.textContent, E.state.className], ["connected", "ok"]);

console.log("frames");
frame(w);
check("card hidden, canvas bright", card(), HIDDEN);
now += 16;
frame(w);
status(w, { canvasW: 2, canvasH: 2, videoW: 2, videoH: 2 });
check("#fps shows a rate", /^\d+\.\d fps$/.test(E.fps.textContent), true);
check("#geom canvas", E.geom.textContent, "2×2");
now += 2500;
status(w, { canvasW: 2, canvasH: 2, videoW: 2, videoH: 2 });
check("#fps idle after 2.5 s without a frame", E.fps.textContent, "idle");

console.log("lost frame");
frame(w, 1, seq + 3);
status(w);
check("#state red for a lost frame", [E.state.textContent, E.state.className], ["connected · frames lost", "bad"]);
now += 5001;
status(w);
check("#state back to connected 5 s later", [E.state.textContent, E.state.className], ["connected", "ok"]);

console.log("decode error");
frame(w, 0);
await flush();
status(w);
check("#state red for a decode error", [E.state.textContent, E.state.className], ["connected · decode errors", "bad"]);
now += 5001;
status(w);
check("#state back to connected 5 s later", [E.state.textContent, E.state.className], ["connected", "ok"]);

console.log("lost connection");
let t0 = timers.length;
close(w);
check("lost card, dimmed", card(), { text: "Connection lost. Reconnecting…", detail: "", button: null, off: false, dim: true });
check("#state reconnecting", E.state.textContent, "reconnecting…");
check("a reconnect is scheduled", !!retryAfter(t0), true);
E.fps.textContent = "60.0 fps";
tick();
check("the telemetry tick marks #fps idle while closed (counters kept)", E.fps.textContent, "idle · 1 errors · 2 lost");
retryAfter(t0).fn();
check("new socket", FakeWS.all.length, 2);
w = ws();
open(w);
check("still lost until hello", card().text, "Connection lost. Reconnecting…");
say(w, { op: "hello", device: "Pixel 7" });
check("waiting again", card().text, "Waiting for video from Pixel 7…");
frame(w, 1, 1);
check("hidden again", card().off, true);

console.log("keys while connected");
let ev = keydown("Enter", E.screen);
check("Enter goes to the device", [ev.prevented, w.sent[w.sent.length - 1]], [true, { op: "key", code: 66, meta: 0 }]);

console.log("bye");
say(w, { op: "bye", reason: "Quit from the browser." });
check("session ended card", card(), { text: "Session ended", detail: "Quit from the browser.", button: "Reconnect", off: false, dim: true });
check("the Reconnect button has the focus", doc.activeElement === E["conn-retry"], true);
const sentBeforeKey = w.sent.length;
ev = keydown("Enter", E.screen);
check("a key between the bye and the close is not sent", [ev.prevented, w.sent.length], [false, sentBeforeKey]);
frame(w, 1, 2);
check("a frame after the bye keeps it", card().text, "Session ended");
t0 = timers.length;
close(w);
check("no reconnect after a bye", !!retryAfter(t0), false);
check("no new socket", FakeWS.all.length, 2);
check("#state session ended", E.state.textContent, "session ended");
check("card still ended", card().text, "Session ended");

console.log("keys once the session has ended");
ev = keydown("Enter", E["conn-retry"]);
check("Enter on the button is left to the browser, which presses it", ev.prevented, false);
ev = keydown(" ", E["conn-retry"]);
check("so is Space", ev.prevented, false);
ev = keydown("Tab", E["conn-retry"]);
check("and Tab, which moves the focus on", ev.prevented, false);
ev = keydown("Tab", E["menu-btn"]);
check("keys aimed anywhere else are the browser's too, so Tab can come back", ev.prevented, false);
ev = keydown("Enter", E.screen);
check("Enter on the picture as well: there is no device", ev.prevented, false);
E.sheet.classList.add("idle");
E["menu-btn"].fire("click");
check("the controls sheet opens", E.sheet.classList.contains("idle"), false);
ev = keydown("Escape", E["conn-retry"]);
check("Esc closes it, even from the Reconnect button", [ev.prevented, E.sheet.classList.contains("idle")], [true, true]);

console.log("Reconnect");
E["conn-retry"].fire("click");
check("reconnecting card, no button", card(), { text: "Reconnecting…", detail: "", button: null, off: false, dim: true });
check("socket opened at once", FakeWS.all.length, 3);
w = ws();
open(w);
say(w, { op: "hello", device: "Pixel 7" });
check("waiting", card().text, "Waiting for video from Pixel 7…");
frame(w, 1, 1);
check("hidden", card(), HIDDEN);
ev = keydown("Enter", E.screen);
check("keys reach the device again", [ev.prevented, w.sent[w.sent.length - 1]], [true, { op: "key", code: 66, meta: 0 }]);

console.log("Reconnect before the ended socket closes");
say(w, { op: "bye", reason: "scterm stopped." });
E["conn-retry"].fire("click");
check("reconnecting at once, dimmed", card(), { text: "Reconnecting…", detail: "", button: null, off: false, dim: true });
check("no second socket while the old one is open", FakeWS.all.length, 3);
frame(w, 1, 2);
check("a frame still arriving on the old socket keeps the card and the dim", card(),
  { text: "Reconnecting…", detail: "", button: null, off: false, dim: true });
t0 = timers.length;
close(w);
check("the old socket's close retries", !!retryAfter(t0), true);
check("still reconnecting", card().text, "Reconnecting…");
retryAfter(t0).fn();
w = ws();
open(w);
say(w, { op: "hello", device: "Pixel 7" });
check("the new socket waits for video", card().text, "Waiting for video from Pixel 7…");
frame(w, 1, 1);
check("and its first frame hides the card", card(), HIDDEN);

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
