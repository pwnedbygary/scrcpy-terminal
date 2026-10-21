//   node web/audio_test.mjs
//
// Drives the page's sound out of web/player.js: the audio section (Pcm and the
// sound chip's rules, lifted from its header up to the input section) with the
// real web/pcm-worklet.js loaded first as a classic script, the way index.html
// loads it, and then the whole page, every script index.html lists in one
// global scope against a small DOM. Web Audio is stubbed after the specs it
// stands in for: a context starts "suspended" and runs only after a user
// activation (per the HTML spec: a key press other than Esc, a mouse press, or
// the end of a touch), and the switch to "running" is asynchronous, with a
// statechange event.
//
// What it pins: one AudioContext however many callers start the sound; a
// failed start is closed and retried by a gesture, never by the packets;
// gestures anywhere on the page start the sound, and are no longer listened
// for once it plays; without AudioWorklet the same ring plays through a
// ScriptProcessorNode; the sound chip and the HUD say what the sound is doing.

import { readFileSync } from "node:fs";
import vm from "node:vm";
import { servedIndex } from "./page_fixture.mjs";

const read = (name) => readFileSync(new URL("./" + name, import.meta.url), "utf8");
const src = read("player.js");
const ringSrc = read("pcm-worklet.js");

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

const audioSrc = lift(src, "// audio:", "// input: pointer");
const statusSrc = lift(src, "// connection status:", "// end of connection status");
if (!audioSrc) throw new Error("cannot locate the audio section of player.js");

// Two rounds of the event loop: every promise chain the code starts settles.
const tick = () => new Promise((r) => setImmediate(r)).then(() => new Promise((r) => setImmediate(r)));

// One 20 ms packet as the server sends it: 960 stereo frames of s16le. No
// sample is 0, so the silence a starved ring pads with stands out.
const FRAMES = 960;
function burst(seed) {
  const buf = new ArrayBuffer(FRAMES * 4);
  const v = new Int16Array(buf);
  for (let i = 0; i < FRAMES; i++) {
    v[i * 2] = ((i + seed) % 999) + 1;
    v[i * 2 + 1] = -v[i * 2];
  }
  return buf;
}

// The HTML spec's activation-triggering input events.
const activates = (type, ev) =>
  (type === "keydown" && ev.key !== "Escape") || type === "mousedown" || type === "touchend" ||
  (type === "pointerdown" && ev.pointerType === "mouse") ||
  (type === "pointerup" && ev.pointerType !== "mouse");

// ---------------------------------------------------------------------------
// stubs
// ---------------------------------------------------------------------------

class FakeNode {
  constructor(kind) { this.kind = kind; this.to = []; }
  connect(n) { this.to.push(n); return n; }
}

// audio returns the stub Web Audio for one page, recording into `w`.
// opts.worklet: false is a page without AudioWorklet (plain http): the context
// has no audioWorklet, while the AudioWorkletNode constructor stays defined, as
// in Chromium; opts.noWorkletNode also leaves that undefined, as in Firefox.
// opts.addModule(n) and opts.failNew(n) make the nth attempt fail. opts.rate
// is the rate the context runs at whatever it was asked for, as an older
// Safari's webkitAudioContext does (it takes no options).
function audio(w, opts) {
  class FakeAC {
    constructor(options) {
      w.made++;
      w.madeIn.push(w.event);
      if (opts.failNew && opts.failNew(w.made)) throw new Error("NotSupportedError");
      w.live++;
      w.maxLive = Math.max(w.maxLive, w.live);
      this.options = options;
      this.sampleRate = opts.rate || (options && options.sampleRate) || 44100;
      this.state = "suspended";
      this.onstatechange = null;
      this.destination = new FakeNode("destination");
      if (opts.worklet !== false) {
        this.audioWorklet = {
          addModule: (url) => {
            w.modules.push(url);
            return opts.addModule ? opts.addModule(w.made) : Promise.resolve();
          },
        };
      }
      w.ctxs.push(this);
      if (w.active) this.run();
    }
    run() { Promise.resolve().then(() => this.set("running")); }
    set(state) {
      if (this.state === "closed" || this.state === state) return;
      this.state = state;
      if (this.onstatechange) this.onstatechange({});
    }
    resume() {
      w.resumes++;
      w.resumedIn.push(w.event);
      if (w.active) this.run();
      return Promise.resolve();
    }
    suspend() {
      w.suspends++;
      return Promise.resolve().then(() => this.set("suspended"));
    }
    close() {
      if (this.state === "closed") return Promise.reject(new Error("InvalidStateError"));
      this.state = "closed";
      w.live--;
      w.closed++;
      return Promise.resolve();
    }
    createGain() {
      const n = new FakeNode("gain");
      n.gain = { value: 1 };
      return n;
    }
    createScriptProcessor(bufferSize, inputs, outputs) {
      const n = new FakeNode("script");
      Object.assign(n, { bufferSize, inputs, outputs, onaudioprocess: null });
      w.sps.push(n);
      return n;
    }
  }
  class FakeWorkletNode extends FakeNode {
    constructor(ctx, name, options) {
      super("worklet");
      Object.assign(this, { ctx, name, options });
      this.port = { onmessage: null, postMessage: (m, transfer) => w.posted.push({ m, transfer }) };
      w.worklets.push(this);
    }
  }
  return { FakeAC, FakeWorkletNode };
}

// state is what one page did, plus a window whose listeners are recorded and
// dispatched like a browser's: activation first, then capture, then bubble.
function state(g) {
  const w = {
    made: 0, live: 0, maxLive: 0, closed: 0, resumes: 0, suspends: 0, modules: [], ctxs: [],
    worklets: [], sps: [], posted: [], madeIn: [], resumedIn: [],
    active: false, event: "", listeners: [],
  };
  g.window = g;
  g.addEventListener = (type, fn, opt) => {
    const capture = typeof opt === "object" ? !!(opt && opt.capture) : !!opt;
    const once = typeof opt === "object" && !!(opt && opt.once);
    w.listeners.push({ type, fn, capture, once });
  };
  g.removeEventListener = (type, fn, opt) => {
    const capture = typeof opt === "object" ? !!(opt && opt.capture) : !!opt;
    w.listeners = w.listeners.filter((l) => !(l.type === type && l.fn === fn && l.capture === capture));
  };
  w.fire = (type, ev = {}) => {
    ev = Object.assign({ type, preventDefault() {} }, ev);
    if (activates(type, ev)) w.active = true;
    w.event = type;
    const order = w.listeners.filter((l) => l.type === type && l.capture)
      .concat(w.listeners.filter((l) => l.type === type && !l.capture));
    for (const l of order) {
      if (!w.listeners.includes(l)) continue; // removed by an earlier listener
      if (l.once) w.listeners = w.listeners.filter((x) => x !== l);
      l.fn(ev);
    }
    w.event = "";
  };
  return w;
}

// unlocks lists the gestures a Pcm is listening for, and whether in capture.
const unlocks = (w, pcm) =>
  w.listeners.filter((l) => pcm && l.fn === pcm.unlocker).map((l) => l.type + (l.capture ? "" : " (bubble)")).sort();
const ALL_UNLOCKS = ["click", "keydown", "pointerdown", "pointerup", "touchend"];

// world runs the audio section on its own: the ring first (index.html loads
// pcm-worklet.js before player.js), then the section.
function world(opts = {}) {
  const g = { console };
  const w = state(g);
  const { FakeAC, FakeWorkletNode } = audio(w, opts);
  if (!opts.noAudio) g.AudioContext = FakeAC;
  if (!opts.noWorkletNode) g.AudioWorkletNode = FakeWorkletNode;
  vm.createContext(g);
  try { vm.runInContext(ringSrc, g); } catch (e) { w.ringError = String(e); }
  vm.runInContext(audioSrc + "\n;globalThis.__A = { Pcm," +
    " soundChip: typeof soundChip === \"function\" ? soundChip : null };", g);
  w.Pcm = g.__A.Pcm;
  w.soundChip = g.__A.soundChip;
  return w;
}

// fakeEl is just enough of an element for player.js and pointer.js.
function fakeEl(tag, cls, hidden) {
  const listeners = {};
  const classes = new Set((cls || "").split(/\s+/).filter(Boolean));
  return {
    tagName: tag.toUpperCase(), id: "", hidden: !!hidden, inert: false,
    textContent: "", title: "", value: "", style: {}, dataset: {}, children: [],
    classList: {
      add: (...c) => c.forEach((x) => classes.add(x)),
      remove: (...c) => c.forEach((x) => classes.delete(x)),
      contains: (c) => classes.has(c),
      toggle(c, on) {
        if (on === undefined) on = !classes.has(c);
        if (on) classes.add(c); else classes.delete(c);
        return on;
      },
    },
    addEventListener(t, fn) { (listeners[t] ||= []).push(fn); },
    removeEventListener(t, fn) { listeners[t] = (listeners[t] || []).filter((f) => f !== fn); },
    fire(t, ev) { for (const fn of listeners[t] || []) fn(Object.assign({ type: t, preventDefault() {} }, ev)); },
    getContext: () => ({ drawImage() {}, putImageData() {} }),
    getBoundingClientRect: () => ({ left: 0, top: 0, width: 400, height: 40 }),
    setPointerCapture() {}, focus() {}, blur() {},
    appendChild(c) { this.children.push(c); return c; },
    setAttribute(k, v) { this[k] = String(v); },
    removeAttribute(k) { delete this[k]; },
    contains(x) { return x === this || this.children.includes(x); },
    matches: () => false,
    remove() {},
  };
}

// page loads every script index.html lists, in its order, into one global
// scope, the way a browser does: a script that throws is reported and the
// next one still runs. The page is the one the server serves, with the action
// catalog in it; opts.unserved loads index.html as it is on disk instead, and
// opts.coarse is a phone's, where matchMedia("(pointer: coarse)") matches.
function page(opts = {}) {
  const html = opts.unserved ? read("index.html") : servedIndex();
  const g = { console: opts.console || console };
  const w = state(g);
  const { FakeAC, FakeWorkletNode } = audio(w, opts);
  const els = new Map();
  for (const m of html.matchAll(/<(\w+)\b([^>]*)>/g)) {
    const id = /\bid="([^"]+)"/.exec(m[2]);
    if (!id) continue;
    const cls = /\bclass="([^"]*)"/.exec(m[2]);
    els.set(id[1], fakeEl(m[1], cls ? cls[1] : "", /(^|\s)hidden(\s|$)/.test(m[2])));
  }
  const catalog = /<script type="application\/json" id="actions">([^<]*)<\/script>/.exec(html);
  if (catalog) els.get("actions").textContent = catalog[1];
  class FakeWS {
    constructor(url) { this.url = url; this.readyState = 0; this.sent = []; p.ws = this; }
    send(s) { this.sent.push(JSON.parse(s)); }
    close() {}
  }
  Object.assign(FakeWS, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 });
  const timers = [];
  const p = { w, g, els, timers, errors: [], ws: null };
  Object.assign(g, {
    innerWidth: 400, innerHeight: 800, devicePixelRatio: 2,
    location: { protocol: "http:", host: "192.168.1.20:6969" },
    performance: { now: () => 0 },
    setTimeout: (fn, ms) => timers.push({ fn, ms }),
    clearTimeout: (id) => { if (id && timers[id - 1]) timers[id - 1].fn = null; },
    setInterval: () => 0,
    requestAnimationFrame: () => 0,
    WebSocket: FakeWS,
    document: {
      hidden: false, title: "", activeElement: null, body: fakeEl("body", "", false),
      getElementById: (id) => els.get(id) || null,
      querySelectorAll: () => [],
      createElement: (tag) => fakeEl(tag, "", false),
      addEventListener() {}, removeEventListener() {},
    },
  });
  if (!opts.noAudio) g.AudioContext = FakeAC;
  if (!opts.noWorkletNode) g.AudioWorkletNode = FakeWorkletNode;
  if (opts.coarse) g.matchMedia = (q) => ({ matches: q === "(pointer: coarse)" });
  vm.createContext(g);
  p.scripts = [...html.matchAll(/<script src="\/([^"]+)"><\/script>/g)].map((m) => m[1]);
  for (const name of p.scripts) {
    try { vm.runInContext(read(name), g, { filename: name }); } catch (e) { p.errors.push(name + ": " + e.message); }
  }
  p.pcm = g.sctermPlayer ? g.sctermPlayer.pcm : null;
  p.open = () => { p.ws.readyState = 1; p.ws.onopen(); };
  // audio sends one PCM packet over the socket: 0x02, then the samples, in an
  // ArrayBuffer of the page's own realm (Net checks instanceof).
  const PageBuffer = vm.runInContext("ArrayBuffer", g);
  p.audio = (pcm) => {
    const msg = new PageBuffer(1 + pcm.byteLength);
    const bytes = new Uint8Array(msg);
    bytes[0] = 0x02;
    bytes.set(new Uint8Array(pcm), 1);
    p.ws.onmessage({ data: msg });
  };
  p.status = (msg) => p.ws.onmessage({ data: JSON.stringify(Object.assign({ op: "status" }, msg)) });
  p.chip = () => {
    const c = els.get("sound-chip");
    if (!c) return "(no chip)";
    return c.classList.contains("off") ? null : c.textContent;
  };
  p.tap = (type, extra) => w.fire(type, Object.assign({ pointerId: 1, clientX: 10, clientY: 10, button: 0 }, extra));
  return p;
}

// drain runs a ScriptProcessorNode's callback over fresh output blocks until
// `frames` non-silent frames came out (or `max` blocks), returning them.
function drain(sp, frames, max) {
  const got = [];
  for (let b = 0; b < max && got.length < frames; b++) {
    const l = new Float32Array(sp.bufferSize), r = new Float32Array(sp.bufferSize);
    sp.onaudioprocess({ outputBuffer: { length: sp.bufferSize, numberOfChannels: 2, getChannelData: (c) => (c ? r : l) } });
    for (let i = 0; i < l.length; i++) if (l[i] !== 0 || r[i] !== 0) got.push([l[i], r[i]]);
  }
  return got;
}

const expected = (seeds) => {
  const want = [];
  for (const s of seeds) {
    const v = new Int16Array(burst(s));
    for (let i = 0; i < FRAMES; i++) want.push([v[i * 2] / 32768, v[i * 2 + 1] / 32768]);
  }
  return want;
};
const same = (got, want) =>
  got.length === want.length && got.every(([l, r], i) => Math.abs(l - want[i][0]) < 1e-7 && Math.abs(r - want[i][1]) < 1e-7);

// ---------------------------------------------------------------------------
// 1. one start, however many callers
// ---------------------------------------------------------------------------
console.log("one start, however many callers");
{
  const releases = [];
  const w = world({ addModule: () => new Promise((r) => releases.push(r)) });
  check("the ring loads ahead of player.js as a classic script", w.ringError, undefined);
  const pcm = new w.Pcm();
  check("Pcm takes the server's packets through play()", typeof pcm.play, "function");
  let settled = 0;
  const calls = [pcm.start(), pcm.start(), pcm.start()];
  if (typeof pcm.play === "function") {
    pcm.play(burst(0));
    pcm.play(burst(1));
  }
  if (typeof pcm.unlock === "function") pcm.unlock();
  for (const c of calls) Promise.resolve(c).then(() => settled++);
  check("overlapping starts and packets build one AudioContext", w.made, 1);
  check("and the callers share one promise", calls[0] === calls[1] && calls[1] === calls[2], true);
  releases.forEach((r) => r());
  await tick();
  check("and one worklet node", w.worklets.length, 1);
  check("every caller is answered", settled, 3);
  check("and it is ready", pcm.ready, true);
  await pcm.start();
  if (typeof pcm.play === "function") pcm.play(burst(2));
  await tick();
  check("starting again later builds nothing", w.made, 1);
  check("never two contexts at once", w.maxLive, 1);
}

// ---------------------------------------------------------------------------
// 2. a failed start is closed, and only a retry from the chip or Alt+M tries
//    again: not the packets, and not every tap and key
// ---------------------------------------------------------------------------
console.log("a failed start");
// retry stands for the sound chip's click handler, or Alt+M's key: it calls
// unlock() inside that event.
const retry = (w, pcm, type, ev) => {
  w.listeners.push({ type, fn: () => pcm.unlock(), capture: false, once: true });
  w.fire(type, ev);
};
{
  // The first attempt cannot load the worklet module; later ones can.
  const w = world({ addModule: (n) => (n === 1 ? Promise.reject(new Error("404")) : Promise.resolve()) });
  const pcm = new w.Pcm();
  if (typeof pcm.changed === "function") pcm.changed();
  await pcm.start();
  await tick();
  check("it is reported", pcm.failed, true);
  check("the context it built is closed", [w.closed, w.live], [1, 0]);
  check("and let go", pcm.ctx, null);
  for (let i = 0; i < 20; i++) {
    if (typeof pcm.play === "function") pcm.play(burst(i));
    else pcm.start();
  }
  await tick();
  check("twenty packets later nothing was retried", w.made, 1);
  check("and no gesture is listened for: each tap and key would only build and close another context",
    unlocks(w, pcm), []);
  w.fire("pointerup", { pointerType: "touch" });
  w.fire("touchend", {});
  w.fire("click", {});
  w.fire("keydown", { key: "a", code: "KeyA" });
  await tick();
  check("so taps and keys build nothing", w.made, 1);
  retry(w, pcm, "click", {});
  check("the chip's retry tries again, inside its click", w.madeIn[1], "click");
  await tick();
  check("and this time the sound plays", [pcm.ready, pcm.failed, pcm.running], [true, false, true]);
  check("never two contexts at once", w.maxLive, 1);
}
{
  // The context itself cannot be created the first time.
  const w = world({ failNew: (n) => n === 1 });
  const pcm = new w.Pcm();
  if (typeof pcm.changed === "function") pcm.changed();
  await pcm.start();
  await tick();
  check("a context that cannot be created is a failed start too", [pcm.failed, pcm.ctx], [true, null]);
  w.fire("keydown", { key: "a", code: "KeyA", repeat: true });
  w.fire("keydown", { key: "a", code: "KeyA" });
  await tick();
  check("a key, held down or pressed, does not retry it", w.made, 1);
  retry(w, pcm, "keydown", { key: "m", code: "KeyM", altKey: true });
  check("Alt+M does, inside its key press", w.madeIn[1], "keydown");
  await tick();
  check("successfully", [pcm.ready, pcm.running], [true, true]);
}

// ---------------------------------------------------------------------------
// 3. gestures anywhere start the sound, then are no longer listened for
// ---------------------------------------------------------------------------
console.log("gestures start the sound");
{
  const w = world();
  const pcm = new w.Pcm();
  if (typeof pcm.changed === "function") pcm.changed();
  check("pointerdown, pointerup, touchend, click and keydown, in the capture phase",
    unlocks(w, pcm), ALL_UNLOCKS);
  if (typeof pcm.play === "function") pcm.play(burst(0));
  else pcm.start();
  await tick();
  check("audio arriving before any gesture builds the graph", pcm.ready, true);
  check("which waits, suspended", pcm.running, false);
  const resumes = w.resumes;
  w.fire("pointerdown", { pointerType: "touch" });
  check("a touch's pointerdown is left alone: it is not the gesture", w.resumes, resumes);
  await tick();
  check("and the sound still waits", pcm.running, false);
  w.fire("pointerup", { pointerType: "touch" });
  check("its pointerup resumes the context, inside the event", w.resumedIn.includes("pointerup"), true);
  await tick();
  check("and the sound plays", pcm.running, true);
  check("then nothing is listened for any more", unlocks(w, pcm), []);
  // The browser suspends it again: another app took the audio, the phone locked.
  if (pcm.ctx) pcm.ctx.set("interrupted");
  check("a context the browser suspended is listened for again", unlocks(w, pcm), ALL_UNLOCKS);
  w.fire("keydown", { key: "x", code: "KeyX" });
  await tick();
  check("and a key press resumes it", [pcm.running, unlocks(w, pcm)], [true, []]);
  check("never two contexts at once", w.maxLive, 1);
}
for (const [type, ev] of [
  ["click", {}],
  ["keydown", { key: "h", code: "KeyH", altKey: true }],
  ["touchend", {}],
  ["pointerdown", { pointerType: "mouse" }],
  ["pointerup", { pointerType: "pen" }],
]) {
  // Toolbar clicks and keys count as much as the canvas: each listed gesture,
  // on its own, resumes a waiting context.
  const w = world();
  const pcm = new w.Pcm();
  if (typeof pcm.changed === "function") pcm.changed();
  await pcm.start();
  await tick();
  const before = w.resumes;
  w.fire(type, ev);
  check(`a ${type}${ev.pointerType ? " (" + ev.pointerType + ")" : ""} resumes it`, w.resumes > before, true);
}
{
  // With nothing built yet, the gesture builds it -- inside the event.
  const w = world();
  const pcm = new w.Pcm();
  if (typeof pcm.changed === "function") pcm.changed();
  w.fire("touchend", {});
  check("a gesture before any audio creates the context and resumes it, inside the event",
    [w.madeIn, w.resumedIn.includes("touchend")], [["touchend"], true]);
  await tick();
  check("which then plays", pcm.running, true);
}
{
  // Input still calls pcm.resume() on every press of the canvas.
  const input = lift(src, "// Android keycodes", "// screenshot (Alt+S)") || "";
  check("a canvas press still resumes the sound", /addEventListener\("pointerdown"[^]*?pcm\.resume\(\)/.test(input), true);
  const w = world();
  const pcm = new w.Pcm();
  await pcm.start();
  await tick();
  w.active = true;
  for (const s of ["suspended", "interrupted"]) {
    pcm.ctx.state = s;
    const before = w.resumes;
    pcm.resume();
    check(`resume() resumes a ${s} context`, w.resumes, before + 1);
  }
  pcm.ctx.state = "running";
  const before = w.resumes;
  pcm.resume();
  check("and leaves a running one alone", w.resumes, before);
}

// ---------------------------------------------------------------------------
// 4. the AudioWorklet path, and no Web Audio at all
// ---------------------------------------------------------------------------
console.log("with AudioWorklet");
{
  const w = world();
  const pcm = new w.Pcm();
  await pcm.start();
  await tick();
  check("the worklet plays it", pcm.mode, "worklet");
  check("loaded from /pcm-worklet.js", w.modules, ["/pcm-worklet.js"]);
  const node = w.worklets[0];
  check("as one scterm-pcm node with stereo out",
    node ? [node.name, node.options.outputChannelCount] : null, ["scterm-pcm", [2]]);
  check("into the local gain, then the speakers",
    !!node && node.to[0] === pcm.gain && pcm.gain.to[0] === pcm.ctx.destination, true);
  if (typeof pcm.play === "function") pcm.play(burst(0));
  await tick();
  check("a context that is not running is sent nothing to pile up", w.posted.length, 0);
  w.active = true;
  pcm.resume();
  await tick();
  if (typeof pcm.play === "function") pcm.play(burst(1));
  await tick();
  check("once it runs, each packet goes to the worklet", w.posted.length, 1);
  check("transferred, not copied", !!w.posted[0] && w.posted[0].transfer[0] === w.posted[0].m.pcm, true);
  if (node) node.port.onmessage({ data: { stats: { underruns: 2, dropped: 1, queued: 960 } } });
  check("the worklet's stats are the page's", pcm.stats, { underruns: 2, dropped: 1, queued: 960 });
}
console.log("no Web Audio at all");
{
  const w = world({ noAudio: true });
  const pcm = new w.Pcm();
  if (typeof pcm.changed === "function") pcm.changed();
  let threw = null;
  try {
    if (typeof pcm.play === "function") pcm.play(burst(0));
    await pcm.start();
  } catch (e) { threw = String(e); }
  await tick();
  check("audio is ignored without an error", threw, null);
  check("and the mode says why", pcm.mode, "none");
  check("no gesture is listened for: there is nothing to start", unlocks(w, pcm), []);
}

// ---------------------------------------------------------------------------
// 5. no AudioWorklet (plain http): the compatibility path
// ---------------------------------------------------------------------------
console.log("without AudioWorklet: the compatibility path");
{
  const w = world({ worklet: false });
  const pcm = new w.Pcm();
  pcm.setLocalGain(0); // muted before any sound has arrived
  if (typeof pcm.changed === "function") pcm.changed();
  w.fire("pointerup", { pointerType: "touch" });
  await tick();
  check("the sound starts rather than failing", [pcm.ready, pcm.failed, pcm.running], [true, false, true]);
  check("in compatibility mode", pcm.mode, "compat");
  check("with no worklet to load", w.modules, []);
  const sp = w.sps[0];
  check("played by a ScriptProcessorNode", !!sp, true);
  check("with stereo out", sp && sp.outputs, 2);
  check("in blocks of 2048 or 4096 frames", !!sp && [2048, 4096].includes(sp.bufferSize), true);
  check("into the local gain, then the speakers",
    !!sp && sp.to[0] === pcm.gain && pcm.gain.to[0] === pcm.ctx.destination, true);
  check("muting before the sound started still holds", pcm.gain && pcm.gain.gain.value, 0);
  pcm.setLocalGain(0.5);
  check("and the local gain follows Alt+-/Alt+=", pcm.gain && pcm.gain.gain.value, 0.5);
  if (sp && typeof pcm.play === "function") {
    const seeds = [0, 7, 14, 21, 28, 35];
    for (const s of seeds) pcm.play(burst(s));
    await tick();
    check("nothing has underrun before it plays", pcm.stats.underruns, 0);
    const want = expected(seeds);
    const got = drain(sp, want.length, 10);
    check("the ScriptProcessor drains the ring: frames in equal frames out", got.length, want.length);
    check("unchanged and in order", same(got, want), true);
    drain(sp, 1, 1);
    check("running dry is counted as an underrun", pcm.stats.underruns > 0, true);
    check("and the stats are the ring's, for the HUD and the telemetry",
      [Object.keys(pcm.stats).sort(), pcm.stats.queued], [["dropped", "queued", "underruns"], 0]);
    check("window.sctermPlayer can tell which path plays", pcm.mode, "compat");
  }
}
{
  const w = world({ worklet: false, noWorkletNode: true });
  const pcm = new w.Pcm();
  if (typeof pcm.changed === "function") pcm.changed();
  w.fire("pointerup", { pointerType: "touch" });
  await tick();
  check("Firefox's plain-http page, with no AudioWorkletNode either, takes the same path",
    [pcm.running, pcm.mode, w.modules.length, w.sps.length], [true, "compat", 0, 1]);
}

// ---------------------------------------------------------------------------
// 5b. a context that runs at another rate than the 48 kHz stream
// ---------------------------------------------------------------------------
console.log("a context at another rate");
{
  const w = world({ rate: 48000 });
  const pcm = new w.Pcm();
  await pcm.start();
  await tick();
  check("asked for 48 kHz", w.ctxs[0] && w.ctxs[0].options.sampleRate, 48000);
  const opts = w.worklets[0] ? w.worklets[0].options.processorOptions : {};
  check("and running at it, the worklet plays the stream frame for frame",
    [pcm.rate, opts.ratio === undefined || opts.ratio === 1], [48000, true]);
}
{
  const w = world({ rate: 44100 });
  const pcm = new w.Pcm();
  await pcm.start();
  await tick();
  const opts = w.worklets[0] ? w.worklets[0].options.processorOptions : {};
  check("a context that runs at 44.1 kHz anyway is read back", pcm.rate, 44100);
  check("and the worklet resamples: 48000/44100 stream frames to each frame it plays", opts.ratio, 48000 / 44100);
  check("with the same ring otherwise", [opts.targetFrames, opts.maxFrames], [4608, 11520]);
}
{
  const w = world({ rate: 44100, worklet: false });
  const pcm = new w.Pcm();
  if (typeof pcm.changed === "function") pcm.changed();
  w.fire("pointerup", { pointerType: "touch" });
  await tick();
  const sp = w.sps[0];
  if (sp) {
    for (let s = 0; s < 10; s++) pcm.play(burst(s * 7));
    await tick();
    drain(sp, 2 * sp.bufferSize, 2);
    const used = 10 * FRAMES - pcm.stats.queued;
    check("so does the compatibility path: two blocks of 2048 frames at 44.1 kHz take 4458 frames of the stream",
      Math.abs(used - Math.round(2 * sp.bufferSize * 48000 / 44100)) <= 1, true);
  } else {
    check("so does the compatibility path", "no ScriptProcessorNode", "a ScriptProcessorNode");
  }
}

// ---------------------------------------------------------------------------
// 6. what the sound chip says
// ---------------------------------------------------------------------------
console.log("the sound chip");
{
  const chip = world().soundChip;
  check("player.js has the sound chip's rules", typeof chip === "function", true);
  if (chip) {
    const playing = { mode: "worklet", heard: true, running: true, muted: false, note: false, dismissed: false };
    const say = (o) => { const c = chip(Object.assign({}, playing, o)); return c && c.text; };
    check("sound playing: nothing to say", chip(playing), null);
    check("no audio arriving: nothing to say", say({ heard: false, running: false }), null);
    check("audio arriving, sound not started", say({ running: false }), "Tap for sound");
    check("and tapping it starts the sound", chip(Object.assign({}, playing, { running: false })).act, "unlock");
    check("muted here", say({ muted: true }), "Sound off · Alt+M");
    // Tapping the muted chip unmutes, and the tap itself starts the sound.
    check("muted outranks not started", say({ muted: true, running: false }), "Sound off · Alt+M");
    check("compatibility mode, while its note is due", say({ mode: "compat", note: true }), "Sound: compatibility mode");
    const note = chip(Object.assign({}, playing, { mode: "compat", note: true }));
    const tip = (note && note.title) || "";
    check("its tooltip says why: plain http, a little more delay, localhost or https for less",
      /plain http/i.test(tip) && /delay/.test(tip) && /localhost/.test(tip) && /https/.test(tip), true);
    check("the note goes when no longer due", say({ mode: "compat", note: false }), null);
    check("compatibility mode not started: tap first", say({ mode: "compat", note: true, running: false }), "Tap for sound");
    check("no Web Audio, audio arriving", say({ mode: "none", running: false }), "No sound in this browser");
    check("no Web Audio, no audio either: nothing to say", say({ mode: "none", running: false, heard: false }), null);
    check("no Web Audio, note dismissed", say({ mode: "none", running: false, dismissed: true }), null);
    const failed = chip(Object.assign({}, playing, { running: false, failed: true }));
    check("a start that failed: say so, with the chord that retries it",
      failed && [failed.text, failed.act, failed.keys], ["Sound failed · Alt+M", "retry", "Alt+M"]);
    check("muted outranks it", say({ running: false, failed: true, muted: true }), "Sound off · Alt+M");
    check("muted names its chord too", (chip(Object.assign({}, playing, { muted: true })) || {}).keys, "Alt+M");
    check("a session that has ended: nothing to say, whatever else",
      [say({ ended: true, running: false }), say({ ended: true, muted: true }), say({ ended: true, failed: true }),
        say({ ended: true, mode: "none" })], [null, null, null, null]);
  }
}

// ---------------------------------------------------------------------------
// 7. the HUD's sound readout is the device's volume, and says so
// ---------------------------------------------------------------------------
console.log("the HUD's device volume");
{
  let label = null;
  if (statusSrc) {
    const c = { console };
    vm.createContext(c);
    vm.runInContext(statusSrc + "\n;globalThis.__L = typeof audioLabel === \"function\" ? audioLabel : null;", c);
    label = c.__L;
  }
  check("player.js has the HUD's sound readout", typeof label === "function", true);
  if (label) {
    const on = { audio: "on", devVolOk: true, devVol: 47 };
    check("it names the device's volume", label(on, 0), "device vol 47%");
    check("the device muted", label(Object.assign({}, on, { devVol: 0 }), 0), "device vol muted");
    check("before the first reading", label(Object.assign({}, on, { devVolOk: false }), 0), "device vol —");
    check("no sound in the session", label({ audio: "off" }, 0), "audio off");
    check("the device turned the sound off: no volume, whatever the last reading",
      label(Object.assign({}, on, { audio: "off" }), 3), "audio off · u3");
    check("only on and off: never an error for the output of the computer running scterm",
      label({ audio: "error" }, 0), "audio off");
    check("this page's underruns follow", label(on, 3), "device vol 47% · u3");
  }
}

// ---------------------------------------------------------------------------
// 8. the whole page: scripts, chip, HUD and keys wired together
// ---------------------------------------------------------------------------
console.log("the page");
{
  const p = page();
  check("index.html loads the ring before the player",
    p.scripts.includes("pcm-worklet.js") && p.scripts.indexOf("pcm-worklet.js") < p.scripts.indexOf("player.js"), true);
  check("every script loads into one page without clashing", p.errors, []);
  check("and the page builds its action bar from the catalog it is served with",
    p.els.get("bar").children.filter((c) => c.tagName === "BUTTON").length > 0, true);
  check("the chip starts hidden", p.chip(), null);
  check("the page listens for the gestures that start the sound", unlocks(p.w, p.pcm), ALL_UNLOCKS);
  check("and has not started any sound yet", p.w.made, 0);
  p.open();
  p.audio(burst(0));
  await tick();
  check("audio before any gesture: the chip says so", p.chip(), "Tap for sound");
  p.timers.forEach((t) => t.fn && t.fn()); // the HUD and the bar fade out
  check("with the HUD hidden", p.els.get("hud").classList.contains("idle"), true);
  check("the chip stays", p.chip(), "Tap for sound");
  p.tap("pointerdown", { pointerType: "touch" });
  await tick();
  check("a touch's pointerdown does not start the sound", p.chip(), "Tap for sound");
  p.tap("pointerup", { pointerType: "touch" });
  await tick();
  check("its pointerup does, and the chip goes", [p.pcm && p.pcm.running, p.chip()], [true, null]);
  check("the page stops listening", unlocks(p.w, p.pcm), []);
  p.w.fire("keydown", { code: "KeyM", key: "m", altKey: true });
  check("Alt+M mutes this browser, and the chip says how to undo it", p.chip(), "Sound off · Alt+M");
  check("the toast still says so too", p.els.get("toast").textContent, "local audio muted");
  const chipEl = p.els.get("sound-chip");
  if (chipEl) chipEl.fire("click", {});
  check("tapping the chip turns the sound back on", [p.pcm && p.pcm.localGain, p.chip()], [1, null]);
  p.status({ audio: "on", devVolOk: true, devVol: 47, control: true });
  check("the HUD names the device's volume", p.els.get("audio").textContent, "device vol 47%");
  check("its placeholder too", /<span id="audio">device vol —<\/span>/.test(read("index.html")), true);
  check("never two contexts at once", p.w.maxLive, 1);
}
{
  const p = page();
  p.open();
  p.audio(burst(0));
  await tick();
  p.w.active = true; // the tap on the chip is the gesture
  const chipEl = p.els.get("sound-chip");
  if (chipEl) chipEl.fire("click", {});
  await tick();
  check("tapping Tap for sound starts it", [p.pcm && p.pcm.running, p.chip()], [true, null]);
}
console.log("the page over plain http (no AudioWorklet)");
{
  const p = page({ worklet: false });
  check("every script loads", p.errors, []);
  p.open();
  p.audio(burst(0));
  await tick();
  check("before a gesture: Tap for sound", p.chip(), "Tap for sound");
  const n = p.timers.length;
  p.tap("pointerup", { pointerType: "touch" });
  await tick();
  check("after it, a short note: compatibility mode", p.chip(), "Sound: compatibility mode");
  const chipEl = p.els.get("sound-chip");
  const tip = chipEl ? chipEl.title : "";
  check("whose tooltip explains it",
    /plain http/i.test(tip) && /delay/.test(tip) && /localhost/.test(tip) && /https/.test(tip), true);
  const fade = p.timers.slice(n).find((t) => t.fn && t.ms >= 2000 && t.ms <= 10000);
  check("it fades after a few seconds", !!fade, true);
  if (fade) fade.fn();
  check("and is gone", p.chip(), null);
  const sp = p.w.sps[0];
  if (sp) {
    const seeds = [1, 2, 3, 4, 5, 6];
    for (const s of seeds) p.audio(burst(s));
    await tick();
    const want = expected(seeds);
    const got = drain(sp, want.length, 10);
    check("the socket's packets play through the ScriptProcessor, and only those sent while it ran",
      [got.length, same(got, want)], [want.length, true]);
    drain(sp, 1, 1);
    p.status({ audio: "on", devVolOk: true, devVol: 47, control: true });
    check("its underruns reach the HUD", /^device vol 47% · u[1-9]/.test(p.els.get("audio").textContent), true);
  }
}
{
  const p = page({ worklet: false });
  p.open();
  p.audio(burst(0));
  p.tap("pointerup", { pointerType: "touch" });
  await tick();
  const chipEl = p.els.get("sound-chip");
  const tip = chipEl ? chipEl.title : "(no chip)";
  if (chipEl) chipEl.fire("click", {});
  check("tapping the note shows the explanation (a phone has no tooltips)", p.els.get("toast").textContent, tip);
  check("and puts the note away", p.chip(), null);
}
console.log("the page without its catalog");
{
  // index.html opened as a file, not served: no catalog block at all.
  const warned = [];
  const p = page({ unserved: true, console: Object.assign({}, console, { warn: (m) => warned.push(m) }) });
  check("still loads", p.errors, []);
  check("says what is missing", warned, ["scterm: the page has no action catalog"]);
  check("with no action bar", p.els.get("bar").children.length, 0);
  check("and still connects", !!p.ws, true);
}
console.log("the page in a browser without Web Audio");
{
  const p = page({ noAudio: true });
  check("every script loads", p.errors, []);
  p.open();
  p.audio(burst(0));
  await tick();
  check("says there is no sound here", p.chip(), "No sound in this browser");
  const chipEl = p.els.get("sound-chip");
  if (chipEl) chipEl.fire("click", {});
  check("until dismissed", p.chip(), null);
}

// clickOn clicks one of the page's elements, as the event being handled:
// what the page starts in it starts inside the click.
const clickOn = (p, el) => {
  p.w.event = "click";
  if (el) el.fire("click", {});
  p.w.event = "";
};
console.log("the page when the sound will not start");
{
  // Two starts fail to load the worklet; the third works.
  const p = page({ addModule: (n) => (n <= 2 ? Promise.reject(new Error("404")) : Promise.resolve()) });
  p.open();
  p.audio(burst(0));
  await tick();
  const chipEl = p.els.get("sound-chip");
  check("the chip says the sound failed, and names the chord that retries it",
    [p.chip(), chipEl && chipEl["aria-keyshortcuts"]], ["Sound failed · Alt+M", "Alt+M"]);
  check("no gesture is listened for", unlocks(p.w, p.pcm), []);
  p.tap("pointerdown", { pointerType: "touch" });
  p.tap("pointerup", { pointerType: "touch" });
  p.w.fire("touchend", {});
  p.w.fire("click", {});
  p.w.fire("keydown", { code: "KeyA", key: "a" });
  p.w.fire("keydown", { code: "Enter", key: "Enter" });
  await tick();
  check("so taps, clicks and keys no longer build and close a context each", p.w.made, 1);
  p.w.active = true;
  clickOn(p, chipEl);
  await tick();
  check("a tap on the chip tries again, inside the tap (and fails again here)", [p.w.made, p.w.madeIn[1], p.chip()],
    [2, "click", "Sound failed · Alt+M"]);
  p.w.fire("keydown", { code: "KeyM", key: "m", altKey: true });
  await tick();
  check("Alt+M tries again, inside its key press, rather than muting what never played",
    [p.w.made, p.w.madeIn[2], p.pcm.localGain], [3, "keydown", 1]);
  check("and this time the sound plays, and the chip goes", [p.pcm.running, p.chip()], [true, null]);
  check("never two contexts at once", p.w.maxLive, 1);
}
{
  // Alt+M mutes before any sound has come, and its key press is the gesture
  // whose start then fails: muted outranks failed on the chip.
  const p = page({ addModule: () => Promise.reject(new Error("404")) });
  p.open();
  const altM = () => p.w.fire("keydown", { code: "KeyM", key: "m", altKey: true });
  altM();
  p.audio(burst(0));
  await tick();
  check("muted, and then the sound failed to start: the chip says Sound off",
    [p.pcm.failed, p.pcm.localGain, p.w.made, p.chip()], [true, 0, 1, "Sound off · Alt+M"]);
  altM();
  await tick();
  check("Alt+M unmutes, as the chip says, rather than retrying: no new context, and the chip now says Sound failed",
    [p.pcm.localGain, p.w.made, p.chip()], [1, 1, "Sound failed · Alt+M"]);
  altM();
  await tick();
  check("so the next Alt+M retries, inside its key press", [p.w.made, p.w.madeIn[1], p.pcm.localGain], [2, "keydown", 1]);
}
console.log("a chip that stays");
{
  const p = page();
  p.open();
  p.audio(burst(0));
  p.tap("pointerup", { pointerType: "touch" });
  await tick();
  const chipEl = p.els.get("sound-chip");
  const sound = p.els.get("sound");
  const quiet = () => !!chipEl && chipEl.classList.contains("quiet");
  const n = p.timers.length;
  p.w.fire("keydown", { code: "KeyM", key: "m", altKey: true });
  check("Alt+M: the chip says how to undo it, and names that chord for assistive technology",
    [p.chip(), chipEl && chipEl["aria-keyshortcuts"]], ["Sound off · Alt+M", "Alt+M"]);
  check("it shows for a while even without the HUD", quiet(), false);
  p.timers.slice(n).forEach((t) => t.fn && t.ms === 2500 && t.fn());
  check("(the HUD has faded, with its #sound flag)", !!sound && sound.classList.contains("idle"), true);
  p.timers.slice(n).forEach((t) => t.fn && t.ms === 6000 && t.fn());
  check("then it is quiet: shown only along with the HUD, so it does not sit over the picture's corner " +
    "taking taps meant for the device", [p.chip(), quiet()], ["Sound off · Alt+M", true]);
  const css = read("player.css");
  check("which player.css hides, and lets taps through, while the HUD is hidden",
    /#sound\.idle #sound-chip\.quiet \{[^}]*opacity: 0;[^}]*pointer-events: none;/.test(css), true);
  p.w.fire("keydown", { code: "KeyX", key: "x" });
  check("a key brings the HUD up, and the chip with it", !!sound && sound.classList.contains("idle"), false);
  if (chipEl) chipEl.fire("click", {});
  check("where a tap on it still unmutes", [p.pcm.localGain, p.chip()], [1, null]);
}
{
  const p = page();
  p.open();
  p.audio(burst(0));
  p.tap("pointerup", { pointerType: "touch" });
  await tick();
  const chipEl = p.els.get("sound-chip");
  const quiet = () => !!chipEl && chipEl.classList.contains("quiet");
  const altM = () => p.w.fire("keydown", { code: "KeyM", key: "m", altKey: true });
  const first = p.timers.length;
  altM();
  const second = p.timers.length;
  altM();
  altM();
  p.timers.slice(first, second).forEach((t) => t.fn && t.ms === 6000 && t.fn());
  check("words that go and come back within 6 s (Sound off, nothing, Sound off) are not made quiet by the first " +
    "words' timer", [p.chip(), quiet()], ["Sound off · Alt+M", false]);
  p.timers.slice(second).forEach((t) => t.fn && t.ms === 6000 && t.fn());
  check("but 6 s after they came back", [p.chip(), quiet()], ["Sound off · Alt+M", true]);
}
{
  const p = page();
  p.open();
  p.audio(burst(0));
  await tick();
  const chipEl = p.els.get("sound-chip");
  p.timers.forEach((t) => t.fn && t.fn());
  check("while Tap for sound, which any tap takes away, is never quiet",
    [p.chip(), !!chipEl && chipEl.classList.contains("quiet")], ["Tap for sound", false]);
}
{
  const p = page({ noAudio: true });
  p.open();
  p.audio(burst(0));
  await tick();
  const chipEl = p.els.get("sound-chip");
  p.timers.forEach((t) => t.fn && t.ms === 6000 && t.fn());
  check("No sound in this browser is quiet after a while too: nothing has to dismiss it",
    [p.chip(), !!chipEl && chipEl.classList.contains("quiet")], ["No sound in this browser", true]);
}
console.log("the page's sound when the session ends");
{
  const bye = (p) => p.ws.onmessage({ data: JSON.stringify({ op: "bye", reason: "scterm stopped." }) });
  const p = page();
  p.open();
  p.audio(burst(0));
  await tick();
  check("Tap for sound, before any gesture", p.chip(), "Tap for sound");
  bye(p);
  await tick();
  check("after a bye the chip does not ask for a tap: there is nothing to hear", p.chip(), null);
  check("and nothing is listened for", unlocks(p.w, p.pcm), []);
  const resumes = p.w.resumes;
  p.w.fire("keydown", { code: "KeyA", key: "a" });
  p.tap("pointerup", { pointerType: "touch" });
  await tick();
  check("so a key or a tap starts nothing", [p.w.resumes - resumes, p.pcm.running], [0, false]);
  // pressPicture is a mouse press on the canvas itself, whose own listener
  // asks the context to resume (the first tap is the gesture).
  const pressPicture = (x) => {
    x.w.event = "pointerdown";
    x.els.get("screen").fire("pointerdown", { pointerId: 2, pointerType: "mouse", button: 0, clientX: 10, clientY: 10 });
    x.tap("pointerdown", { pointerId: 2, pointerType: "mouse" });
    x.w.event = "";
  };
  pressPicture(p);
  await tick();
  check("nor a click on the picture, which asks the context itself", [p.w.resumes - resumes, p.pcm.running], [0, false]);
  const q = page();
  q.open();
  q.audio(burst(0));
  q.tap("pointerup", { pointerType: "touch" });
  await tick();
  check("sound playing", q.pcm.running, true);
  bye(q);
  await tick();
  check("a bye suspends it", [q.w.suspends, q.pcm.running, q.chip(), unlocks(q.w, q.pcm)], [1, false, null, []]);
  const resumed = q.w.resumes;
  pressPicture(q);
  await tick();
  check("and a click on the last frame does not resume it", [q.w.resumes - resumed, q.pcm.running], [0, false]);
  clickOn(q, q.els.get("conn-retry"));
  check("and Reconnect, a click, resumes it inside the click", q.w.resumedIn[q.w.resumedIn.length - 1], "click");
  await tick();
  check("so it plays again with the new session", q.pcm.running, true);
}
console.log("the page in a browser without the Fullscreen API");
{
  const p = page();
  const full = p.els.get("sheet-full");
  check("there is no Full screen button: not in the bar, nor in the sheet",
    [p.els.get("bar").children.some((c) => c.textContent === "Full screen"), !full || full.hidden], [false, true]);
}
console.log("the page on a phone");
{
  const p = page({ coarse: true });
  const idle = (id) => p.els.get(id).classList.contains("idle");
  check("a coarse pointer at load: the HUD, the bar and a quiet chip start down, the bar inert, and ⋯ shows",
    [p.errors, idle("hud"), idle("bar"), p.els.get("bar").inert, idle("sound"), p.els.get("bar-btn").hidden],
    [[], true, true, true, true, false]);
  p.open();
  p.audio(burst(0));
  await tick();
  check("while Tap for sound shows without them", p.chip(), "Tap for sound");
}

// ---------------------------------------------------------------------------
// 9. markup and style
// ---------------------------------------------------------------------------
console.log("markup and style");
{
  const html = read("index.html");
  const css = read("player.css");
  const box = /<div id="sound"([^>]*)>([^]*?)<\/div>/.exec(html);
  check("index.html has the sound chip", box !== null, true);
  check("announced politely as a status",
    box !== null && /role="status"/.test(box[1]) && /aria-live="polite"/.test(box[1]), true);
  check("and it is a real button", box !== null && /<button id="sound-chip" type="button"/.test(box[2]), true);
  check("no inline styles", /\sstyle=/.test(html), false);
  const rule = /#sound \{([^}]*)\}/.exec(css);
  check("its box takes no pointer events", rule !== null && /pointer-events: none/.test(rule[1]), true);
  check("the chip itself does", /#sound-chip \{[^}]*pointer-events: auto/.test(css), true);
  check("but not while hidden", /#sound-chip\.off \{[^}]*visibility: hidden/.test(css), true);
  check("in the top right corner, away from the menu button and the centred card",
    rule !== null && /top:/.test(rule[1]) && /right:/.test(rule[1]) && !/left:/.test(rule[1]) && !/50%/.test(rule[1]), true);
  // A rule in the reduced-motion block that turns transitions off for the
  // chip, by name or for every element.
  const reduced = /@media \(prefers-reduced-motion: reduce\) \{([^]*?)\n\}/.exec(css);
  const still = (sel) => !!reduced && [...reduced[1].matchAll(/([^{}]+)\{([^}]*)\}/g)].some(([, sels, body]) =>
    /transition: none/.test(body) && sels.split(",").map((s) => s.trim()).some((s) => s === "*" || s === sel));
  check("its motion respects prefers-reduced-motion", still("#sound-chip"), true);
  check("the old one-shot pointerdown start is gone",
    /addEventListener\("pointerdown", \(\) => pcm\.start\(\)/.test(src), false);
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
