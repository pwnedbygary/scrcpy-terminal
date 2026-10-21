//   node web/page_test.mjs
//
// Runs the whole of web/player.js against a small stand-in DOM and socket, and
// drives the page's connection wiring end to end: the status card and the
// dimmed canvas, the HUD readouts, the title, the Reconnect button, and where
// keys go once the session has ended; then the action bar and the controls
// sheet the page builds from the action catalog. The elements are the ones
// web/index.html declares, as the server serves it (with the catalog in it),
// so a lookup of an element the page lacks fails here as it would in a
// browser.

import { readFileSync } from "node:fs";
import vm from "node:vm";
import { catalog, servedIndex } from "./page_fixture.mjs";

const read = (name) => readFileSync(new URL("./" + name, import.meta.url), "utf8");

let pass = 0, fail = 0;
function check(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g === w) { pass++; console.log(`  ok   ${name}`); }
  else { fail++; console.log(`  FAIL ${name}\n         got  ${g}\n         want ${w}`); }
}

const E = {};
const docListeners = {};
// modality is what the last input was, "keyboard" or "pointer": a focused
// element matches :focus-visible after a key, as in a browser.
const doc = {
  title: "scterm", hidden: false, activeElement: null, modality: "pointer",
  getElementById: (id) => E[id] || null,
  querySelectorAll: () => [],
  addEventListener(t, fn) { (docListeners[t] ||= []).push(fn); },
  createElement: (tag) => el("", tag),
  body: { tagName: "BODY", appendChild() {} },
  // The Fullscreen API, as a browser that has it: a request fills the screen
  // with the element, and fullscreenchange follows.
  fullscreenEnabled: true, fullscreenElement: null, fullscreenRequests: [],
  exitFullscreen() { doc.fullscreenElement = null; fireDoc("fullscreenchange"); return Promise.resolve(); },
};
const fireDoc = (t, ev) => (docListeners[t] || []).forEach((fn) => fn(ev || {}));

// inside tells whether x is box or sits in it, and inertOf whether x or
// anything it sits in is inert.
const inside = (x, box) => { for (; x; x = x.parentNode) if (x === box) return true; return false; };
const inertOf = (x) => { for (; x; x = x.parentNode) if (x.inert) return true; return false; };

// el is a stand-in element. Writes to textContent are counted, so a live
// region rewritten with what it already says shows up. Focus works as in a
// browser: an element that is inert, or sits in one, cannot take it, and
// making the element that has it inert sends it to the page.
function el(id, tag) {
  const cls = new Set();
  const listeners = {};
  let text = "";
  let inert = false;
  const e = {
    id, value: "", hidden: false, style: {}, className: "", writes: 0,
    width: 480, height: 1080,
    tagName: (tag || "div").toUpperCase(), type: "", title: "", dataset: {}, attrs: {}, children: [],
    appendChild(c) { e.children.push(c); c.parentNode = e; return c; },
    setAttribute(k, v) { e.attrs[k] = String(v); },
    getAttribute(k) { return k in e.attrs ? e.attrs[k] : null; },
    toBlob() { e.blobs = (e.blobs || 0) + 1; },
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
    focus() { if (!inertOf(e)) doc.activeElement = e; },
    blur() { if (doc.activeElement === e) doc.activeElement = null; },
    removeAttribute(k) { delete e.attrs[k]; },
    contains: (x) => inside(x, e),
    // hovered stands for the mouse resting on the element or in it.
    hovered: false, scrollWidth: 0, clientWidth: 0, scrollLeft: 0,
    matches(sel) {
      if (sel === ":focus-visible") return doc.activeElement === e && doc.modality === "keyboard";
      if (sel === ":hover") return e.hovered;
      throw new Error("the stand-in DOM cannot match " + sel);
    },
  };
  Object.defineProperty(e, "textContent", {
    get: () => text,
    set: (v) => { e.writes++; text = String(v); },
  });
  Object.defineProperty(e, "inert", {
    get: () => inert,
    set: (v) => {
      inert = !!v;
      if (inert && inside(doc.activeElement, e)) doc.activeElement = doc.body;
    },
  });
  return e;
}

// The page's elements, hidden, with the classes and the text index.html gives them.
for (const m of servedIndex().matchAll(/<(\w+)([^>]*?)\sid="([^"]+)"([^>]*)>([^<]*)/g)) {
  const e = el(m[3], m[1]);
  e.hidden = /(^|\s)hidden(\s|\/|$)/.test(m[2] + m[4]);
  const cls = /\sclass="([^"]*)"/.exec(m[2] + m[4]);
  if (cls) e.classList.add(...cls[1].split(/\s+/).filter(Boolean));
  e.textContent = m[5].trim();
  e.writes = 0;
  E[m[3]] = e;
}
// Where each of them sits: in the nearest element with an id that holds it
// in the markup, or in the page's body.
{
  const open = [];
  const page = servedIndex().replace(/<!--[\s\S]*?-->/g, "");
  for (const [, end, tag, attrs] of page.matchAll(/<(\/?)(\w+)([^>]*)>/g)) {
    if (end) {
      const at = open.map((o) => o.tag).lastIndexOf(tag);
      if (at >= 0) open.length = at;
      continue;
    }
    const id = /\sid="([^"]+)"/.exec(attrs);
    const e = id ? E[id[1]] : null;
    if (e) e.parentNode = open.map((o) => o.e).filter(Boolean).pop() || doc.body;
    if (!["meta", "link", "input", "br", "img"].includes(tag)) open.push({ tag, e });
  }
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

doc.documentElement = el("", "html");
doc.documentElement.requestFullscreen = (opts) => {
  doc.fullscreenRequests.push(opts);
  doc.fullscreenElement = doc.documentElement;
  fireDoc("fullscreenchange");
  return Promise.resolve();
};

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
  clearTimeout: (id) => { if (timers[id - 1]) timers[id - 1].cleared = true; },
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
// The bar's Controls button as the page builds it, before anything opens the sheet.
const barSheetAtLoad = ["aria-haspopup", "aria-controls", "aria-expanded"]
  .map((k) => (E.bar.children.find((c) => c.dataset.act === "toolbar") || el("", "button")).getAttribute(k));

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
// due runs the timers set since `from` with a delay of `ms` that are still
// pending, as that much time passing would.
const due = (ms, from = 0) => timers.slice(from).filter((t) => t.ms === ms && !t.cleared)
  .forEach((t) => { t.cleared = true; t.fn(); });
const retryAfter = (n) => timers.slice(n).find((t) => t.ms === 250);
const keydown = (key, target) => {
  const ev = {
    type: "keydown", key, code: key, target, repeat: false, isComposing: false,
    altKey: false, ctrlKey: false, metaKey: false, shiftKey: false,
    prevented: false, preventDefault() { this.prevented = true; },
    getModifierState: () => false,
  };
  doc.modality = "keyboard";
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

// ---------------------------------------------------------------------------
// the action bar and the controls sheet, built from the action catalog
// ---------------------------------------------------------------------------

const live = ws();
const sent = (from) => live.sent.slice(from).map((m) => m.op);
const key = (o) => {
  const ev = Object.assign({
    type: "keydown", key: "", code: "", target: null, repeat: false, isComposing: false,
    altKey: false, ctrlKey: false, metaKey: false, shiftKey: false,
    prevented: false, preventDefault() { this.prevented = true; },
    getModifierState: () => false,
  }, o);
  doc.modality = "keyboard";
  (winListeners[ev.type] || []).forEach((fn) => fn(ev));
  return ev;
};
const CAT = catalog();
const byId = new Map(CAT.device.concat(CAT.local).map((a) => [a.id, a]));
const bar = CAT.views.webBar;
// The tooltip form, derived here from the catalog: the label, then
// Alt+<mnemonic> and F<fkey>, as in "Volume up (Alt+U, F6)".
const keysOf = (a) => [].concat(a.mnemonic ? ["Alt+" + a.mnemonic.toUpperCase()] : [], a.fkey ? ["F" + a.fkey] : []);
const named = (a) => (keysOf(a).length ? a.label + " (" + keysOf(a).join(", ") + ")" : a.label);
// The bar, written out by hand from protocol/fixtures/actions.json: each
// button's action, the short name it shows, its tooltip (the label, then
// Alt+<mnemonic> and F<fkey>) and its aria-keyshortcuts (the same chords, in
// that attribute's syntax).
const BAR = [
  ["back", "Back", "Back (Alt+B)", "Alt+B"],
  ["home", "Home", "Home (Alt+H, F1)", "Alt+H F1"],
  ["appswitch", "Rec", "Recents (Alt+T, F3)", "Alt+T F3"],
  ["menu", "Menu", "Menu key (F2)", "F2"],
  ["toolbar", "Controls", "Controls (Alt+/)", "Alt+/"],
  ["volup", "Vol+", "Volume up (Alt+U, F6)", "Alt+U F6"],
  ["voldown", "Vol-", "Volume down (Alt+D, F5)", "Alt+D F5"],
  ["mute", "Mute", "Mute device (F7)", "F7"],
  ["rotate", "Rot", "Rotate (Alt+R, F8)", "Alt+R F8"],
  ["notif", "Notif", "Notifications (Alt+N, F9)", "Alt+N F9"],
  ["settings", "Shade", "Settings shade (Alt+E, F10)", "Alt+E F10"],
  ["collapse", "Collapse", "Collapse panels (Alt+C, F11)", "Alt+C F11"],
  ["power", "Power", "Power (Alt+P, F4)", "Alt+P F4"],
  ["keyboard", "Keys", "Keyboard (Alt+I)", "Alt+I"],
  ["screenshot", "Shot", "Screenshot (Alt+S)", "Alt+S"],
  ["resetvideo", "Redraw", "Refresh video (Alt+K)", "Alt+K"],
  ["grab", "Grab", "Grab (Alt+G, F12)", "Alt+G F12"],
  ["quit", "Quit", "Quit (Alt+Q)", "Alt+Q"],
];

console.log("the action bar");
const kids = E.bar.children;
// The catalog's buttons; Full screen, the page's own, follows them.
const buttons = kids.filter((c) => c.tagName === "BUTTON" && c.dataset.act);
// A control the page did not build is a blank element, so its checks fail
// rather than stop the suite.
const button = (id) => buttons.find((b) => b.dataset.act === id) || el("", "button");
check("one button per webBar entry, in its order", buttons.map((b) => b.dataset.act), bar);
check("each shows its action's short label", buttons.map((b) => b.textContent), bar.map((id) => byId.get(id).short));
check("each tooltip is the label and the chords", buttons.map((b) => b.title), bar.map((id) => named(byId.get(id))));
check("text, tooltip and keyshortcuts, button by button",
  buttons.map((b) => [b.dataset.act, b.textContent, b.title, b.getAttribute("aria-keyshortcuts")]), BAR);
check("each is named by the text it shows: no aria-label, or one that starts with that text",
  buttons.filter((b) => b.getAttribute("aria-label") !== null && !b.getAttribute("aria-label").startsWith(b.textContent))
    .map((b) => b.dataset.act), []);
check("Volume up names both of its keys", button("volup").title, "Volume up (Alt+U, F6)");
check("Menu names its F-key", button("menu").title, "Menu key (F2)");
check("Mute says whose mute it is", button("mute").title, "Mute device (F7)");
check("the keyframe button says what it does", [button("resetvideo").textContent, button("resetvideo").title],
  ["Redraw", "Refresh video (Alt+K)"]);
check("the buttons submit nothing", buttons.every((b) => b.type === "button"), true);
check("a separator stands before Vol+, Rot, Power and Full screen, and nowhere else",
  kids.flatMap((c, i) => (c.className === "bar-sep" ? [(kids[i + 1] || { dataset: {} }).dataset.act || (kids[i + 1] || {}).textContent] : [])),
  ["volup", "rotate", "power", "Full screen"]);

console.log("the controls sheet");
const html = servedIndex();
const sheetTag = /<div id="sheet"([^>]*)>/.exec(html);
check("the sheet is a modal dialog named by its title", !!sheetTag && /role="dialog"/.test(sheetTag[1]) &&
  /aria-modal="true"/.test(sheetTag[1]) && /aria-labelledby="sheet-title"/.test(sheetTag[1]), true);
check("which says Controls, beside its chord", [E["sheet-title"].textContent, E["sheet-keys"].children.map((k) => k.textContent)],
  ["Controls", ["Alt+/"]]);
check("the ☰ button is named Controls, with its chord in the tooltip and as its keyshortcut",
  [E["menu-btn"].getAttribute("aria-label"), E["menu-btn"].title, E["menu-btn"].getAttribute("aria-keyshortcuts")],
  ["Controls", "Controls (Alt+/)", "Alt+/"]);
const menuTag = /<button id="menu-btn"([^>]*)>/.exec(html);
check("and it controls the sheet, a dialog, which starts closed", !!menuTag && /aria-haspopup="dialog"/.test(menuTag[1]) &&
  /aria-controls="sheet"/.test(menuTag[1]) && /aria-expanded="false"/.test(menuTag[1]), true);
const barSheet = button("toolbar");
check("as does the bar's Controls button, which opens the same dialog", barSheetAtLoad, ["dialog", "sheet", "false"]);
const rows = E["sheet-actions"].children;
const row = (id) => rows.find((r) => r.dataset.act === id) || el("", "button");
const labelOf = (r) => (r.children[0] || {}).textContent;
const rowIds = bar.concat([...byId.keys()].filter((id) => !bar.includes(id))).filter((id) => id !== "toolbar");
check("a row for every action but the sheet's own, in the bar's order", rows.map((r) => r.dataset.act), rowIds);
check("each row is a button named by what it shows: no aria-label, and no tooltip to read twice",
  rows.map((r) => [r.tagName, r.type, r.getAttribute("aria-label"), r.title]), rowIds.map(() => ["BUTTON", "button", null, ""]));
check("showing the label", rows.map(labelOf), rowIds.map((id) => byId.get(id).label));
check("and the chords, as keys", rows.map((r) => r.children[1].children.map((k) => k.tagName + " " + k.textContent)),
  rowIds.map((id) => keysOf(byId.get(id)).map((k) => "KBD " + k)));
check("which are its keyshortcuts", rows.map((r) => [r.dataset.act, r.getAttribute("aria-keyshortcuts")]),
  BAR.filter(([id]) => id !== "toolbar").map(([id, , , keys]) => [id, keys]));
// nameOf is a control's name from its content, as a browser computes it: the
// text it shows, less anything hidden from assistive technology.
const nameOf = (e) => (e.getAttribute("aria-hidden") === "true" ? "" :
  e.children.length ? e.children.map(nameOf).filter(Boolean).join(" ") : e.textContent);
check("so a row is named by its label alone: the chords beside it are hidden from assistive technology, " +
  "which has them as its keyshortcuts instead of twice", rows.map(nameOf), rowIds.map((id) => byId.get(id).label));
check("no control of the bar or the sheet has an aria-label that is also its title",
  buttons.concat(rows, [E["menu-btn"], E["sheet-close"]])
    .filter((c) => c.getAttribute("aria-label") !== null && c.getAttribute("aria-label") === c.title)
    .map((c) => c.dataset.act || c.id), []);
check("then the gestures and this browser's keys",
  ["right click", "middle click", "3-finger tap", "<kbd>Esc</kbd>", "<kbd>Alt+M</kbd>", "<kbd>Alt+-</kbd>", "<kbd>Alt+=</kbd>"]
    .filter((s) => !html.includes(s)), []);

const isOpen = () => !E.sheet.classList.contains("idle");
const menuBtn = E["menu-btn"];
const closeBtn = E["sheet-close"];
menuBtn.focus();
menuBtn.fire("click");
check("☰ opens it", [isOpen(), E.sheet.inert], [true, false]);
check("and says it is open", menuBtn.getAttribute("aria-expanded"), "true");
check("and so does the bar's Controls button", barSheet.getAttribute("aria-expanded"), "true");
check("and the focus moves to its first row", doc.activeElement === rows[0], true);
let n = live.sent.length;
ev = key({ key: "Tab", code: "Tab", target: rows[0] });
check("Tab moves to the next row", [doc.activeElement === rows[1], ev.prevented], [true, true]);
ev = key({ type: "keyup", key: "Tab", code: "Tab", target: rows[1] });
check("on its keydown only: its keyup leaves the focus where it is", [doc.activeElement === rows[1], ev.prevented], [true, true]);
closeBtn.focus();
key({ key: "Tab", code: "Tab", target: closeBtn });
check("and from Close, the last button, round to the first", doc.activeElement === rows[0], true);
key({ key: "Tab", code: "Tab", shiftKey: true, target: rows[0] });
check("Shift+Tab goes back round to Close", doc.activeElement === closeBtn, true);
for (const k of [{ key: "Enter", code: "Enter" }, { key: " ", code: "Space" }]) {
  const down = key(Object.assign({ target: rows[2] }, k));
  const up = key(Object.assign({ type: "keyup", target: rows[2] }, k));
  check(`${k.code} on a row is left to the browser, which presses it`, [down.prevented, up.prevented], [false, false]);
}
check("and none of those keys reached the device", sent(n), []);
ev = key({ key: "Escape", code: "Escape", target: rows[0] });
check("Esc closes it", [isOpen(), E.sheet.inert, ev.prevented], [false, true, true]);
check("and ☰ says it is closed", menuBtn.getAttribute("aria-expanded"), "false");
check("as does the bar's Controls button", barSheet.getAttribute("aria-expanded"), "false");
check("gives the focus back to ☰", doc.activeElement === menuBtn, true);
check("and is not Back while it does", sent(n), []);
barSheet.fire("click");
check("opened with the bar's Controls button, both say it is open",
  [isOpen(), menuBtn.getAttribute("aria-expanded"), barSheet.getAttribute("aria-expanded")], [true, "true", "true"]);
row("home").fire("click");
check("and a row closing it to run its action sets both back",
  [isOpen(), menuBtn.getAttribute("aria-expanded"), barSheet.getAttribute("aria-expanded")], [false, "false", "false"]);
menuBtn.fire("click");
E.sheet.fire("click", { target: rows[3] });
check("a click inside the card leaves it open", isOpen(), true);
E.sheet.fire("click", { target: E.sheet });
check("a click outside the card closes it", isOpen(), false);
menuBtn.fire("click");
closeBtn.fire("click");
check("so does Close", isOpen(), false);
key({ key: "/", code: "Slash", altKey: true, target: menuBtn });
check("Alt+/ opens it too, and ☰ says so", [isOpen(), menuBtn.getAttribute("aria-expanded")], [true, "true"]);
key({ key: "/", code: "Slash", altKey: true, target: rows[0] });
check("and closes it again", [isOpen(), menuBtn.getAttribute("aria-expanded")], [false, "false"]);
menuBtn.fire("click");
// A click on the card's text leaves the focus on the page, outside the sheet's buttons.
doc.activeElement = doc.body;
n = live.sent.length;
const elsewhere = [{ key: "Enter", code: "Enter" }, { key: " ", code: "Space" }]
  .map((k) => key(Object.assign({ target: doc.body }, k)).prevented);
check("Enter and Space aimed anywhere but its buttons are keys as usual: the device gets them",
  [elsewhere, sent(n)], [[true, true], ["key", "text"]]);
closeBtn.fire("click");
menuBtn.focus();
menuBtn.fire("click");
doc.activeElement = doc.body;
key({ key: "Escape", code: "Escape", target: doc.body });
check("with the focus on the page like that, Esc still gives it back to ☰", [isOpen(), doc.activeElement === menuBtn],
  [false, true]);

console.log("closing the sheet once the chrome has had time to fade");
// fade is 2.5 s with no input: the chrome (the HUD and the bar) fades out and
// the bar turns inert. poke is the pointer moving again, which brings it back.
const fade = () => due(2500);
const poke = () => (winListeners.pointermove || []).forEach((fn) => fn({}));
// pointer dispatches a pointer event as a browser does: at its target, then
// on the window, where the page listens in the bubble phase.
const pointer = (type, target, o) => {
  const ev = Object.assign({ type, target, pointerId: 9, pointerType: "mouse", button: 0, clientX: 5, clientY: 5,
    preventDefault() {} }, o);
  if (type === "pointerdown") doc.modality = "pointer";
  target.fire(type, ev);
  (winListeners[type] || []).forEach((fn) => fn(ev));
  return ev;
};
// click is a mouse press and release on the picture, a tap to the device.
const click = (o) => { pointer("pointerdown", E.screen, o); pointer("pointerup", E.screen, o); };
// focusOn names what has the focus: an id, a bar button or row by its action,
// or the page.
const focusOn = () => {
  const a = doc.activeElement;
  if (!a) return null;
  if (a.id) return a.id;
  if (buttons.includes(a)) return "bar:" + a.dataset.act;
  if (rows.includes(a)) return "row:" + a.dataset.act;
  return a.tagName;
};
poke();
menuBtn.focus();
menuBtn.fire("click");
fade();
key({ key: "Escape", code: "Escape", target: doc.activeElement });
check("open for 2.5 s with no input, then Esc: the focus goes back to ☰", [isOpen(), focusOn()], [false, "menu-btn"]);
menuBtn.focus();
menuBtn.fire("click");
fade();
key({ key: "/", code: "Slash", altKey: true, target: doc.activeElement });
check("and so it does after Alt+/", [isOpen(), focusOn()], [false, "menu-btn"]);
barSheet.focus();
barSheet.fire("click");
fade();
key({ key: "Escape", code: "Escape", target: doc.activeElement });
check("and to the bar's Controls button, when that opened it", [isOpen(), focusOn()], [false, "bar:toolbar"]);
check("which is shown again, with the HUD, now that the sheet is shut",
  [E.bar.classList.contains("idle"), E.bar.inert, E.hud.classList.contains("idle")], [false, false, false]);
n = live.sent.length;
key({ key: "a", code: "KeyA", target: doc.activeElement });
fade();
check("its keyboard focus does not hold the chrome during a session, when Tab, Enter and Space go to the device: " +
  "a key goes there, and 2.5 s on the chrome fades, the inert bar taking the focus with it",
  [sent(n), E.hud.classList.contains("idle"), E.bar.inert, focusOn()], [["text"], true, true, "BODY"]);
poke();
menuBtn.focus();
fade();
const onMenu = focusOn();
click();
check("☰, never inert, keeps the focus through a fade; a press on the picture is for the device, " +
  "and takes the focus off the page's buttons", [onMenu, focusOn()], ["menu-btn", null]);
fade();
check("then the chrome fades out after 2.5 s as before, the bar inert; ☰ stays, never inert",
  [E.hud.classList.contains("idle"), E.bar.classList.contains("idle"), E.bar.inert, menuBtn.inert], [true, true, true, false]);
poke();
check("and comes back when the pointer moves", [E.hud.classList.contains("idle"), E.bar.inert, menuBtn.inert],
  [false, false, false]);

// outcome presses one action's button or row and returns what that did beside
// what the catalog says it should do: a device action reaches the server as
// its op, and the page's own actions change the page and send nothing. The
// page's own toggles are put back afterwards.
const player = (expr) => vm.runInContext(expr, ctx);
function outcome(a, press) {
  const from = live.sent.length;
  const blobs = E.screen.blobs || 0;
  const was = { grab: player("input.grab"), keys: player("input.softKeys"), sheet: isOpen() };
  press();
  let got = "no check for " + a.id, want = "a check for " + a.id;
  if (a.kind === "keycode" || a.kind === "control") [got, want] = [sent(from), [a.id]];
  else if (a.id === "grab") [got, want] = [[player("input.grab") !== was.grab, sent(from)], [true, []]];
  else if (a.id === "keyboard") {
    [got, want] = [[player("input.softKeys") !== was.keys, doc.activeElement === E.ime, sent(from)], [true, true, []]];
  } else if (a.id === "screenshot") [got, want] = [[(E.screen.blobs || 0) - blobs, sent(from)], [1, []]];
  else if (a.id === "toolbar") [got, want] = [[isOpen() !== was.sheet, sent(from)], [true, []]];
  if (a.id === "grab" || a.id === "keyboard") player(`input.act("${a.id}")`);
  return [got, want];
}

console.log("a bar button runs its action");
for (const b of buttons) {
  const a = byId.get(b.dataset.act);
  if (a.id === "quit") continue; // two presses: below
  const [got, want] = outcome(a, () => b.fire("click"));
  check(`${a.short}: the action runs and the button flashes`, [got, b.classList.contains("done")], [want, true]);
  if (a.id === "toolbar") closeBtn.fire("click");
}

console.log("a row runs its action");
for (const r of rows) {
  const a = byId.get(r.dataset.act);
  if (a.id === "quit") continue; // two presses: below
  menuBtn.fire("click");
  const [got, want] = outcome(a, () => r.fire("click"));
  check(`${a.label}: the sheet closes and the action runs`, [isOpen(), got], [false, want]);
}
check("and the page's own toggles are back off", [player("input.grab"), player("input.softKeys")], [false, false]);

console.log("Quit needs a second press");
const quit = button("quit");
n = live.sent.length;
quit.fire("click");
check("the bar's first press only asks", [quit.textContent, quit.classList.contains("armed"), sent(n)], ["Quit?", true, []]);
check("in place: the button keeps the width it had", [quit.style.width, quit.style.boxSizing], ["100px", "border-box"]);
check("and it says so in its tooltip and in a toast",
  [quit.getAttribute("aria-label"), quit.title, E.toast.textContent],
  [null, "Quit? Press again to end the session", "press Quit again to end the session"]);
const toastTag = /<div id="toast"([^>]*)>/.exec(html);
check("the toast is a polite live region (role status), so it is announced as well as shown",
  !!toastTag && /role="status"/.test(toastTag[1]) && /aria-live="polite"/.test(toastTag[1]), true);
now += 1999;
quit.fire("click");
check("a second press within 2 s quits", sent(n), ["quit"]);
check("and the button is itself again",
  [quit.textContent, quit.classList.contains("armed"), quit.style.width, quit.title, quit.getAttribute("aria-label")],
  ["Quit", false, "", "Quit (Alt+Q)", null]);
n = live.sent.length;
t0 = timers.length;
quit.fire("click");
const expiry = timers.slice(t0).filter((t) => t.ms === 2000);
check("the ask lasts 2 s", expiry.length > 0, true);
now += 2001;
expiry.forEach((t) => t.fn());
check("then the button is itself again", [quit.textContent, quit.classList.contains("armed")], ["Quit", false]);
quit.fire("click");
check("and the next press asks again", [quit.textContent, sent(n)], ["Quit?", []]);
now += 2001;
quit.fire("click");
check("so does a press 2 s on, even before the page's timer has run", [quit.textContent, sent(n)], ["Quit?", []]);
button("home").fire("click");
check("any other action drops the ask", [quit.textContent, quit.classList.contains("armed"), sent(n)], ["Quit", false, ["home"]]);
quit.fire("click");
key({ key: "h", code: "KeyH", altKey: true });
check("a chord drops it too", [quit.textContent, sent(n)], ["Quit", ["home", "home"]]);
quit.fire("click");
menuBtn.fire("click");
check("and so does opening the sheet", quit.textContent, "Quit");
closeBtn.fire("click");
n = live.sent.length;
key({ key: "q", code: "KeyQ", altKey: true });
check("Alt+Q quits at once", sent(n), ["quit"]);
quit.fire("click");
n = live.sent.length;
key({ key: "q", code: "KeyQ", altKey: true });
check("also while Quit is asking, which it then stops", [sent(n), quit.textContent], [["quit"], "Quit"]);

menuBtn.fire("click");
const quitRow = row("quit");
n = live.sent.length;
quitRow.fire("click");
check("the sheet's Quit row asks first, in place, and the sheet stays",
  [labelOf(quitRow), quitRow.classList.contains("armed"), isOpen(), sent(n)], ["Quit?", true, true, []]);
check("with a tooltip that asks too", [quitRow.title, quitRow.getAttribute("aria-label")],
  ["Quit? Press again to end the session", null]);
quitRow.fire("click");
check("its second press closes the sheet and quits", [isOpen(), sent(n), labelOf(quitRow)], [false, ["quit"], "Quit"]);
check("and the row is back to no tooltip", [quitRow.title, quitRow.getAttribute("aria-label")], ["", null]);
menuBtn.fire("click");
quitRow.fire("click");
key({ key: "Escape", code: "Escape", target: quitRow });
check("closing the sheet drops the ask", [labelOf(quitRow), quitRow.classList.contains("armed")], ["Quit", false]);
// next is the sheet's control after the Quit row (dir 1) or before it (-1):
// the rows, then Full screen and Close.
const next = (dir) => {
  const list = rows.concat([E["sheet-full"], closeBtn]);
  return list[(list.indexOf(quitRow) + dir + list.length) % list.length];
};
for (const [shiftKey, name] of [[false, "Tab"], [true, "Shift+Tab"]]) {
  menuBtn.fire("click");
  quitRow.focus();
  quitRow.fire("click");
  key({ key: "Tab", code: "Tab", shiftKey, target: quitRow });
  check(`${name} moves on from an asking Quit row and drops the ask, as any other press does`,
    [doc.activeElement === next(shiftKey ? -1 : 1), labelOf(quitRow), quitRow.classList.contains("armed")],
    [true, "Quit", false]);
  closeBtn.fire("click");
}
menuBtn.fire("click");
quitRow.focus();
n = live.sent.length;
quitRow.fire("click");
key({ key: "Tab", code: "Tab", target: quitRow });
key({ key: "Tab", code: "Tab", shiftKey: true, target: doc.activeElement });
quitRow.fire("click");
check("so a press after a Tab away and back asks again rather than quitting",
  [labelOf(quitRow), isOpen(), sent(n)], ["Quit?", true, []]);
key({ key: "Escape", code: "Escape", target: quitRow });
const css = read("player.css");
const armedRule = css.indexOf("#bar button.armed, .sheet-row.armed {");
check("an asking Quit keeps its colour under the pointer: its rule follows the buttons' hover and press rules",
  armedRule > 0 && ["button:hover", "button:active"]
    .every((s) => css.indexOf(s) >= 0 && css.indexOf(s) < armedRule), true);

console.log("any other press drops the ask, as in the terminal");
// asks has the bar's Quit ask, does `what`, and returns whether Quit still
// asks and what reached the device. An ask still standing is dropped after.
const asks = (what) => {
  quit.fire("click");
  const from = live.sent.length;
  what();
  const still = quit.classList.contains("armed");
  player("toolbar.disarmQuit()");
  return [still, sent(from)];
};
const finger = { pointerId: 7, button: 0, pointerType: "touch", clientX: 5, clientY: 5, preventDefault() {} };
check("typing drops it", asks(() => key({ key: "a", code: "KeyA", target: doc.body })), [false, ["text"]]);
check("so does any other key the device gets", asks(() => key({ key: "Enter", code: "Enter", target: E.screen })),
  [false, ["key"]]);
check("a Ctrl chord the browser keeps", asks(() => key({ key: "c", code: "KeyC", ctrlKey: true, target: doc.body })),
  [false, []]);
check("and one the device gets", asks(() => key({ key: "a", code: "KeyA", ctrlKey: true, target: doc.body })),
  [false, ["key"]]);
check("a press of the picture", asks(() => E.screen.fire("pointerdown", finger)), [false, []]);
E.screen.fire("pointerup", finger);
check("the wheel over it", asks(() => E.screen.fire("wheel", { deltaY: -100, clientX: 5, clientY: 5, preventDefault() {} })),
  [false, ["wheel"]]);
const wheel = (o) => E.screen.fire("wheel", Object.assign({ deltaX: 0, deltaY: 0, clientX: 5, clientY: 5, preventDefault() {} }, o));
check("but not wheel steps too small to send a notch", asks(() => { wheel({ deltaY: 1 }); wheel({ deltaY: -1 }); }),
  [true, []]);
check("or one that only goes sideways", asks(() => wheel({ deltaX: 30 })), [true, []]);
check("nor a key let go on its own: a key-up is no press",
  asks(() => key({ type: "keyup", key: "a", code: "KeyA", target: doc.body })), [true, []]);
check("text from the phone's keyboard", asks(() => { E.ime.value = "é"; E.ime.fire("input"); }), [false, ["text"]]);
check("and a paste", asks(() => (docListeners.paste || []).forEach((fn) =>
  fn({ clipboardData: { getData: () => "pasted" }, preventDefault() {} }))), [false, ["text"]]);
check("but not a bare modifier, or the pointer only moving over the picture", asks(() => {
  key({ key: "Shift", code: "ShiftLeft" });
  E.screen.fire("pointermove", { pointerId: 8, clientX: 9, clientY: 9, preventDefault() {} });
}), [true, []]);

console.log("a held key presses a button once");
// hold presses a key as a browser does, held down for `repeats` repeats:
// keydowns, then a keyup, each aimed at the focused element. Enter presses
// (clicks) a focused button on each keydown the page leaves alone; Space does
// on the keyup, if the page left its first keydown alone.
function hold(o, repeats) {
  const at = () => doc.activeElement || doc.body;
  let spaceOn = null;
  for (let i = 0; i <= repeats; i++) {
    const t = at();
    const down = key(Object.assign({ target: t, repeat: i > 0 }, o));
    if (down.prevented || t.tagName !== "BUTTON") continue;
    if (o.key === "Enter") t.fire("click");
    else if (o.key === " " && i === 0) spaceOn = t;
  }
  const t = at();
  const up = key(Object.assign({ type: "keyup", target: t }, o));
  if (o.key === " " && !up.prevented && spaceOn === t) t.fire("click");
}
menuBtn.fire("click");
quitRow.focus();
n = live.sent.length;
hold({ key: "Enter", code: "Enter" }, 3);
check("a held Enter on the Quit row asks once: its repeats do not confirm it, or reach the device",
  [labelOf(quitRow), isOpen(), sent(n)], ["Quit?", true, []]);
hold({ key: "Enter", code: "Enter" }, 0);
check("a second press does confirm it", [isOpen(), sent(n)], [false, ["quit"]]);
menuBtn.fire("click");
row("home").focus();
n = live.sent.length;
hold({ key: " ", code: "Space" }, 3);
check("a held Space still presses a row, once, when it is let go", [isOpen(), sent(n)], [false, ["home"]]);
menuBtn.focus();
menuBtn.fire("click");
closeBtn.focus();
n = live.sent.length;
hold({ key: "Enter", code: "Enter" }, 3);
check("a held Enter on Close closes the sheet once, and its repeats stay away from the device",
  [isOpen(), focusOn(), sent(n)], [false, "menu-btn", []]);
menuBtn.fire("click");
n = live.sent.length;
hold({ key: "Escape", code: "Escape" }, 3);
check("a held Esc closes it, and its repeats are not Back", [isOpen(), focusOn(), sent(n)], [false, "menu-btn", []]);
menuBtn.fire("click");
row("home").focus();
n = live.sent.length;
hold({ key: "Enter", code: "Enter" }, 3);
check("a held Enter on a row runs it once, and nothing follows it", [isOpen(), sent(n)], [false, ["home"]]);
doc.activeElement = doc.body;
n = live.sent.length;
hold({ key: "Enter", code: "Enter" }, 3);
check("while a held Enter that the device gets goes on repeating there", sent(n), ["key", "key", "key", "key"]);
menuBtn.fire("click");
key({ key: "Escape", code: "Escape", target: doc.activeElement });
n = live.sent.length;
key({ key: "Escape", code: "Escape", target: doc.activeElement });
key({ key: "Escape", code: "Escape", repeat: true, target: doc.activeElement });
check("and a key whose key-up went elsewhere repeats as usual the next time it is pressed", sent(n), ["back", "back"]);
doc.activeElement = doc.body;
n = live.sent.length;
key({ key: "Enter", code: "Enter", target: doc.body });
menuBtn.fire("click");
ev = key({ key: "Enter", code: "Enter", repeat: true, target: doc.activeElement });
check("an Enter held down since before the sheet opened does not press its first row with its repeats",
  [ev.prevented, isOpen(), sent(n)], [true, true, ["key"]]);
key({ type: "keyup", key: "Enter", code: "Enter", target: doc.activeElement });
closeBtn.fire("click");

console.log("mouse buttons on the picture");
n = live.sent.length;
const at = { pointerId: 5, clientX: 10, clientY: 10, preventDefault() {} };
E.screen.fire("pointerdown", Object.assign({ button: 1, pointerType: "mouse" }, at));
E.screen.fire("pointerup", Object.assign({ button: 1, pointerType: "mouse" }, at));
E.screen.fire("contextmenu", { pointerType: "touch", preventDefault() {} });
E.screen.fire("contextmenu", { pointerType: "mouse", preventDefault() {} });
check("middle click is Home, a finger's long press is not Back, a right click is", sent(n), ["home", "back"]);

console.log("the toast");
t0 = timers.length;
quit.fire("click");
check("Quit's ask shows in the toast", [E.toast.classList.contains("show"), E.toast.textContent],
  [true, "press Quit again to end the session"]);
due(2000, t0);
check("which fades out after its time, still saying it as it goes",
  [E.toast.classList.contains("show"), E.toast.textContent], [false, "press Quit again to end the session"]);
due(200, t0);
check("and is then emptied: a status region that kept the line would still have it for a screen reader",
  E.toast.textContent, "");
t0 = timers.length;
quit.fire("click");
due(2000, t0);
key({ key: "g", code: "KeyG", altKey: true });
due(200, t0);
check("a toast shown while the last one fades keeps its words", [E.toast.classList.contains("show"), E.toast.textContent],
  [true, "grab on: Ctrl+W/T/N/R go to the device,\nCtrl+Q ends the session"]);
key({ key: "g", code: "KeyG", altKey: true });

console.log("the session ends while the sheet is open");
menuBtn.focus();
menuBtn.fire("click");
check("the sheet is open, with the focus on its first row", [isOpen(), doc.activeElement === rows[0]], [true, true]);
say(live, { op: "bye", reason: "scterm stopped." });
close(live);
check("the bye moves the focus to Reconnect", doc.activeElement === E["conn-retry"], true);
hold({ key: "Escape", code: "Escape" }, 0);
check("Esc closes the sheet and leaves the focus on Reconnect", [isOpen(), doc.activeElement === E["conn-retry"]],
  [false, true]);
const sockets = FakeWS.all.length;
hold({ key: "Enter", code: "Enter" }, 0);
check("so Enter presses Reconnect: a new socket, and the sheet stays shut", [FakeWS.all.length, isOpen()],
  [sockets + 1, false]);
w = ws();
open(w);
say(w, { op: "hello", device: "Pixel 7" });
frame(w, 1, 1);
say(w, { op: "bye", reason: "scterm stopped." });
close(w);
const shots = E.screen.blobs || 0;
button("screenshot").focus();
hold({ key: "Enter", code: "Enter" }, 3);
check("once it has ended, a held Enter presses a bar button once: one screenshot, not four",
  (E.screen.blobs || 0) - shots, 1);
const tabs = [false, true].map((repeat) => key({ key: "Tab", code: "Tab", repeat, target: doc.activeElement }).prevented);
check("while a held Tab stays the browser's, repeats and all, to move the focus on", tabs, [false, false]);
E["conn-retry"].focus();
const socketsThen = FakeWS.all.length;
hold({ key: "Enter", code: "Enter" }, 3);
w = ws();
open(w);
check("a held Enter on Reconnect reconnects once, and its repeats do not follow the hello as keys",
  [FakeWS.all.length, w.sent.map((m) => m.op)], [socketsThen + 1, ["hello"]]);
say(w, { op: "hello", device: "Pixel 7" });
frame(w, 1, 1);
doc.activeElement = doc.body;
key({ key: "Enter", code: "Enter", target: doc.body });
say(w, { op: "bye", reason: "scterm stopped." });
close(w);
ev = key({ key: "Enter", code: "Enter", repeat: true, target: doc.activeElement });
check("an Enter still held when the session ends does not press Reconnect, which now has the focus, with its repeats",
  [doc.activeElement === E["conn-retry"], ev.prevented], [true, true]);
key({ type: "keyup", key: "Enter", code: "Enter", target: doc.activeElement });

console.log("once the session has ended, what still works");
// The session has just ended, with the focus on Reconnect. Only what needs no
// device still runs, from its chord as from its button: the controls sheet,
// and a screenshot of the last frame.
click();
check("a press on the picture leaves the focus on Reconnect, where Enter presses it", focusOn(), "conn-retry");
let blobs = E.screen.blobs || 0;
ev = key({ key: "/", code: "Slash", altKey: true, target: doc.activeElement });
check("Alt+/ opens the controls sheet, as ☰ does", [ev.prevented, isOpen()], [true, true]);
ev = key({ key: "/", code: "Slash", altKey: true, repeat: true, target: doc.activeElement });
check("its auto-repeat does not close it again", [ev.prevented, isOpen()], [true, true]);
key({ key: "/", code: "Slash", altKey: true, target: doc.activeElement });
check("a second press does", isOpen(), false);
ev = key({ key: "s", code: "KeyS", altKey: true, target: doc.activeElement });
key({ key: "s", code: "KeyS", altKey: true, repeat: true, target: doc.activeElement });
check("Alt+S saves the last frame, as the Shot button does, once while it is held",
  [ev.prevented, (E.screen.blobs || 0) - blobs], [true, 1]);
const gainThen = player("pcm.localGain");
const leftAlone = [{ key: "m", code: "KeyM", altKey: true }, { key: "h", code: "KeyH", altKey: true },
  { key: "g", code: "KeyG", altKey: true }, { key: "F5", code: "F5" }, { key: "F1", code: "F1" }]
  .map((k) => key(Object.assign({ target: doc.activeElement }, k)).prevented);
check("Alt+M, the device's chords and the F-keys stay the browser's: no sound or device is left, and F5 reloads",
  [leftAlone, player("pcm.localGain") === gainThen, player("input.grab")], [[false, false, false, false, false], true, false]);
const pasteEv = { clipboardData: { getData: () => "after the end" }, prevented: false, preventDefault() { this.prevented = true; } };
let toastWrites = E.toast.writes;
(docListeners.paste || []).forEach((fn) => fn(pasteEv));
check("a paste is the browser's too: nothing is pasted, and no toast says it was",
  [pasteEv.prevented, E.toast.writes - toastWrites, E.toast.textContent === "pasted 13 chars"], [false, 0, false]);
const homeBtn = button("home");
homeBtn.classList.remove("done");
homeBtn.fire("click");
check("a device button says the session has ended, and does not flash as if it ran",
  [E.toast.textContent, homeBtn.classList.contains("done")], ["the session has ended", false]);
E.toast.textContent = "";
quit.fire("click");
check("Quit does not ask for a second press: there is nothing left to quit",
  [quit.textContent, quit.classList.contains("armed"), E.toast.textContent], ["Quit", false, "the session has ended"]);
menuBtn.fire("click");
E.toast.textContent = "";
row("back").fire("click");
check("a row closes the sheet and says so too", [isOpen(), E.toast.textContent], [false, "the session has ended"]);
blobs = E.screen.blobs || 0;
button("screenshot").fire("click");
check("while Shot still saves the last frame, and flashes", [(E.screen.blobs || 0) - blobs,
  button("screenshot").classList.contains("done")], [1, true]);
// Tab is the browser's: from Reconnect it moves the focus to ☰, then into the
// bar. The stand-in does not move the focus itself, and the chrome is already
// up here (that a Tab brings it up is checked in a browser only).
key({ key: "Tab", code: "Tab", target: doc.activeElement });
menuBtn.focus();
key({ key: "Tab", code: "Tab", target: menuBtn });
button("back").focus();
fade();
fade();
check("Tab is the browser's now, and keyboard focus on the bar holds the chrome up: fading would make the bar " +
  "inert and drop the focus to the page", [E.hud.classList.contains("idle"), E.bar.inert, focusOn()],
  [false, false, "bar:back"]);

E["conn-retry"].fire("click");
w = ws();
open(w);
say(w, { op: "hello", device: "Pixel 7" });
frame(w, 1, 1);
const now2 = () => w.sent.map((m) => m.op);

console.log("a held chord runs the page's own actions once");
// held presses a key, holds it for `repeats` auto-repeats, and lets it go.
const held = (k, repeats) => {
  for (let i = 0; i <= repeats; i++) key(Object.assign({ target: doc.body, repeat: i > 0 }, k));
  key(Object.assign({ type: "keyup", target: doc.body }, k));
};
doc.activeElement = doc.body;
held({ key: "g", code: "KeyG", altKey: true }, 3);
check("Alt+G held down: grab is on, not flipped again by each repeat", player("input.grab"), true);
held({ key: "F12", code: "F12" }, 4);
check("F12 held down: off again, once", player("input.grab"), false);
held({ key: "i", code: "KeyI", altKey: true }, 3);
check("Alt+I held down: the phone keyboard is on", player("input.softKeys"), true);
held({ key: "i", code: "KeyI", altKey: true }, 0);
doc.activeElement = doc.body;
held({ key: "/", code: "Slash", altKey: true }, 3);
check("Alt+/ held down: the sheet is open", isOpen(), true);
key({ key: "Escape", code: "Escape", target: doc.activeElement });
doc.activeElement = doc.body;
held({ key: "m", code: "KeyM", altKey: true }, 3);
check("Alt+M held down: this browser is muted", player("pcm.localGain"), 0);
held({ key: "m", code: "KeyM", altKey: true }, 0);
check("and a press unmutes it", player("pcm.localGain"), 1);
blobs = E.screen.blobs || 0;
held({ key: "s", code: "KeyS", altKey: true }, 3);
check("Alt+S held down: one screenshot", (E.screen.blobs || 0) - blobs, 1);
n = w.sent.length;
held({ key: "u", code: "KeyU", altKey: true }, 2);
held({ key: "F6", code: "F6" }, 1);
check("while a device action's chord repeats, as a key held on the device would",
  now2().slice(n), ["volup", "volup", "volup", "volup", "volup"]);

console.log("the bar behind the sheet");
const barState = () => [E.bar.classList.contains("idle"), E.bar.inert];
poke();
pointer("pointerdown", menuBtn);
menuBtn.fire("click");
poke();
const byClick = barState();
key({ key: "Escape", code: "Escape", target: doc.activeElement });
key({ key: "/", code: "Slash", altKey: true, target: doc.body });
const byKey = barState();
check("the bar is hidden behind the sheet, the same whether a click or a key opened it, and can still take " +
  "the focus back", [byClick, byKey], [[true, false], [true, false]]);
key({ key: "Escape", code: "Escape", target: doc.activeElement });
check("and is back when it closes", barState(), [false, false]);

console.log("touch");
// chromeShown says whether the HUD and the bar show, and whether the bar is inert.
const chromeShown = () => [!E.hud.classList.contains("idle"), !E.bar.classList.contains("idle"), E.bar.inert];
const barBtn = E["bar-btn"] || el("", "button");
const touchAt = (type, target, x = 50, y = 50) =>
  pointer(type, target, { pointerType: "touch", pointerId: 21, clientX: x, clientY: y });
click();
fade();
check("with a mouse, the chrome fades as before, and ⋯ is not shown", [chromeShown(), barBtn.hidden], [[false, false, true], true]);
poke();
menuBtn.fire("click");
fade();
touchAt("pointerdown", closeBtn);
closeBtn.fire("click");
touchAt("pointerup", closeBtn);
check("a sheet opened while the chrome was up and shut 2.5 s on by a finger, which brings nothing up: the chrome is back",
  [isOpen(), chromeShown()], [false, [true, true, false]]);
fade();
check("and fades on time, since its timer waited out the sheet", chromeShown(), [false, false, true]);
n = w.sent.length;
touchAt("pointerdown", E.screen);
touchAt("pointermove", E.screen, 90, 70);
touchAt("pointerup", E.screen, 90, 70);
check("a finger's drag on the picture goes to the device", now2().slice(n), ["down", "up"]);
check("and does not bring the bar up over the picture, where it would take the next tap", chromeShown(),
  [false, false, true]);
touchAt("pointerdown", E.screen);
touchAt("pointerup", E.screen);
check("nor does a tap", chromeShown(), [false, false, true]);
check("a touch shows ⋯ instead, beside ☰", barBtn.hidden, false);
barBtn.fire("click");
check("⋯ brings the chrome up, and says it is expanded", [chromeShown(), barBtn.getAttribute("aria-expanded")],
  [[true, true, false], "true"]);
fade();
fade();
check("and it stays: no timer puts it away under a finger", chromeShown(), [true, true, false]);
n = w.sent.length;
touchAt("pointerdown", button("home"));
button("home").fire("click");
touchAt("pointerup", button("home"));
check("a finger on one of its buttons runs it, and the bar stays", [now2().slice(n), chromeShown()],
  [["home"], [true, true, false]]);
E.bar.hovered = true; // what a finger touched keeps :hover after it lifts
touchAt("pointerdown", E.screen);
touchAt("pointerup", E.screen);
check("a finger on the picture puts it away, though the finger's :hover is left on the bar",
  [chromeShown(), barBtn.getAttribute("aria-expanded")], [[false, false, true], "false"]);
E.bar.hovered = false;
barBtn.fire("click");
barBtn.fire("click");
check("as does ⋯ again", [chromeShown(), barBtn.getAttribute("aria-expanded")], [[false, false, true], "false"]);
fireDoc("visibilitychange");
check("coming back to the tab does not bring it up", chromeShown(), [false, false, true]);
barBtn.fire("click");
touchAt("pointerdown", barSheet);
barSheet.fire("click");
touchAt("pointerup", barSheet);
check("the bar's Controls button, shown by ⋯, opens the sheet, with the bar hidden behind it",
  [isOpen(), chromeShown()], [true, [true, false, false]]);
touchAt("pointerdown", closeBtn);
closeBtn.fire("click");
touchAt("pointerup", closeBtn);
fade();
check("a finger on Close shows the bar again, and it stays: ⋯ still holds it", [isOpen(), chromeShown()],
  [false, [true, true, false]]);
poke();
check("a mouse takes over: ⋯ goes, and the chrome comes up as before", [barBtn.hidden, chromeShown()],
  [true, [true, true, false]]);
fade();
check("and ⋯'s hold goes with touch mode: the chrome fades as a mouse's does", chromeShown(), [false, false, true]);
fireDoc("visibilitychange");
check("with a mouse, coming back to the tab shows it, as loading the page does", chromeShown(), [true, true, false]);
touchAt("pointerdown", E.screen);
touchAt("pointerup", E.screen);
key({ key: "Unidentified", code: "", keyCode: 229, target: E.ime });
key({ key: "a", code: "KeyA", target: doc.body });
check("back in touch mode, a key does not bring it up, from a phone's own keyboard (key Unidentified, aimed at its " +
  "hidden field) or any other: the next tap there would run a bar button", chromeShown(), [false, false, true]);
poke();

console.log("a mouse resting on the bar");
E.bar.hovered = true;
fade();
fade();
check("while the mouse rests on the bar, it does not fade", chromeShown(), [true, true, false]);
E.bar.hovered = false;
fade();
check("once it is off the bar, it does", chromeShown(), [false, false, true]);
poke();
E.bar.scrollWidth = 1000;
E.bar.clientWidth = 400;
E.bar.scrollLeft = 0;
const wheelOnBar = (o) => {
  const e = Object.assign({ deltaX: 0, deltaY: 0, deltaMode: 0, prevented: false, preventDefault() { this.prevented = true; } }, o);
  E.bar.fire("wheel", e);
  return e;
};
let we = wheelOnBar({ deltaY: 120 });
check("a wheel turned over a bar that does not fit scrolls it sideways", [E.bar.scrollLeft, we.prevented], [120, true]);
wheelOnBar({ deltaY: 3, deltaMode: 1 });
check("a line of the wheel is 16 px of it", E.bar.scrollLeft, 168);
we = wheelOnBar({ deltaX: 40, deltaY: 10 });
check("a sideways scroll is left to the browser", [E.bar.scrollLeft, we.prevented], [168, false]);
E.bar.scrollWidth = 400;
we = wheelOnBar({ deltaY: 120 });
check("and a bar that fits does not move", [E.bar.scrollLeft, we.prevented], [168, false]);

console.log("full screen");
const fullBar = E.bar.children[E.bar.children.length - 1] || el("", "button");
const fullSheet = E["sheet-full"] || el("", "button");
check("the bar ends with Full screen, this browser's own, named by its text",
  [fullBar.tagName, fullBar.type, fullBar.textContent, fullBar.getAttribute("aria-label"), fullBar.dataset.act],
  ["BUTTON", "button", "Full screen", null, undefined]);
check("a toggle that says it is off, as is the sheet's", [fullBar.getAttribute("aria-pressed"), fullSheet.hidden,
  fullSheet.getAttribute("aria-pressed"), fullBar.title], ["false", false, "false", "Full screen"]);
fullBar.fire("click");
await flush();
check("a press fills the screen with the whole page, without the browser's navigation bar",
  [doc.fullscreenElement === doc.documentElement, doc.fullscreenRequests], [true, [{ navigationUI: "hide" }]]);
check("and both buttons say so", [fullBar.getAttribute("aria-pressed"), fullSheet.getAttribute("aria-pressed"), fullBar.title],
  ["true", "true", "Leave full screen (Esc)"]);
fullBar.fire("click");
await flush();
check("a second press leaves it", [doc.fullscreenElement, fullBar.getAttribute("aria-pressed")], [null, "false"]);
fullBar.fire("click");
doc.fullscreenElement = null;
fireDoc("fullscreenchange");
check("and when the browser leaves it (Esc), the buttons follow",
  [fullBar.getAttribute("aria-pressed"), fullSheet.getAttribute("aria-pressed")], ["false", "false"]);
menuBtn.fire("click");
closeBtn.focus();
key({ key: "Tab", code: "Tab", shiftKey: true, target: closeBtn });
check("in the sheet, Tab reaches it, just before Close", doc.activeElement === fullSheet, true);
fullSheet.fire("click");
await flush();
check("where it closes the sheet and fills the screen", [isOpen(), doc.fullscreenElement === doc.documentElement],
  [false, true]);
doc.exitFullscreen();
doc.documentElement.requestFullscreen = () => Promise.reject(new Error("denied"));
fullBar.fire("click");
await flush();
check("a browser that refuses says so in a toast", E.toast.textContent, "full screen is not available here");

console.log("the canvas fits the layout viewport");
// A phone zooms a page that can be zoomed out to show whatever overflows it,
// and its innerWidth and innerHeight grow with that; the window the picture
// is in is still the layout viewport.
doc.documentElement.clientWidth = 390;
doc.documentElement.clientHeight = 844;
Object.assign(ctx.window, { innerWidth: 1052, innerHeight: 2277 });
E.screen.width = 1080;
E.screen.height = 2400;
(winListeners.resize || []).forEach((fn) => fn({}));
check("a 1080x2400 picture in a 390x844 window is 380x844, however far innerWidth has grown",
  [E.screen.style.width, E.screen.style.height], ["380px", "844px"]);
n = w.sent.length;
due(120);
check("and the server is told the window's size, not the overflow's", w.sent.slice(n).filter((m) => m.op === "resize")
  .map((m) => [m.w, m.h]), [[390, 844]]);
// A notch and a home indicator: player.css offsets the canvas by the safe
// area, and the picture fits what is left.
ctx.getComputedStyle = (e) => (e === E.screen ? { top: "47px", right: "0px", bottom: "34px", left: "0px" } : {});
(winListeners.resize || []).forEach((fn) => fn({}));
delete ctx.getComputedStyle;
check("under a notch, it fits what the safe area leaves: 390x763, so 343x763",
  [E.screen.style.width, E.screen.style.height], ["343px", "763px"]);
doc.documentElement.clientWidth = 844;
doc.documentElement.clientHeight = 390;
E.screen.width = 2400;
E.screen.height = 1080;
ctx.getComputedStyle = (e) => (e === E.screen ? { top: "0px", right: "47px", bottom: "21px", left: "47px" } : {});
(winListeners.resize || []).forEach((fn) => fn({}));
delete ctx.getComputedStyle;
check("turned to landscape, with the safe area 47 px in from each side and 21 from the bottom: 750x369 is left, " +
  "so a 2400x1080 picture is 750x338",
  [E.screen.style.width, E.screen.style.height], ["750px", "338px"]);

console.log("the page's head, and its looks");
const head = (/<head>([^]*?)<\/head>/.exec(html) || ["", ""])[1];
const viewportMeta = (/<meta name="viewport" content="([^"]*)">/.exec(head) || ["", ""])[1];
check("the page can be zoomed: the viewport has no user-scalable=no or maximum-scale",
  /user-scalable\s*=\s*no|maximum-scale/.test(viewportMeta), false);
check("and runs under a notch or a home indicator (viewport-fit=cover)", /viewport-fit=cover/.test(viewportMeta), true);
const plain = css.replace(/\/\*[^]*?\*\//g, "");
const rules = [...plain.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([, sel, body]) => ({ sel: sel.trim(), body }));
const rule = (sel) => (rules.find((r) => r.sel === sel) || { body: "" }).body;
check("every touch on the picture is still the page's, for the device, so zooming starts only off it",
  /touch-action: none/.test(rule("#screen")), true);
check("and the canvas cannot overflow the window before its first fit, or the page would be zoomed out",
  [/max-width: 100%/.test(rule("#screen")), /max-height: 100%/.test(rule("#screen"))], [true, true]);
check("dark: a color-scheme meta, and color-scheme in :root",
  [/<meta name="color-scheme" content="dark">/.test(head), /color-scheme: dark/.test(rule(":root"))], [true, true]);
const icon = /<link rel="icon" href="([^"]*)">/.exec(head);
const svg = icon ? decodeURIComponent(icon[1].replace(/^data:image\/svg\+xml,/, "")) : "";
const csp = /Content-Security-Policy",[^]*?img-src ([^;"]*)/.exec(readFileSync(new URL("../web.go", import.meta.url), "utf8"));
check("a favicon in the page itself, an SVG data: URL that the server's CSP allows, so no /favicon.ico is asked for",
  [!!icon && icon[1].startsWith("data:image/svg+xml,"), /^<svg xmlns='http:\/\/www\.w3\.org\/2000\/svg'[^>]*>[^]*<\/svg>$/.test(svg),
    !!csp && /\bdata:/.test(csp[1])], [true, true, true]);
const tokens = [...rule(":root").matchAll(/--([\w-]+):/g)].map((m) => m[1]);
check("colours, radii, spacing and type are custom properties in :root",
  ["fg", "fg-dim", "panel", "btn", "radius", "gap", "edge", "font", "text", "text-s"].filter((t) => !tokens.includes(t)), []);
check("one style for every button", /appearance: none/.test(rule("button")) && /background: var\(--btn-bg/.test(rule("button")) &&
  /border: 1px solid var\(--btn-line/.test(rule("button")) && /cursor: pointer/.test(rule("button")), true);
check("which no button's own rule overrides with a background or a border (they set --btn-bg and --btn-line), " +
  "so hovering and pressing show on all of them",
  rules.filter((r) => /#menu-btn|#bar-btn|#sound-chip|#sheet-close|#sheet-full|#conn-retry|\.sheet-row|#bar button/.test(r.sel) &&
    !/\.(armed|done)\b/.test(r.sel) && /(^|[;{\s])(background|border|border-color):/.test(r.body)).map((r) => r.sel), []);
check("every button shows the keyboard focus", /outline: 2px solid/.test(rule("button:focus-visible")), true);
const reducedBlock = (/@media \(prefers-reduced-motion: reduce\) \{([^]*?)\n\}/.exec(plain) || ["", ""])[1];
check("reduced motion turns off every transition and animation, not just a few",
  /\*, \*::before, \*::after \{[^}]*transition: none !important;[^}]*animation: none !important;/.test(reducedBlock), true);
check("no rules for what never happens: a faded ☰, a grabbed canvas", [css.includes("#menu-btn.idle"), css.includes(".grabbed")],
  [false, false]);
// The sides of the window each of them sits against, and so must keep clear of.
const sides = { "#bar": "rbl", "#hud": "rbl", "#menu-btn, #bar-btn": "t", "#menu-btn": "l", "#bar-btn": "l", "#sound": "tr",
  "#toast": "trl", "#conn": "rl", "#sheet": "trbl", "#screen": "trbl" };
check("the bar, the HUD, ☰ and ⋯, the sound chip, the toast, the status card, the sheet and the picture keep clear of a notch " +
  "and a home indicator, on every side they sit against",
  Object.entries(sides).flatMap(([sel, s]) => [...s].filter((x) => !rule(sel).includes(`var(--safe-${x})`)).map((x) => sel + " " + x)), []);
check("the bar is one row that scrolls sideways, rather than rows that wrap",
  [/flex-wrap: wrap/.test(rule("#bar")), /overflow-x: auto/.test(rule("#bar"))], [false, true]);
check("only its buttons take pointer events, so a tap between them reaches the picture",
  [/pointer-events: none/.test(rule("#bar")), /pointer-events: auto/.test(rule("#bar button"))], [true, true]);
check("a finger gets buttons 44 CSS px or more each way", /--tap: 44px/.test((/@media \(pointer: coarse\) \{([^]*?)\n\}/.exec(plain) || ["", ""])[1]) &&
  /min-width: var\(--tap\)/.test(rule("button")) && /min-height: var\(--tap\)/.test(rule("button")), true);
const narrow = (/@media \(max-width: (\d+)px\) \{([^]*?)\n\}/.exec(plain) || ["", "0", ""]);
check("on a narrow window the toast shows below ☰, ⋯ and the sound chip, not over them",
  Number(narrow[1]) >= 700 && /#toast \{[^}]*top: calc\([^;]*var\(--ctl\)/.test(narrow[2]), true);
const short = (/@media \(max-height: (\d+)px\) \{([^]*?)\n\}/.exec(plain) || ["", "0", ""]);
check("a short window gets denser rows in the sheet", Number(short[1]) >= 736 && /\.sheet-row \{[^}]*padding: 3px/.test(short[2]), true);

// contrast is WCAG 2's contrast ratio of a text colour on a background,
// both over a white picture: the background's alpha lets the white through.
// var() comes from :root (or the rule's own custom properties), and a
// gradient counts as its most transparent stop, where some text sits.
const vars = Object.fromEntries([...rule(":root").matchAll(/--([\w-]+):\s*([^;]+);/g)].map(([, k, v]) => [k, v.trim()]));
const resolve = (v, own = {}) => v.replace(/var\(--([\w-]+)(?:,\s*([^()]*(?:\([^()]*\))?))?\)/g,
  (_, k, dflt) => resolve(own[k] || vars[k] || dflt || "", own));
const ownVars = (sel) => Object.fromEntries([...rule(sel).matchAll(/--([\w-]+):\s*([^;]+);/g)].map(([, k, v]) => [k, v.trim()]));
const decl = (sel, prop) => {
  const m = new RegExp("(?:^|[;\\s])" + prop + ":\\s*([^;]+);").exec(rule(sel));
  return m ? resolve(m[1].trim(), ownVars(sel)) : "";
};
const colours = (v) => [...v.matchAll(/#[0-9a-f]{6}\b|#[0-9a-f]{3}\b|rgba?\([^)]*\)/gi)].map(([c]) => {
  if (c[0] === "#") {
    const h = c.length === 4 ? c.slice(1).split("").map((x) => x + x).join("") : c.slice(1);
    return [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16)).concat([1]);
  }
  const p = c.replace(/rgba?\(|\)/g, "").split(",").map(Number);
  return [p[0], p[1], p[2], p.length > 3 ? p[3] : 1];
});
const onWhite = (c) => c.slice(0, 3).map((x) => x * c[3] + 255 * (1 - c[3]));
const lin = (x) => { x /= 255; return x <= 0.04045 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); };
const lum = (rgb) => 0.2126 * lin(rgb[0]) + 0.7152 * lin(rgb[1]) + 0.0722 * lin(rgb[2]);
const contrast = (fg, bgSel) => {
  const bgs = colours(decl(bgSel, "--btn-bg") || decl(bgSel, "background"));
  if (!bgs.length) return 1;
  const bg = onWhite(bgs.reduce((a, b) => (b[3] < a[3] ? b : a)));
  const text = colours(fg)[0];
  if (!text) return 1;
  const t = onWhite(text).map((x, i) => x * text[3] + bg[i] * (1 - text[3]));
  const [hi, lo] = [lum(t), lum(bg)].sort((a, b) => b - a);
  return (hi + 0.05) / (lo + 0.05);
};
const pageText = decl("html, body", "color");
const pairs = [
  ["the HUD's readouts", pageText, "#hud"], ["its help row", decl("#hud .row.help", "color"), "#hud"],
  ["its separators", decl("#hud .sep", "color"), "#hud"], ["its state, ok", decl("#state.ok", "color"), "#hud"],
  ["warning", decl("#state.warn", "color"), "#hud"], ["and bad", decl("#state.bad", "color"), "#hud"],
  ["the toast", decl("#toast", "color"), "#toast"], ["the status card", pageText, "#conn"],
  ["its detail", decl("#conn-detail", "color"), "#conn"], ["the sound chip", pageText, "#sound-chip"],
  ["☰ and ⋯", pageText, "#menu-btn, #bar-btn"],
];
check("text over the picture reads at 4.5:1 or better, even over a white picture",
  pairs.filter(([, fg, bg]) => !(contrast(fg, bg) >= 4.5)).map(([name, fg, bg]) => name + " " + contrast(fg, bg).toFixed(2)), []);

console.log("a paste while the connection is lost");
close(w);
const lostPaste = { clipboardData: { getData: () => "lost" }, prevented: false, preventDefault() { this.prevented = true; } };
(docListeners.paste || []).forEach((fn) => fn(lostPaste));
check("the page still takes it, as the session goes on, but says it was not pasted",
  [lostPaste.prevented, E.toast.textContent], [true, "not connected: nothing pasted"]);

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
