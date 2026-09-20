//   node web/input_test.mjs
//
// Drives the real Input class out of web/player.js with synthetic pointer and
// key events, asserting exactly which ops reach the device. The trick is that
// the class only ever touches els/toast/performance/document/pcm/toolbar, so
// the input section of player.js can be lifted into a vm context and run for
// real -- no copy of the logic, so the test cannot drift from the shipped code.

import { readFileSync } from "node:fs";
import vm from "node:vm";

const SRC = new URL("./player.js", import.meta.url);
const src = readFileSync(SRC, "utf8");

const start = src.indexOf("// Android keycodes");
const end = src.indexOf("// screenshot (Alt+S)");
if (start < 0 || end < 0 || end <= start) throw new Error("cannot locate input section");
const code = src.slice(start, end);
if (!code.includes("class Input")) throw new Error("slice does not contain class Input");

// ---------------------------------------------------------------------------
// stubs
// ---------------------------------------------------------------------------

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const doc = { activeElement: null, addEventListener() {}, removeEventListener() {} };
function fakeEl(id) {
  const listeners = {};
  return {
    id, value: "", textContent: "", className: "",
    classList: { add() {}, remove() {}, contains: () => false },
    addEventListener(t, fn) { (listeners[t] ||= []).push(fn); },
    removeEventListener() {},
    focus() { doc.activeElement = this; },
    blur() { if (doc.activeElement === this) doc.activeElement = null; },
    setPointerCapture() {},
    fire(t, ev) { for (const fn of listeners[t] || []) fn(ev); },
  };
}

const els = { canvas: fakeEl("screen"), ime: fakeEl("ime"), toast: fakeEl("toast") };
const toasts = [];
const shots = [];

const localGain = { v: 1 };
const pcmStub = {
  resume() {},
  get localGain() { return localGain.v; },
  setLocalGain(v) { localGain.v = v; },
};

// The action bar lives after the input section, but Input.act() closes over it
// (the Alt+/ and toolbar "menu" action open the controls sheet). A stub here
// keeps the VM context faithful without dragging the Toolbar class in.
const toolbarStub = {
  toggles: 0,
  opened: false,
  toggleSheet() { this.toggles++; this.opened = !this.opened; },
};

const ctx = {
  els, doc,
  toast: (m) => toasts.push(m),
  screenshot: () => shots.push(1),
  performance: { now: () => Date.now() },
  setTimeout, clearTimeout,
  requestAnimationFrame: (fn) => setTimeout(fn, 0),
  document: doc,
  window: { addEventListener() {}, removeEventListener() {} },
  pcm: pcmStub,
  toolbar: toolbarStub,
  console,
};
vm.createContext(ctx);
vm.runInContext(code + "\n;globalThis.__Input = Input;", ctx);
const Input = ctx.__Input;

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

function harness() {
  // Fresh elements per harness: attach() registers listeners on them, and a
  // shared one would leave stale drains from earlier harnesses attached.
  els.canvas = fakeEl("screen");
  els.ime = fakeEl("ime");
  toolbarStub.toggles = 0;
  toolbarStub.opened = false;
  const net = { sent: [], send(m) { this.sent.push(m); } };
  const view = { norm: (ev) => ({ x: ev.clientX, y: ev.clientY }) };
  const input = new Input(net, view);
  input.attach();
  const c = els.canvas;
  const ops = () => net.sent.map((m) => m.op);
  const ev = (type, id, x, y) => {
    const e = { pointerId: id, clientX: x, clientY: y, button: 0, preventDefault() {} };
    if (type === "down") c.fire("pointerdown", e);
    else if (type === "move") c.fire("pointermove", e);
    else if (type === "up") { c.fire("pointerup", e); c.fire("lostpointercapture", e); }
    return e;
  };
  return { net, view, input, ev, ops };
}

let pass = 0, fail = 0;
function check(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g === w) { pass++; console.log(`  ok   ${name}`); }
  else { fail++; console.log(`  FAIL ${name}\n         got  ${g}\n         want ${w}`); }
}

// ---------------------------------------------------------------------------
// 1. a plain tap still reaches the device exactly once
// ---------------------------------------------------------------------------
console.log("plain tap");
{
  const h = harness();
  h.ev("down", 1, 100, 200);
  await sleep(30);                       // lifts inside the grace window
  h.ev("up", 1, 100, 200);
  check("tap sends down+up and nothing else", h.ops(), ["down", "up"]);
  check("tap down lands where the finger did",
    h.net.sent.map((m) => [m.x, m.y]), [[100, 200], [100, 200]]);
  check("losing capture twice does not double-fire", h.ops().length, 2);
}

// ---------------------------------------------------------------------------
// 2. a held press is flushed by the grace window
// ---------------------------------------------------------------------------
console.log("slow press");
{
  const h = harness();
  h.ev("down", 1, 10, 10);
  await sleep(120);                      // longer than SOFTKEY_GRACE_MS
  check("down is flushed when the window expires", h.ops(), ["down"]);
  h.ev("up", 1, 10, 10);
  check("up follows", h.ops(), ["down", "up"]);
}

// ---------------------------------------------------------------------------
// 3. a drag is not held back
// ---------------------------------------------------------------------------
console.log("drag");
{
  const h = harness();
  h.ev("down", 1, 0, 0);
  h.ev("move", 1, 5, 5);                 // inside the slop: still a tap
  check("jitter does not start the touch", h.ops(), []);
  h.ev("move", 1, 60, 60);               // past the slop: a drag
  await sleep(20);                       // let the rAF move flush
  check("movement releases the touch", h.ops(), ["down", "move"]);
  check("drag starts at the original contact point", h.net.sent[0], { op: "down", x: 0, y: 0 });
  check("move follows to the new position", h.net.sent[1], { op: "move", x: 60, y: 60 });
  h.ev("up", 1, 60, 60);
  check("up ends the drag", h.ops(), ["down", "move", "up"]);
}

// ---------------------------------------------------------------------------
// 4. three-finger tap: nothing reaches the device, keyboard toggles
// ---------------------------------------------------------------------------
console.log("three-finger tap");
{
  const h = harness();
  h.ev("down", 1, 100, 100);
  await sleep(15);
  h.ev("down", 2, 200, 100);
  await sleep(15);
  h.ev("down", 3, 300, 100);
  const mid = h.ops().slice();
  h.ev("up", 1, 100, 100);
  h.ev("up", 2, 200, 100);
  h.ev("up", 3, 300, 100);
  check("no stray tap reaches the device", h.ops(), []);
  check("nothing leaked mid-gesture either", mid, []);
  check("keyboard came up", h.input.softKeys, true);
  check("the field was focused", doc.activeElement === els.ime, true);

  // a second three-finger tap turns it back off
  h.ev("down", 4, 100, 100);
  h.ev("down", 5, 200, 100);
  h.ev("down", 6, 300, 100);
  h.ev("up", 4, 100, 100);
  h.ev("up", 5, 200, 100);
  h.ev("up", 6, 300, 100);
  check("second tap turns the keyboard off", h.input.softKeys, false);
  check("and still sends nothing", h.ops(), []);
}

// ---------------------------------------------------------------------------
// 5. a two-finger gesture is neither a tap nor a keyboard toggle
// ---------------------------------------------------------------------------
console.log("two fingers");
{
  const h = harness();
  h.ev("down", 1, 50, 50);
  h.ev("down", 2, 60, 50);
  h.ev("up", 1, 50, 50);
  h.ev("up", 2, 60, 50);
  check("two fingers send nothing", h.ops(), []);
  check("two fingers do not toggle the keyboard", h.input.softKeys, false);
}

// ---------------------------------------------------------------------------
// 6. three fingers held for ages is not a tap
// ---------------------------------------------------------------------------
console.log("three fingers, held");
{
  const h = harness();
  h.ev("down", 1, 5, 5);
  h.ev("down", 2, 6, 5);
  h.ev("down", 3, 7, 5);
  await sleep(760);                      // longer than SOFTKEY_TAP_MS
  h.ev("up", 1, 5, 5);
  h.ev("up", 2, 6, 5);
  h.ev("up", 3, 7, 5);
  check("a long three-finger hold is not a tap", h.input.softKeys, false);
  check("and sends nothing", h.ops(), []);
}

// ---------------------------------------------------------------------------
// 7. phone IME key events still reach the hotkey tables (empty ev.code)
// ---------------------------------------------------------------------------
console.log("hotkeys from a phone keyboard");
{
  const h = harness();
  const key = (o) => h.input.key(Object.assign({ preventDefault() {} }, o), true);
  key({ code: "", key: "g", altKey: true, ctrlKey: false, metaKey: false, shiftKey: false });
  check("Alt+G toggles grab without a code", h.input.grab, true);
  h.net.sent.length = 0;
  key({ code: "", key: "p", altKey: true, ctrlKey: false, metaKey: false, shiftKey: false });
  check("Alt+P is read as power", h.ops(), ["power"]);
  h.net.sent.length = 0;
  key({ code: "", key: "Enter", altKey: false, ctrlKey: false, metaKey: false, shiftKey: false });
  check("Enter maps to keycode 66", h.net.sent[0], { op: "key", code: 66, meta: 0 });
  h.net.sent.length = 0;
  key({ code: "", key: "a", altKey: false, ctrlKey: false, metaKey: false, shiftKey: false });
  check("a letter is typed as text", h.net.sent[0], { op: "text", text: "a" });
}

// ---------------------------------------------------------------------------
// 8. the IME sink types what the phone keyboard composed, exactly once
// ---------------------------------------------------------------------------
console.log("IME text");
{
  const h = harness();
  // Gboard style: keydown carries nothing usable, the text lands in the field.
  const e = { code: "", key: "Unidentified", altKey: false, ctrlKey: false, metaKey: false, shiftKey: false, preventDefault() {} };
  h.input.key(e, true);
  check("an Unidentified keydown sends nothing", h.net.sent, []);
  els.ime.value = "h";
  els.ime.fire("input", {});
  check("composed text is sent", h.net.sent[0], { op: "text", text: "h" });
  check("the field is emptied so it never accumulates", els.ime.value, "");
  els.ime.value = "ello";
  els.ime.fire("compositionend", {});
  check("a composition commit is sent", h.net.sent[1], { op: "text", text: "ello" });
  check("no duplicates", h.net.sent.length, 2);
}

// ---------------------------------------------------------------------------
// 9. the wiring actually connects the input object to the DOM
// ---------------------------------------------------------------------------
console.log("wiring");
{
  // Constructing the Input is not enough. player.js spent a while building it
  // and never calling attach(), so the page mirrored a device it could not
  // drive at all -- no mouse, no keyboard, no touch -- while still streaming
  // perfectly, which made it look like a server-side fault.
  check("player.js calls input.attach()", /\binput\.attach\(\)\s*;/.test(src), true);
  check("player.js guards the pointer overlay",
    /typeof Pointer !== "undefined"/.test(src), true);
  check("attach() registers pointer handling",
    src.includes('addEventListener("pointerdown"'), true);
  check("attach() registers key handling",
    src.includes('addEventListener("keydown"'), true);
}

// ---------------------------------------------------------------------------
// 10. the picture is fitted to the window, never stretched
// ---------------------------------------------------------------------------
console.log("fit");
{
  const vs = src.indexOf("class View");
  const ve = src.indexOf("class Pcm");
  if (vs < 0 || ve <= vs) throw new Error("cannot locate class View in player.js");

  let writes = 0;
  const style = {};
  for (const k of ["width", "height"]) {
    let v = "";
    Object.defineProperty(style, k, { get: () => v, set: (nv) => { v = nv; writes++; } });
  }
  const win = { innerWidth: 1280, innerHeight: 720, devicePixelRatio: 1 };
  const canvas = { width: 1280, height: 720, style, getContext: () => ({}) };

  const vctx = { window: win, canvas, performance: { now: () => 0 }, console };
  vm.createContext(vctx);
  vm.runInContext(src.slice(vs, ve) + "\n;globalThis.__View = View;", vctx);
  const V = new vctx.__View(canvas);

  // Resize the "window", set the capture the server sent, and refit from scratch
  // the way a resize does.
  const fitSize = (vw, vh, iw, ih) => {
    win.innerWidth = vw;
    win.innerHeight = vh;
    canvas.width = iw;
    canvas.height = ih;
    V.fitW = 0;
    V.fitH = 0;
    V.fit();
    return canvas.style.width.replace("px", "") + "x" + canvas.style.height.replace("px", "");
  };

  // A window with the capture's own shape shows it exactly: no bars, no scaling.
  check("window matching the capture is 1:1", fitSize(1280, 720, 1280, 720), "1280x720");
  // Bigger window: scaled up to fill, still exactly 16:9.
  check("larger window scales up to fill", fitSize(1920, 1080, 1280, 720), "1920x1080");
  check("small window scales down to fit", fitSize(640, 360, 1280, 720), "640x360");
  // Taller than the picture: fits by width, bars top and bottom.
  check("portrait-ish window fits by width", fitSize(1000, 1000, 1280, 720), "1000x563");
  // The shape that used to squash the image in --window.
  check("wide short window fits by height", fitSize(1885, 352, 1280, 720), "626x352");
  // A device whose capture is not 16:9 at all.
  check("portrait capture in a landscape window", fitSize(1920, 1080, 720, 1280), "608x1080");

  // The whole point: both axes always get the same scale factor.
  let distorted = null;
  for (const [vw, vh] of [[1280, 720], [1920, 1080], [1000, 1000], [1885, 352], [300, 900]]) {
    const got = fitSize(vw, vh, 1280, 720);
    const [w, h] = got.split("x").map(Number);
    if (Math.abs(w / h - 1280 / 720) > 0.01) distorted = got;
  }
  check("never distorted, whatever the window shape", distorted, null);

  // Fitting runs once per geometry change, not once per frame.
  fitSize(1280, 720, 1280, 720);
  const before = writes;
  V.fit();
  check("a repeat fit does not rewrite the element", writes, before);
}

// ---------------------------------------------------------------------------
// 11. the browser's keys are the terminal's keys
// ---------------------------------------------------------------------------
console.log("terminal key parity");
{
  // The table the terminal uses (input.go fKeyMap -> appkeys.go mapAppFKey).
  // The browser used to send Android's own F1..F12 keycodes for these, so the
  // same physical key did one thing in the TUI and another in the window.
  const FKEY_OPS = {
    F1: "home", F2: "menu", F3: "appswitch", F4: "power",
    F5: "voldown", F6: "volup", F7: "mute",
    F8: "rotate", F9: "notif", F10: "settings", F11: "collapse",
  };
  for (const [fkey, op] of Object.entries(FKEY_OPS)) {
    const h = harness();
    const e = { code: fkey, key: fkey, preventDefault() {} };
    h.input.key(e, true);
    h.input.key(e, false);
    check(`${fkey} is ${op}`, h.ops(), [op]);
  }
  {
    // A phone keyboard reports the key name and no code; F12 must still grab.
    const h = harness();
    const e = { code: "", key: "F12", preventDefault() {} };
    h.input.key(e, true);
    check("F12 grabs (no code, like a phone keyboard)", h.input.grab, true);
    check("F12 sends no op of its own", h.net.sent, []);
  }
  {
    const h = harness();
    const key = (o) => h.input.key(Object.assign({ preventDefault() {} }, o), true);
    key({ code: "F1", key: "F1", altKey: true });
    check("Alt+F1 is the same as F1", h.ops(), ["home"]);
    h.net.sent.length = 0;
    key({ code: "Escape", key: "Escape" });
    check("Esc alone is Back, as in the terminal", h.ops(), ["back"]);
    h.net.sent.length = 0;
    key({ code: "KeyA", key: "a", ctrlKey: true });
    check("Ctrl+A is the letter keycode with CTRL",
      h.net.sent[0], { op: "key", code: 29, meta: 0x1000 });
    h.net.sent.length = 0;
    key({ code: "Minus", key: "-", altKey: true });
    check("Alt+- is the LOCAL volume, not a device key", h.net.sent, []);
    check("Alt+- turns the local gain down", pcmStub.localGain < 1, true);
  }
  {
    // Grab is the terminal's answer to "something else wants this key": here
    // that something is the browser (Ctrl+W closes the window, Ctrl+R reloads).
    const h = harness();
    const key = (o) => h.input.key(Object.assign({ preventDefault() {} }, o), true);
    h.input.grab = false;
    h.net.sent.length = 0;
    key({ code: "KeyW", key: "w", ctrlKey: true });
    key({ code: "KeyR", key: "r", ctrlKey: true });
    check("grab off: the browser keeps its own accelerators", h.net.sent, []);
    h.input.grab = true;
    h.net.sent.length = 0;
    key({ code: "KeyW", key: "w", ctrlKey: true });
    check("grab on: Ctrl+W reaches the device",
      h.net.sent[0], { op: "key", code: 51, meta: 0x1000 });
  }
}

// ---------------------------------------------------------------------------
// 11b. Alt+<letter> is the mnemonic device action (the no-F-key path)
// ---------------------------------------------------------------------------
console.log("mnemonic chords");
{
  // The same table as the terminal (appkeys.go appMnemonicOps). A phone
  // keyboard has Ctrl and Alt but no F-row, so these are the primary bindings.
  const MNEMONICS = {
    KeyH: "home", KeyB: "back", KeyT: "appswitch", KeyP: "power",
    KeyU: "volup", KeyD: "voldown", KeyC: "collapse", KeyN: "notif",
    KeyE: "settings", KeyR: "rotate", KeyK: "resetvideo", KeyQ: "quit",
  };
  for (const [code, op] of Object.entries(MNEMONICS)) {
    const h = harness();
    h.input.key({ code, key: code.slice(3).toLowerCase(), altKey: true, preventDefault() {} }, true);
    check(`Alt+${code.slice(3)} is ${op}`, h.ops(), [op]);
  }
  {
    // A phone keyboard leaves `code` empty: the letter must still act.
    const h = harness();
    h.input.key({ code: "", key: "h", altKey: true, preventDefault() {} }, true);
    check("a phone keyboard's Alt+H is still Home", h.ops(), ["home"]);
  }
  {
    // Local actions: keyboard and the controls sheet must not go to the device.
    const h = harness();
    h.input.key({ code: "KeyI", key: "i", altKey: true, preventDefault() {} }, true);
    check("Alt+I toggles the local keyboard", h.input.softKeys, true);
    check("Alt+I sends nothing to the device", h.net.sent, []);
  }
  {
    const h = harness();
    h.input.key({ code: "Slash", key: "/", altKey: true, preventDefault() {} }, true);
    check("Alt+/ opens the controls sheet", toolbarStub.opened, true);
    check("Alt+/ sends nothing to the device", h.net.sent, []);
  }
  {
    // An unmapped Alt+letter is still just text; no device action may appear.
    const h = harness();
    h.input.key({ code: "KeyZ", key: "z", altKey: true, preventDefault() {} }, true);
    check("an unmapped Alt+letter is still typed", h.net.sent, [{ op: "text", text: "z" }]);
  }
  {
    // Mute has no chord on purpose: Alt+M is the LOCAL mute everywhere.
    const h = harness();
    const before = pcmStub.localGain;
    h.input.key({ code: "KeyM", key: "m", altKey: true, preventDefault() {} }, true);
    check("Alt+M does not touch the device", h.net.sent, []);
    check("Alt+M changes the local gain", pcmStub.localGain !== before, true);
  }
  {
    // A phone keyboard reports the bare letter and no code; Alt+M must still
    // be the local mute there, exactly as codeOf() makes the other chords work.
    const h = harness();
    const before = pcmStub.localGain;
    h.input.key({ code: "", key: "m", altKey: true, preventDefault() {} }, true);
    check("a phone keyboard's Alt+M is still the local mute", h.net.sent, []);
    check("and it changed the local gain", pcmStub.localGain !== before, true);
  }
}

// ---------------------------------------------------------------------------
// 12. the hello goes out when the SOCKET opens, not when the script loads
// ---------------------------------------------------------------------------
console.log("negotiation on connect");
{
  // The page sends its hello (viewport + caps:"audio") one line after
  // net.connect(). The socket is still CONNECTING at that moment, so the
  // message was dropped on every page load -- and the server only feeds PCM to
  // a client that asked for it, so the browser was silent forever, in both
  // --web and --window, while the page looked perfectly connected.
  const ns = src.indexOf("class Net");
  const ne = src.indexOf("class View");
  if (ns < 0 || ne <= ns) throw new Error("cannot locate class Net in player.js");

  class FakeWS {
    constructor(url) {
      this.url = url;
      this.readyState = FakeWS.CONNECTING;
      this.sent = [];
      FakeWS.last = this;
    }
    send(s) { this.sent.push(JSON.parse(s)); }
    close() { this.readyState = FakeWS.CLOSED; }
  }
  FakeWS.CONNECTING = 0;
  FakeWS.OPEN = 1;
  FakeWS.CLOSING = 2;
  FakeWS.CLOSED = 3;

  const nctx = {
    WebSocket: FakeWS,
    location: { protocol: "http:", host: "127.0.0.1:6969" },
    setState() {},
    setTimeout, clearTimeout, console,
  };
  vm.createContext(nctx);
  vm.runInContext(src.slice(ns, ne) + "\n;globalThis.__Net = Net;", nctx);

  const net = new nctx.__Net();
  const hellos = [];
  net.onOpen = () => { hellos.push(1); net.send({ op: "hello", caps: "audio" }); };
  net.connect();
  const ws = FakeWS.last;
  check("nothing is on the wire while the socket is still opening", ws.sent, []);

  // Anything produced in that window must survive too, not vanish.
  check("a pre-open message is accepted", net.send({ op: "resize", w: 100, h: 200 }), true);
  check("and is not sent early", ws.sent, []);

  ws.readyState = FakeWS.OPEN;
  ws.onopen();
  check("hello goes out the moment the socket opens",
    ws.sent.map((m) => m.op), ["hello", "resize"]);
  check("it asks for audio", ws.sent[0].caps, "audio");

  // A reconnect is a NEW client on the server, with audio off again: the hello
  // has to be re-sent, which only an on-open hook can do.
  ws.sent.length = 0;
  net.connect();
  const ws2 = FakeWS.last;
  ws2.readyState = FakeWS.OPEN;
  ws2.onopen();
  check("a reconnect negotiates again", ws2.sent.map((m) => m.op), ["hello"]);

  check("player.js wires the hello to socket open",
    /net\.onOpen\s*=\s*\(\)\s*=>\s*sendHello\(\)/.test(src), true);
  check("player.js no longer sends it at script load",
    /^sendHello\(\);$/m.test(src), false);
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);