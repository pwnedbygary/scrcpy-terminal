// scterm web/--window player.
//
// Talks the binary protocol documented in web.go: 0x01 video (16-byte header +
// JPEG or raw BGR0), 0x02 audio (PCM s16le stereo 48 kHz), 0x03 hello JSON.
// Control events go back as small JSON objects.

"use strict";

// ---------------------------------------------------------------------------
// tiny helpers
// ---------------------------------------------------------------------------

const els = {
  canvas: document.getElementById("screen"),
  hud: document.getElementById("hud"),
  device: document.getElementById("device"),
  geom: document.getElementById("geom"),
  fps: document.getElementById("fps"),
  audio: document.getElementById("audio"),
  state: document.getElementById("state"),
  toast: document.getElementById("toast"),
  ime: document.getElementById("ime"),
  conn: document.getElementById("conn"),
  connText: document.getElementById("conn-text"),
  connDetail: document.getElementById("conn-detail"),
  connRetry: document.getElementById("conn-retry"),
  soundChip: document.getElementById("sound-chip"),
};

const MSG_VIDEO = 0x01;
const MSG_AUDIO = 0x02;
const MSG_HELLO = 0x03;
const VIDEO_HEADER = 16;

const FMT_JPEG = 0;
const FMT_RAW = 1;

// TOAST_FADE_MS is the toast's fade-out in player.css. Once it has faded the
// toast is emptied: it is a status region, and a line left in it would still
// be there for a screen reader to come across.
const TOAST_FADE_MS = 200;

let toastTimer = 0;
function toast(msg, ms) {
  els.toast.textContent = msg;
  els.toast.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    els.toast.classList.remove("show");
    toastTimer = setTimeout(() => { els.toast.textContent = ""; }, TOAST_FADE_MS);
  }, ms || 1600);
}

function setState(text, cls) {
  els.state.textContent = text;
  els.state.className = cls || "";
}

// ---------------------------------------------------------------------------
// the action catalog
//
// protocol/fixtures/actions.json, the one definition of the app-level actions
// (actions.go). The server puts it into index.html as a JSON block, and this
// page builds its Alt+letter and F-key tables, its action bar and its controls
// sheet from it, so none of them is a copy that could disagree with the
// terminal's.
// ---------------------------------------------------------------------------

const CATALOG = readCatalog(document.getElementById("actions"));

// readCatalog parses the catalog block. A page without one (index.html opened
// as a file rather than served) gets an empty catalog: it still shows the
// device and takes pointer and typed input, with no action bar or chords.
function readCatalog(el) {
  try {
    const c = JSON.parse(el.textContent);
    if (Array.isArray(c.device) && Array.isArray(c.local) && Array.isArray(c.views.webBar)) return c;
  } catch (e) { /* missing or malformed: the empty catalog below */ }
  console.warn("scterm: the page has no action catalog");
  return { device: [], local: [], views: { webBar: [] } };
}

// ---------------------------------------------------------------------------
// connection
// ---------------------------------------------------------------------------

class Net {
  constructor() {
    this.ws = null;
    this.backoff = 250;
    this.handlers = {};
    this.connected = false;
    // onOpen runs on EVERY successful connect, so the page can (re)negotiate.
    // A message sent while the socket is still CONNECTING is queued instead of
    // dropped: the hello used to be sent at script load, one line after
    // connect(), and was therefore thrown away on every single page load --
    // which is why the browser never got audio (the server only feeds PCM to a
    // client that asked for it with caps:"audio").
    this.onOpen = null;
    this.onClose = null;
    // ended: the server said "bye", so the session is over and a closed socket
    // stays closed until reconnect() -- retrying would only find it gone.
    this.ended = false;
    this.queue = [];
  }

  on(op, fn) { this.handlers[op] = fn; }

  connect() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = proto + "//" + location.host + "/ws";
    let ws;
    try {
      ws = new WebSocket(url);
    } catch (e) {
      setTimeout(() => this.connect(), this.backoff);
      return;
    }
    ws.binaryType = "arraybuffer";
    this.ws = ws;

    ws.onopen = () => {
      this.connected = true;
      this.backoff = 250;
      setState("connected", "ok");
      // Negotiate first (the hello carries the viewport and caps:"audio"), then
      // anything that was produced while the socket was still opening.
      if (this.onOpen) this.onOpen();
      const queued = this.queue;
      this.queue = [];
      for (const msg of queued) this.send(msg);
    };
    ws.onmessage = (ev) => this.dispatch(ev);
    ws.onclose = () => {
      this.connected = false;
      if (this.onClose) this.onClose();
      if (this.ended) {
        setState("session ended", "warn");
        return;
      }
      setState("reconnecting…", "warn");
      const wait = this.backoff;
      this.backoff = Math.min(this.backoff * 2, 4000);
      setTimeout(() => this.connect(), wait);
    };
    ws.onerror = () => { /* onclose follows */ };
  }

  dispatch(ev) {
    if (typeof ev.data === "string") {
      let msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      if (msg.op === "bye") this.ended = true;
      const fn = this.handlers[msg.op];
      if (fn) fn(msg);
      return;
    }
    const buf = ev.data;
    if (!(buf instanceof ArrayBuffer) || buf.byteLength < 1) return;
    const view = new DataView(buf);
    const kind = view.getUint8(0);
    if (kind === MSG_VIDEO) {
      if (buf.byteLength <= VIDEO_HEADER) return;
      const fn = this.handlers.video;
      if (fn) {
        fn({
          seq: view.getUint32(1),
          w: view.getUint16(5),
          h: view.getUint16(7),
          fps: view.getUint16(9) / 10,
          format: view.getUint8(11),
          body: buf.slice(VIDEO_HEADER),
        });
      }
    } else if (kind === MSG_AUDIO) {
      const fn = this.handlers.audio;
      if (fn) fn(buf.slice(1));
    }
  }

  send(obj) {
    const ws = this.ws;
    if (!ws) return false;
    // Still opening: hold it, do not lose it. Anything later than OPEN (closing,
    // closed) is genuinely unsendable and reported as such.
    if (ws.readyState === WebSocket.CONNECTING) {
      this.queue.push(obj);
      return true;
    }
    if (ws.readyState !== WebSocket.OPEN) return false;
    ws.send(JSON.stringify(obj));
    return true;
  }

  // reconnect restarts the retry loop that a bye stopped. A socket that is
  // still closing reconnects from its own onclose.
  reconnect() {
    this.ended = false;
    this.backoff = 250;
    if (!this.ws || this.ws.readyState === WebSocket.CLOSED) this.connect();
  }
}

// ---------------------------------------------------------------------------
// view: canvas geometry, video drawing, pointer mapping
// ---------------------------------------------------------------------------

class View {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext("2d", { alpha: false, desynchronized: true });
    this.decoding = false;
    this.pending = null;
    this.drawn = 0;    // frames actually painted (not just received)
    this.decodeMs = 0; // rolling average JPEG decode+draw cost
    this.lastDraw = 0;
    this.fps = 0;
    this.errors = 0;   // frames that could not be decoded
    this.errorAt = -Infinity; // when the last of them failed
    this.fitW = 0;     // element size last applied by fit()
    this.fitH = 0;
  }

  // viewport in device pixels, capped so one frame stays a sane size on 4K
  // displays with devicePixelRatio > 1
  viewport() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const w = Math.max(2, Math.min(Math.floor(window.innerWidth * dpr), 4096));
    const h = Math.max(2, Math.min(Math.floor(window.innerHeight * dpr), 4096));
    return { w, h };
  }

  // Size the canvas ELEMENT to the largest box that fits the window at the
  // picture's own aspect ratio. The bitmap is always the capture's resolution,
  // so a window of that same shape shows it 1:1 with no bars, and any other
  // shape is scaled down or up to fit with black on the shorter side. Both axes
  // take the same scale factor, which is what stops the image being squashed.
  //
  // Doing it here rather than in CSS keeps the element's rect equal to the drawn
  // picture: pointer mapping measures that rect, so a CSS-side letterbox
  // (object-fit) would leave the two out of step and offset every touch by the
  // size of the bars.
  fit() {
    const iw = this.canvas.width, ih = this.canvas.height;
    const vw = window.innerWidth, vh = window.innerHeight;
    if (!iw || !ih || vw <= 0 || vh <= 0) return;
    const scale = Math.min(vw / iw, vh / ih);
    const w = Math.max(1, Math.round(iw * scale));
    const h = Math.max(1, Math.round(ih * scale));
    if (this.fitW === w && this.fitH === h) return; // no layout thrash per frame
    this.fitW = w;
    this.fitH = h;
    this.canvas.style.width = w + "px";
    this.canvas.style.height = h + "px";
  }

  push(frame) {
    if (this.decoding) {
      this.pending = frame; // newest wins; the older frame is dropped
      return;
    }
    this.render(frame);
  }

  render(frame) {
    this.decoding = true;
    const done = () => {
      this.decoding = false;
      const next = this.pending;
      this.pending = null;
      if (next) this.render(next);
    };

    const finish = (source, w, h) => {
      try {
        this.draw(source, w, h);
      } finally {
        if (source && source.close && source !== this.canvas) {
          try { source.close(); } catch (e) { /* ignore */ }
        }
        done();
      }
    };

    if (frame.format === FMT_RAW) {
      const w = frame.w, h = frame.h;
      const img = new ImageData(new Uint8ClampedArray(frame.body), w, h);
      if (this.canvas.width !== w || this.canvas.height !== h) {
        this.canvas.width = w;
        this.canvas.height = h;
        this.fit(); // the picture's shape changed: refit the element
      }
      this.ctx.putImageData(img, 0, 0);
      this.noteDraw();
      done();
      return;
    }

    // JPEG: decode off the main thread, then blit.
    const t0 = performance.now();
    createImageBitmap(new Blob([frame.body], { type: "image/jpeg" }))
      .then((bmp) => {
        finish(bmp, frame.w, frame.h);
        const dt = performance.now() - t0;
        this.decodeMs = this.decodeMs ? this.decodeMs * 0.8 + dt * 0.2 : dt;
      })
      .catch(() => {
        this.errors++;
        this.errorAt = performance.now();
        done();
      });
  }

  draw(bmp, w, h) {
    if (this.canvas.width !== w || this.canvas.height !== h) {
      this.canvas.width = w;
      this.canvas.height = h;
      this.fit(); // the picture's shape changed: refit the element
    }
    this.ctx.drawImage(bmp, 0, 0, w, h);
    this.noteDraw();
  }

  noteDraw() {
    const now = performance.now();
    if (this.lastDraw) {
      const dt = now - this.lastDraw;
      if (dt > 0) this.fps = this.fps ? this.fps * 0.85 + (1000 / dt) * 0.15 : 1000 / dt;
    }
    this.lastDraw = now;
    this.drawn++;
  }

  videoSize() {
    return { w: this.canvas.width, h: this.canvas.height };
  }

  // norm maps a pointer event to 0..65535 fractions of the *device* frame,
  // which is what the server multiplies back into video coordinates. Using
  // the CSS rect (not the canvas bitmap) means letterboxing and HiDPI are
  // handled by the browser's own layout.
  norm(ev) {
    const r = this.canvas.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) return { x: 0, y: 0 };
    let fx = (ev.clientX - r.left) / r.width;
    let fy = (ev.clientY - r.top) / r.height;
    fx = Math.min(Math.max(fx, 0), 1);
    fy = Math.min(Math.max(fy, 0), 1);
    return { x: Math.round(fx * 65535), y: Math.round(fy * 65535) };
  }
}

// ---------------------------------------------------------------------------
// audio: raw PCM into the ring buffer of web/pcm-worklet.js, played by an
// AudioWorklet or, on a page without one, a ScriptProcessorNode; and what the
// sound chip says about it. web/audio_test.mjs lifts this section, up to the
// input section, and runs it on its own.
// ---------------------------------------------------------------------------

class Pcm {
  constructor() {
    this.ctx = null;
    this.node = null;
    this.gain = null;
    this.ring = null;     // the compatibility path's ring (a worklet holds its own)
    this.ready = false;
    this.starting = null; // the start in flight, shared by every caller
    this.failed = false;  // the last start failed: only a gesture tries again
    // How the sound plays: "worklet", "compat" (no AudioWorklet, so a
    // ScriptProcessorNode drains the same ring, with a little more delay) or
    // "none" (no Web Audio at all); "" until the first start.
    this.mode = "";
    this.heard = false;   // PCM has arrived from the server
    this.armed = false;   // the gestures that can start the sound are listened for
    this.unlocker = (ev) => this.unlock(ev);
    this.onchange = null; // the page redraws the sound chip
    // Local playback gain (Alt+M / Alt+- / Alt+=). Kept here rather than only on
    // the node because the node is created asynchronously, on the first audio
    // packet -- muting before any sound has arrived still has to stick.
    this.localGain = 1;
    this.stats = { underruns: 0, dropped: 0, queued: 0 };
  }

  setLocalGain(v) {
    this.localGain = Math.max(0, Math.min(2, v));
    if (this.gain) this.gain.gain.value = this.localGain;
    this.changed();
  }

  get running() {
    return !!this.ctx && this.ctx.state === "running";
  }

  // play takes one packet of PCM from the server. start() goes first so that
  // the chip, redrawn when the first packet arrives, already knows the mode.
  play(buf) {
    this.start().then(() => this.push(buf));
    if (!this.heard) {
      this.heard = true;
      this.changed();
    }
  }

  // start builds the audio graph once. Every caller shares the attempt in
  // flight, so the packets that each call it cannot build two contexts, and
  // after a failure only a gesture (unlock) tries again, never the packets.
  start(retry) {
    if (!this.ready && !this.starting && this.mode !== "none" && (retry || !this.failed)) {
      const done = () => {
        this.starting = null;
        this.changed();
      };
      this.starting = this.build();
      this.starting.then(done, done);
    }
    return this.starting || Promise.resolve();
  }

  async build() {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) {
      this.mode = "none";
      return;
    }
    let ctx = null;
    try {
      ctx = new AC({ sampleRate: 48000, latencyHint: "interactive" });
      this.ctx = ctx;
      this.failed = false;
      ctx.onstatechange = () => this.changed();
      // Before the first await: a gesture that called this is still being
      // handled here, and some browsers start sound only inside one.
      this.resume();
      const gain = ctx.createGain();
      gain.gain.value = this.localGain;
      let node;
      if (ctx.audioWorklet && typeof AudioWorkletNode === "function") {
        this.mode = "worklet";
        await ctx.audioWorklet.addModule("/pcm-worklet.js");
        node = new AudioWorkletNode(ctx, "scterm-pcm", {
          numberOfInputs: 0,
          numberOfOutputs: 1,
          outputChannelCount: [2],
          processorOptions: PCM_RING,
        });
        node.port.onmessage = (e) => {
          if (e.data && e.data.stats) this.stats = e.data.stats;
        };
      } else {
        // The same ring, which index.html loads as a classic script, drained
        // on the main thread one COMPAT_FRAMES block at a time.
        this.mode = "compat";
        const ring = new SctermPCM({ processorOptions: PCM_RING });
        node = ctx.createScriptProcessor(COMPAT_FRAMES, 0, 2);
        node.onaudioprocess = (e) => {
          const out = e.outputBuffer;
          ring.pull(out.getChannelData(0), out.getChannelData(1));
          this.stats = ring.stats();
        };
        this.ring = ring;
      }
      node.connect(gain);
      gain.connect(ctx.destination);
      this.node = node;
      this.gain = gain;
      this.ready = true;
    } catch (e) {
      // Close what was built: a later gesture starts again from nothing, and
      // no context is ever left open beside the next one.
      this.failed = true;
      this.ctx = null;
      this.node = null;
      this.gain = null;
      this.ring = null;
      if (ctx) {
        ctx.onstatechange = null;
        try { ctx.close().catch(() => {}); } catch (err) { /* already closed */ }
      }
    }
  }

  resume() {
    const ctx = this.ctx;
    if (ctx && ctx.state !== "running" && ctx.state !== "closed") {
      ctx.resume().catch(() => {});
    }
  }

  // unlock is a gesture's chance to start the sound: it resumes the context,
  // or builds the graph if there is none (yet, or since a failed attempt). A
  // touch's pointerdown is skipped, because the browser counts only the end of
  // a touch as the gesture, and so is a key repeating while held down.
  unlock(ev) {
    if (ev && ev.type === "pointerdown" && ev.pointerType !== "mouse") return;
    if (ev && ev.repeat) return;
    if (this.ctx) this.resume();
    else this.start(true);
  }

  // armUnlock starts or stops listening for the gestures that can start the
  // sound. Capture phase on the window, so a toolbar button or a key counts as
  // much as the canvas, whatever handles the event after.
  armUnlock(on) {
    if (on === this.armed) return;
    this.armed = on;
    for (const type of UNLOCK_EVENTS) {
      if (on) window.addEventListener(type, this.unlocker, true);
      else window.removeEventListener(type, this.unlocker, true);
    }
  }

  // changed runs after anything that can change the sound's state. The gesture
  // listeners stay only while the sound is not playing (a browser can suspend
  // it again later), and the page redraws the sound chip.
  changed() {
    this.armUnlock(!this.running && this.mode !== "none");
    if (this.onchange) this.onchange();
  }

  push(buf) {
    // A context that is not running plays nothing, and audio queued meanwhile
    // would only come out late, ahead of everything after it.
    if (!this.ready || !this.running) return;
    if (this.ring) {
      this.ring.feed({ pcm: buf });
      return;
    }
    // Transfer the underlying buffer: no copy on the main thread.
    this.node.port.postMessage({ pcm: buf }, [buf]);
  }
}

// PCM_RING sizes the ring both paths play from, in frames at 48 kHz: playback
// starts once it holds 96 ms, and it never holds more than 240 ms.
const PCM_RING = { targetFrames: 4608, maxFrames: 11520 };

// COMPAT_FRAMES is the ScriptProcessorNode's block: ~43 ms at 48 kHz, small
// enough that the 96 ms the ring starts with still covers a late burst
// between two blocks.
const COMPAT_FRAMES = 2048;

// UNLOCK_EVENTS are the gestures that let a page start sound. Per the HTML
// spec that is a key press, a mouse press or the end of a touch; a touch's
// pointerdown is not one, so a phone needs the pointerup or touchend. A click
// comes after one of them and covers a toolbar button.
const UNLOCK_EVENTS = ["pointerdown", "pointerup", "touchend", "click", "keydown"];

// COMPAT_NOTE explains the compatibility path: the chip's tooltip, and a toast
// when the chip is tapped (a phone shows no tooltips).
const COMPAT_NOTE = "Plain http uses an older audio path,\n" +
  "with a little more delay. localhost\n" +
  "or https gives the low-delay path.";

// soundChip is what the sound chip says about this browser's own sound, or
// null when there is nothing to act on. `s` has Pcm's mode, heard and running,
// plus muted (the local gain is 0), note (the compatibility note is still due)
// and dismissed (the viewer closed the no-sound note). `act` is what tapping
// the chip does.
function soundChip(s) {
  if (s.mode === "none") {
    return s.heard && !s.dismissed ? { text: "No sound in this browser", act: "dismiss" } : null;
  }
  if (s.muted) return { text: "Sound off · Alt+M", act: "unmute" };
  if (s.heard && !s.running) return { text: "Tap for sound", act: "unlock" };
  if (s.mode === "compat" && s.heard && s.running && s.note) {
    return { text: "Sound: compatibility mode", title: COMPAT_NOTE, act: "explain" };
  }
  return null;
}

// ---------------------------------------------------------------------------
// input: pointer, wheel, keyboard, paste
// ---------------------------------------------------------------------------

// Android keycodes for the keys a desktop keyboard has that Android knows.
const KEYCODES = {
  Enter: 66, NumpadEnter: 66, Backspace: 67, Tab: 61, Escape: 111,
  ArrowUp: 19, ArrowDown: 20, ArrowLeft: 21, ArrowRight: 22,
  Home: 122, End: 123, PageUp: 92, PageDown: 93, Delete: 112, Insert: 124,
  ShiftLeft: 59, ShiftRight: 60, ControlLeft: 113, ControlRight: 114,
  AltLeft: 57, AltRight: 58, MetaLeft: 117, MetaRight: 118,
  CapsLock: 115, NumLock: 143, ScrollLock: 116, PrintScreen: 120,
  ContextMenu: 82,
  F1: 131, F2: 132, F3: 133, F4: 134, F5: 135, F6: 136,
  F7: 137, F8: 138, F9: 139, F10: 140, F11: 141, F12: 142,
  NumpadAdd: 81, NumpadSubtract: 69, NumpadMultiply: 17, NumpadDivide: 70,
  NumpadDecimal: 158, NumpadComma: 159, Numpad0: 144, Numpad1: 145,
  Numpad2: 146, Numpad3: 147, Numpad4: 148, Numpad5: 149, Numpad6: 150,
  Numpad7: 151, Numpad8: 152, Numpad9: 153,
  AudioVolumeUp: 24, AudioVolumeDown: 25, AudioVolumeMute: 164,
  BrowserBack: 4, BrowserForward: 125, BrowserHome: 3,
  MediaPlayPause: 85, MediaStop: 86, MediaTrackNext: 87, MediaTrackPrevious: 88,
  Power: 26, WakeUp: 224, Sleep: 223,
};

// Keys that never travel on their own. Modifiers reach the device as the meta
// state of the key they modify, so a bare Shift, Ctrl, Alt or Meta tap sends
// nothing. Lock keys are not forwarded at all: browsers report a tap as one
// event on macOS and two elsewhere, so the device's lock state is left alone.
const BARE_MODIFIER_KEYS = new Set([
  "ShiftLeft", "ShiftRight", "ControlLeft", "ControlRight",
  "AltLeft", "AltRight", "MetaLeft", "MetaRight",
  "CapsLock", "NumLock", "ScrollLock",
]);

// Ctrl+A..Z -> Android letter keycodes 29..54, as handleByte() sends them.
const LETTER_KEYCODES = {};
for (let i = 0; i < 26; i++) {
  LETTER_KEYCODES["Key" + String.fromCharCode(65 + i)] = 29 + i;
}

// Accelerators that belong to the BROWSER, not the page: with grab off they are
// left alone, because closing the window ends a --window session. Grab (F12,
// the terminal's own key) hands them to the device instead.
const CTRL_BROWSER_KEYS = new Set(["KeyW", "KeyT", "KeyN", "KeyR", "KeyQ"]);

// Alt+<key> local controls (Alt is what the TUI uses, so both modes agree).
// F-keys are handled through KEYCODES below, except F4: Alt+F4 is the browser's
// own window-close, so device power also has Alt+P.
const ALT_ACTIONS = {
  Escape: "back", ArrowLeft: "back",
  ArrowUp: "dpad_up", ArrowDown: "dpad_down", ArrowRight: "dpad_right",
  // Alt+M / Alt+- / Alt+= are the terminal's LOCAL playback controls; here the
  // local output is the browser's own gain.
  Minus: "quieter", Equal: "louder",
};

// Phone soft keyboards report key events differently from a desktop keyboard:
// `code` is very often empty and only `key` carries anything, so a table lookup
// by code silently misses every hotkey. Normalise the character back to its
// US-layout code name.
//
// Punctuation that is part of a chord (Alt+/ opens the controls sheet, Alt+- /
// Alt+= are the local volume) must be normalised too, or the chord silently
// degrades to typing the character: on a phone, `code` is empty and Alt+/
// typed "/" at the device instead of opening the sheet. Text that is not a
// chord still goes through the text path (the name lookup simply misses).
const PUNCT_CODES = {
  "/": "Slash", "-": "Minus", "=": "Equal", ".": "Period", ",": "Comma",
  ";": "Semicolon", "'": "Quote", "`": "Backquote",
  "[": "BracketLeft", "]": "BracketRight", "\\": "Backslash",
};

function codeOf(ev) {
  if (ev.code) return ev.code;
  const k = ev.key;
  if (!k || k === "Unidentified") return "";
  if (k.length === 1) {
    if (k >= "a" && k <= "z") return "Key" + k.toUpperCase();
    if (k >= "A" && k <= "Z") return "Key" + k;
    if (k >= "0" && k <= "9") return "Digit" + k;
    return PUNCT_CODES[k] || "";
  }
  return k; // "Enter", "Backspace", "ArrowLeft", ...
}

// The app-level keys come from the action catalog, as the terminal's do
// (actions.go appMnemonicOps and appFKeyOps), so a key does the same thing in
// a terminal, a browser tab and --window. The values are catalog action ids:
// the server performs the device actions from the same catalog.
//
// MNEMONIC_ACTIONS: Alt+<key> runs the action whose mnemonic that key is. It is
// the primary binding on a phone keyboard (Unexpected Keyboard has Ctrl and
// Alt, but no F-key row). Keys are code names made the way codeOf() makes them
// from a phone keyboard's bare character, so Alt+/ opens the controls sheet
// there too; the ☰ button remains the always-available path. Alt+M is not a
// mnemonic: it stays the LOCAL mute, and the device's own mute has no
// Alt+letter chord (F7, the action bar and the controls sheet carry it).
//
// APP_KEYS: F1..F12 run the action with that F-key, as in the terminal
// (input.go fKeyMap + appkeys.go mapAppFKey). ALT_FKEYS: Alt+F1..F12 are the
// same actions, because a bare F-key is the terminal's binding but an OS, a
// window manager or a phone keyboard may never deliver one.
//
// web_keymap_test.go runs this derivation against the catalog the binary
// embeds and compares the result with the terminal's tables.
function catalogActions(catalog) {
  return catalog.device.concat(catalog.local);
}

function keyTables(catalog) {
  const mnemonics = {};
  const fkeys = {};
  for (const a of catalogActions(catalog)) {
    const code = a.mnemonic ? codeOf({ key: a.mnemonic }) : "";
    if (code) mnemonics[code] = a.id;
    if (a.fkey) fkeys["F" + a.fkey] = a.id;
  }
  return { mnemonics, fkeys };
}

const KEY_TABLES = keyTables(CATALOG);
const MNEMONIC_ACTIONS = KEY_TABLES.mnemonics;
const APP_KEYS = KEY_TABLES.fkeys;
const ALT_FKEYS = APP_KEYS;

// Three-finger tap tuning. A finger that stays put is held back from the device
// for SOFTKEY_GRACE_MS (or until it moves past SOFTKEY_HOLD_SLOP_PX, or lifts),
// so that a three-finger tap cannot also land on the device as a stray tap.
// The cost is bounded and small: a drag loses one frame, and a press loses at
// most the grace window -- a tap that lifts sooner is replayed the moment it
// lifts, so nothing waits longer than it has to.
const SOFTKEY_GRACE_MS = 60;
const SOFTKEY_HOLD_SLOP_PX = 10; // movement that proves a drag, releasing the touch
const SOFTKEY_TAP_SLOP_PX = 24; // drift still allowed inside a three-finger tap
const SOFTKEY_FINGERS = 3; // fingers in a keyboard-toggle tap
const SOFTKEY_TAP_MS = 700; // longest such tap that still counts

class Input {
  constructor(net, view) {
    this.net = net;
    this.view = view;
    this.down = false;
    this.btn = 0;
    this.moved = false;
    this.movePending = false;
    this.lastPos = { x: 0, y: 0 };
    this.wheelAcc = 0;
    // grab: hand the keys the browser would otherwise take (Ctrl+W/T/N/R/Q)
    // to the device -- the same idea as F12 in the terminal, where grab stops
    // Zellij from eating them. The pointer is always grabbed here: there is no
    // terminal to give it back to.
    this.grab = false;
    // Multi-finger state. `pointers` is the fingers currently on the canvas,
    // `held` a device touch not sent yet, `fingers` the most seen down at once
    // and `drift` the furthest any of them travelled -- both only for deciding
    // whether the gesture was a tap.
    this.pointers = new Map();
    this.held = null;
    this.fingers = 0;
    this.drift = 0;
    this.tapAt = 0;
    this.softKeys = false;
    this.kbRetry = false; // a pointerdown retry is armed for the IME
    this.printScreenDown = false;
    // The pointer type of the last press on the canvas: older browsers send
    // contextmenu without one.
    this.pointerType = "";
    // onAct, when set, hears every action before it runs: the toolbar drops a
    // Quit that is waiting for its second press. onPress hears every press of
    // a key or of the picture (pressed), which drops it too.
    this.onAct = null;
    this.onPress = null;
    // consumed is the keys, still held down, whose press closed the sheet or
    // pressed one of the page's buttons: key() drops their auto-repeats.
    this.consumed = new Set();
  }

  // pressed tells onPress of a press: any key but a bare modifier, a press of
  // the picture or a notch of the wheel over it, typed or pasted text. As any
  // press but the Quit's own does in the terminal, it drops a Quit waiting for
  // its second.
  pressed() {
    if (this.onPress) this.onPress();
  }

  sendPos(op, extra) {
    const p = this.lastPos;
    const msg = Object.assign({ op: op, x: p.x, y: p.y }, extra || {});
    this.net.send(msg);
  }

  move(ev) {
    this.lastPos = this.view.norm(ev);
    if (!this.down || this.movePending) return;
    this.movePending = true;
    requestAnimationFrame(() => {
      this.movePending = false;
      if (this.down) this.sendPos("move");
    });
  }

  // Throw away a touch that was never sent. Used when a second finger proves
  // the gesture was not a device touch after all.
  dropHeld() {
    if (!this.held) return;
    clearTimeout(this.held.timer);
    this.held = null;
  }

  // Send a held touch now: the grace window expired, or the finger turned the
  // gesture into a drag. The down goes to where the finger first landed so the
  // device sees the true start of the path.
  flushHeld() {
    if (!this.held) return;
    clearTimeout(this.held.timer);
    const pos = this.held.pos;
    this.held = null;
    this.down = true;
    this.net.send({ op: "down", x: pos.x, y: pos.y });
  }

  // Three-finger tap: raise the phone's own keyboard. A phone has no physical
  // keyboard, so Alt+<key> hotkeys are otherwise unreachable from one. Focus
  // has to happen inside the gesture handler or the phone refuses to show the
  // keyboard at all, so this must stay synchronous.
  toggleSoftKeys() {
    if (!els.ime) return;
    this.softKeys = !this.softKeys;
    if (!this.softKeys) {
      els.ime.blur();
      toast("keyboard off");
      return;
    }
    els.ime.value = "";
    els.ime.focus({ preventScroll: true });
    if (document.activeElement !== els.ime) {
      // The browser refused focus outside a touch gesture. This is what a phone
      // does for Alt+I (a keydown is not a gesture for the IME, even though it
      // is one for audio), so stay "on" and retry on the next tap rather than
      // flipping back to off: the user asked for the keyboard, not for an
      // error. The toolbar's Keys button works because it is a real click.
      this.armSoftKeyRetry();
      toast("keyboard on — tap the screen once to raise it", 2600);
      return;
    }
    toast("keyboard on — type to send to the device", 2600);
  }

  // armSoftKeyRetry re-focuses the IME sink inside the next pointer gesture,
  // which is the only context a phone browser accepts for showing the IME.
  armSoftKeyRetry() {
    if (this.kbRetry) return;
    this.kbRetry = true;
    const retry = () => {
      this.kbRetry = false;
      if (!this.softKeys || !els.ime) return;
      els.ime.value = "";
      els.ime.focus({ preventScroll: true });
    };
    window.addEventListener("pointerdown", retry, { once: true, capture: true });
  }

  attach() {
    const c = els.canvas;

    c.addEventListener("pointerdown", (ev) => {
      ev.preventDefault();
      c.setPointerCapture(ev.pointerId);
      // Audio can only start from a gesture; the first tap is the gesture.
      pcm.resume();
      this.pointerType = ev.pointerType;
      this.pressed();

      if (this.pointers.size === 0) {
        // First finger of a new gesture.
        this.fingers = 0;
        this.drift = 0;
        this.tapAt = performance.now();
      }
      this.pointers.set(ev.pointerId, { x: ev.clientX, y: ev.clientY });
      this.fingers = Math.max(this.fingers, this.pointers.size);

      this.btn = ev.button;
      this.lastPos = this.view.norm(ev);

      if (this.pointers.size > 1) {
        // A second finger makes this a multi-finger gesture rather than a
        // device touch: drop whatever the first finger was holding back and
        // send nothing, so extra fingers never reach the device as a stray
        // touch (they used to send a second "down" at the new position).
        this.dropHeld();
        return;
      }
      // Middle click = Home, like the TUI.
      if (ev.button === 1) {
        this.act("home");
        return;
      }
      if (ev.button !== 0) return;

      // Hold the touch back for a moment. A three-finger tap needs its fingers
      // to land together, and without this the first finger would already have
      // tapped the device by the time the third one arrived.
      this.held = {
        pos: this.lastPos,
        timer: setTimeout(() => this.flushHeld(), SOFTKEY_GRACE_MS),
      };
    });

    c.addEventListener("pointermove", (ev) => {
      ev.preventDefault();
      this.moved = true;

      const from = this.pointers.get(ev.pointerId);
      if (from) {
        const d = Math.hypot(ev.clientX - from.x, ev.clientY - from.y);
        if (d > this.drift) this.drift = d;
      }

      if (this.held) {
        // Jitter inside the grace window is still a tap; real movement means a
        // drag, which must not wait a moment longer than it has to.
        if (this.drift <= SOFTKEY_HOLD_SLOP_PX) return;
        this.flushHeld();
      }
      this.move(ev);
    });

    const release = (ev) => {
      // pointerup is followed by lostpointercapture for the same pointer; only
      // the first of the two may run, or a tap would be counted twice.
      if (!this.pointers.has(ev.pointerId)) return;
      const wasHeld = this.held;
      this.pointers.delete(ev.pointerId);
      this.lastPos = this.view.norm(ev);
      this.dropHeld();

      if (this.down) {
        // A touch already on the device always ends with the first finger up.
        this.down = false;
        this.sendPos("up");
      } else if (wasHeld && this.pointers.size === 0 && this.fingers === 1) {
        // A plain tap that was held back. The finger is already up and no
        // other finger ever joined it, so replay it now -- waiting out the
        // rest of the grace window would only delay a gesture that is over.
        this.net.send({ op: "down", x: wasHeld.pos.x, y: wasHeld.pos.y });
        this.net.send({ op: "up", x: this.lastPos.x, y: this.lastPos.y });
      }

      if (this.pointers.size > 0) return;

      // Last finger up: was this a three-finger tap?
      const tapMs = performance.now() - this.tapAt;
      if (this.fingers >= SOFTKEY_FINGERS &&
          tapMs <= SOFTKEY_TAP_MS &&
          this.drift <= SOFTKEY_TAP_SLOP_PX) {
        this.toggleSoftKeys();
      }
      this.fingers = 0;
      this.drift = 0;
    };
    c.addEventListener("pointerup", (ev) => {
      ev.preventDefault();
      release(ev);
    });
    c.addEventListener("pointercancel", (ev) => {
      release(ev);
    });
    c.addEventListener("lostpointercapture", (ev) => {
      release(ev);
    });

    c.addEventListener("contextmenu", (ev) => {
      ev.preventDefault();
      // Right click = Android back, like the TUI. A long press of a finger or
      // a pen raises contextmenu too, and must not press Back.
      if ((ev.pointerType || this.pointerType) !== "mouse") return;
      this.act("back");
    });

    c.addEventListener("wheel", (ev) => {
      ev.preventDefault();
      this.lastPos = this.view.norm(ev);
      this.wheelAcc += -ev.deltaY;
      const notch = 50;
      if (Math.abs(this.wheelAcc) >= notch) {
        const notches = Math.trunc(this.wheelAcc / notch);
        this.wheelAcc -= notches * notch;
        this.pressed();
        this.sendPos("wheel", { delta: notches });
      }
    }, { passive: false });

    document.addEventListener("paste", (ev) => {
      const text = (ev.clipboardData || window.clipboardData).getData("text");
      if (text) {
        ev.preventDefault();
        this.pressed();
        this.net.send({ op: "text", text: text });
        toast("pasted " + text.length + " chars");
      }
    });

    // Text produced by the phone's own keyboard. A soft keyboard rarely emits
    // usable keydown codes -- Gboard and friends report key "Unidentified" and
    // an empty code, and deliver the characters through the input/composition
    // events instead -- so the composed text is read off the hidden field. It
    // is emptied on every event so it never accumulates and Backspace keeps
    // arriving as a key event rather than editing the field.
    const drain = () => {
      const text = els.ime.value;
      if (!text) return;
      els.ime.value = "";
      this.pressed();
      this.net.send({ op: "text", text: text });
    };
    els.ime.addEventListener("input", drain);
    els.ime.addEventListener("compositionend", drain);
    els.ime.addEventListener("keydown", (ev) => {
      // Enter must press the device's Enter, never submit anything here.
      if (ev.key === "Enter") ev.preventDefault();
    });

    window.addEventListener("keydown", (ev) => this.key(ev, true));
    window.addEventListener("keyup", (ev) => this.key(ev, false));
  }

  key(ev, down) {
    // `name` is the physical key, or the nearest equivalent a phone keyboard
    // offers: soft keyboards routinely leave `code` empty, which used to make
    // every hotkey below unreachable from one.
    const name = codeOf(ev);
    // A key whose press closed the sheet (Esc) or pressed one of the page's
    // buttons (Enter, Space) has done its work: its auto-repeats are dropped,
    // or they would reach the device once the sheet has closed or the session
    // is back. Its key-up ends that, and so does a fresh press, in case the
    // key-up went elsewhere.
    if (ev.repeat && this.consumed.has(name)) {
      ev.preventDefault();
      return;
    }
    this.consumed.delete(name);
    // While the controls sheet is open it takes the keys that work a dialog:
    // Esc closes it, Tab and Shift+Tab go round its buttons (so the focus stays
    // inside; and they are presses, which drop a Quit waiting for its second),
    // and Enter or Space on one of them is left to the browser, which presses
    // it once: a held key's repeats would press it again, and a held Enter
    // would arm Quit and then confirm it. Other keys are handled below.
    const press = ev.key === "Enter" || ev.key === " ";
    if (toolbar.sheetOpen) {
      if (ev.key === "Escape") {
        ev.preventDefault();
        if (down) {
          this.consumed.add(name);
          toolbar.hideSheet();
        }
        return;
      }
      if (ev.key === "Tab" && !ev.altKey && !ev.ctrlKey && !ev.metaKey) {
        ev.preventDefault();
        if (down) {
          this.pressed();
          toolbar.cycle(ev.shiftKey ? -1 : 1);
        }
        return;
      }
      if (press && toolbar.owns(ev.target)) {
        if (ev.repeat) ev.preventDefault();
        else if (down) this.consumed.add(name);
        return;
      }
    }
    // After a bye there is no device to send keys to, so the browser keeps them
    // all: Tab moves between the page's buttons, and Enter or Space presses the
    // focused one, once.
    if (this.net.ended) {
      if (press && ev.repeat) ev.preventDefault();
      else if (press && down) this.consumed.add(name);
      return;
    }
    if (BARE_MODIFIER_KEYS.has(name)) {
      ev.preventDefault();
      return;
    }
    if (down) this.pressed();

    // F1..F12 mean exactly what they mean in the terminal (input.go's fKeyMap +
    // mapAppFKey). They used to be sent to Android as the F1..F12 keycodes
    // instead, so the same key did two different things depending on whether
    // you were looking at the TUI or the window.
    const appOp = !ev.altKey && !ev.ctrlKey && !ev.metaKey ? APP_KEYS[name] : null;
    if (appOp) {
      // preventDefault on both edges: F5/F11 would otherwise reload the page
      // and toggle fullscreen while also being volume-down and collapse.
      ev.preventDefault();
      if (down) this.act(appOp);
      return;
    }
    if (ev.altKey && !ev.ctrlKey && ALT_FKEYS[name]) {
      // Alt+F1..F12: the same actions, for keyboards/browsers where a bare F-key
      // never arrives (or the WM owns it).
      ev.preventDefault();
      if (down) this.act(ALT_FKEYS[name]);
      return;
    }

    // Alt combos are local controls: they mirror the TUI keys exactly.
    if (ev.altKey && !ev.ctrlKey) {
      // The mnemonic table is consulted FIRST, so the aliases in ALT_ACTIONS
      // (Alt+H home, Alt+B back, Alt+R rotate...) can never disagree with it:
      // both tables name the same action and the shared one wins.
      const mnemonic = MNEMONIC_ACTIONS[name];
      if (mnemonic) {
        ev.preventDefault();
        if (down) this.act(mnemonic);
        return;
      }
      // Alt+M is the one letter chord the mnemonic table does not carry (it is
      // LOCAL mute), so it needs an explicit case. codeOf() normalises a phone
      // keyboard's bare "m" to KeyM, so both desk and phone reach it here.
      if (name === "KeyM" && down) {
        ev.preventDefault();
        this.localMute();
        return;
      }
      // ALT_ACTIONS is the fallback for the keys MNEMONIC_ACTIONS does not
      // cover (Escape, arrows, the volume punctuation). Any entry that could
      // shadow a mnemonic stays out of it, so the two tables cannot disagree.
      const action = ALT_ACTIONS[name];
      if (action) {
        ev.preventDefault();
        if (!down) return;
        if (action === "screenshot") { screenshot(); return; }
        if (action === "dpad_up") { this.net.send({ op: "key", code: 19 }); return; }
        if (action === "dpad_down") { this.net.send({ op: "key", code: 20 }); return; }
        if (action === "dpad_right") { this.net.send({ op: "key", code: 22 }); return; }
        if (action === "louder") { this.localVolume(+10); return; }
        if (action === "quieter") { this.localVolume(-10); return; }
        if (action === "localmute") { this.localMute(); return; }
        this.act(action);
        return;
      }
      const altCode = KEYCODES[name];
      if (altCode) {
        ev.preventDefault();
        this.sendKey(altCode, down, ev);
        return;
      }
      // Alt+letter with no mapping: types the character.
      if (down && ev.key && ev.key.length === 1) {
        ev.preventDefault();
        this.net.send({ op: "text", text: ev.key });
      }
      return;
    }

    if (ev.ctrlKey && !ev.altKey) {
      // Ctrl+<key> goes to Android as a metastate, like the TUI's raw bytes.
      if (name === "KeyV") { ev.preventDefault(); return; } // paste event follows
      if (name === "KeyC") return; // let the browser copy the page
      // Keys the BROWSER also wants. The terminal's answer to this is F12/grab:
      // with it on, Zellij stops eating the key and it reaches the device. The
      // browser equivalent is the reload/close/tab accelerators and the quit
      // accelerator, so those stay with the browser until grab is on.
      if (!this.grab && CTRL_BROWSER_KEYS.has(name)) return;
      if (name === "KeyQ") { // TUI: Ctrl-Q quits
        ev.preventDefault();
        if (down) this.net.send({ op: "quit" });
        return;
      }
      if (LETTER_KEYCODES[name]) {
        // Ctrl+A..Z -> letter keycode + AMETA_CTRL_ON, as handleByte does.
        ev.preventDefault();
        this.sendKey(LETTER_KEYCODES[name], down, ev);
        return;
      }
      const ctrlCode = KEYCODES[name];
      if (ctrlCode) {
        ev.preventDefault();
        this.sendKey(ctrlCode, down, ev);
        return;
      }
      return;
    }

    // Esc alone is Back, exactly as in the terminal (input.go: "\x1b" -> back).
    if (name === "Escape") {
      ev.preventDefault();
      if (down) this.act("back");
      return;
    }

    const code = KEYCODES[name];
    if (code) {
      ev.preventDefault();
      this.sendKey(code, down, ev);
      return;
    }

    // Printable text: send the code points, not the keycodes, so punctuation
    // and non-ASCII survive IMEs and layouts. Space/letters/digits still go
    // through as key events on the server side (games need keycodes).
    //
    // Text that belongs to an IME composition is left alone: it arrives on the
    // input event instead, and taking it here as well would type it twice.
    if (down && !ev.isComposing && ev.key && ev.key.length === 1 &&
        !ev.ctrlKey && !ev.metaKey) {
      ev.preventDefault();
      this.net.send({ op: "text", text: ev.key });
    }
  }

  // sendKey presses `code` once per keydown. The server turns every key op into
  // a full down+up pair, so the key-up sends nothing, and a held key's
  // auto-repeats are forwarded as further presses: the device has already seen
  // the key released and cannot repeat it itself. PrintScreen is the
  // exception: Windows delivers only its key-up, so a key-up that no key-down
  // preceded is the press. It goes without modifiers, so a host chord such as
  // Alt+PrintScreen reaches the device as a plain PrintScreen.
  sendKey(code, down, ev) {
    if (code === KEYCODES.PrintScreen) {
      const sawDown = this.printScreenDown;
      this.printScreenDown = down;
      if (!down) {
        if (!sawDown) this.net.send({ op: "key", code: code, meta: 0 });
        return;
      }
    } else if (!down) {
      return;
    }
    let meta = 0;
    if (ev.shiftKey) meta |= 0x01;
    if (ev.altKey) meta |= 0x02;
    if (ev.ctrlKey) meta |= 0x1000;
    if (ev.metaKey) meta |= 0x10000; // AMETA_META_ON
    this.net.send({ op: "key", code: code, meta: meta });
  }

  // act runs one catalog action. Device actions are just an op for the server,
  // which performs them from the same catalog as the terminal, and so is quit
  // (it ends the session); the few local ones (grab, keyboard, toolbar,
  // screenshot) are handled here. The action bar and the controls sheet call
  // this too, so a button, a row and a chord are the same call.
  act(op) {
    if (this.onAct) this.onAct(op);
    switch (op) {
    case "grab":
      this.toggleGrab();
      return;
    case "keyboard":
      this.toggleSoftKeys();
      return;
    case "toolbar":
      // The controls sheet. NOT "menu": that op is the device's Menu key (F2).
      toolbar.toggleSheet();
      return;
    case "screenshot":
      screenshot();
      return;
    }
    this.net.send({ op: op });
  }

  // The toast must say what key() does with CTRL_BROWSER_KEYS in each state.
  toggleGrab() {
    this.grab = !this.grab;
    toast(this.grab
      ? "grab on: Ctrl+W/T/N/R go to the device,\nCtrl+Q ends the session"
      : "grab off: Ctrl+W/T/N/R/Q stay with the browser");
  }

  // localVolume / localMute are the terminal's Alt+- Alt+= Alt+M: they change
  // the LOCAL output (in the TUI the host sink, here the browser's own gain),
  // never the device's volume -- that is what the volume keys are for.
  localVolume(delta) {
    const pct = Math.round((pcm.localGain + delta / 100) * 100);
    pcm.setLocalGain(Math.max(0, Math.min(200, pct)) / 100);
    toast("local audio " + Math.round(pcm.localGain * 100) + "%");
  }

  localMute() {
    if (pcm.localGain > 0) {
      this.lastLocalGain = pcm.localGain;
      pcm.setLocalGain(0);
      toast("local audio muted");
      return;
    }
    pcm.setLocalGain(this.lastLocalGain || 1);
    toast("local audio " + Math.round(pcm.localGain * 100) + "%");
  }
}

// ---------------------------------------------------------------------------
// screenshot (Alt+S) - the canvas is never tainted, it only ever gets our own
// JPEG blobs, so toBlob works
// ---------------------------------------------------------------------------

function screenshot() {
  els.canvas.toBlob((blob) => {
    if (!blob) return;
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    const d = new Date();
    const pad = (n) => String(n).padStart(2, "0");
    a.href = url;
    a.download = "scterm-" + d.getFullYear() + pad(d.getMonth() + 1) + pad(d.getDate()) +
      "-" + pad(d.getHours()) + pad(d.getMinutes()) + pad(d.getSeconds()) + ".png";
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 5000);
    toast("screenshot saved");
  }, "image/png");
}

// ---------------------------------------------------------------------------
// action bar + controls sheet
//
// Both are built from the action catalog and run actions through Input.act, so
// a click and its keyboard chord are literally the same call. The bar is the
// no-F-key, no-chord path to every action: one button per webBar entry. The
// sheet lists every action as a row that runs it, beside its chords.
// ---------------------------------------------------------------------------

// BAR_GROUPS are the webBar ids that start a new group of buttons in the bar.
const BAR_GROUPS = ["volup", "rotate", "power"];

// QUIT_CONFIRM_MS is how long a pressed Quit button or row waits for the
// second press that quits.
const QUIT_CONFIRM_MS = 2000;

// chords lists the keys that run an action: Alt+<mnemonic>, then its F-key.
function chords(a) {
  const keys = [];
  if (a.mnemonic) keys.push("Alt+" + a.mnemonic.toUpperCase());
  if (a.fkey) keys.push("F" + a.fkey);
  return keys;
}

// describe is an action's tooltip: its label, then its chords, as in
// "Volume up (Alt+U, F6)".
function describe(a) {
  const keys = chords(a);
  return keys.length ? a.label + " (" + keys.join(", ") + ")" : a.label;
}

// shortcuts gives el the action's chords as its aria-keyshortcuts, written
// the way that attribute wants them, as in "Alt+U F6".
function shortcuts(el, a) {
  const keys = chords(a);
  if (keys.length) el.setAttribute("aria-keyshortcuts", keys.join(" "));
}

function kbd(text) {
  const k = document.createElement("kbd");
  k.textContent = text;
  return k;
}

class Toolbar {
  constructor(input, catalog) {
    this.input = input;
    this.bar = document.getElementById("bar");
    this.sheet = document.getElementById("sheet");
    this.close = document.getElementById("sheet-close");
    this.menuBtn = document.getElementById("menu-btn");
    this.timers = new WeakMap();
    this.actions = new Map(); // bar button or sheet row -> its catalog action
    this.labels = new Map();  // bar button or sheet row -> the element with its text
    this.buttons = [];        // the bar, in webBar order
    this.rows = [];           // the sheet's action rows
    this.toggles = [];        // ☰ and the bar's Controls button, which open the sheet
    this.opener = null;       // what had the focus before the sheet opened
    this.quitArmed = null;    // the Quit button or row waiting for its second press
    this.quitText = "";
    this.quitTitle = "";
    this.quitUntil = 0;
    this.quitTimer = 0;
    input.onAct = input.onPress = () => this.disarmQuit();

    const byId = new Map(catalogActions(catalog).map((a) => [a.id, a]));
    const barIds = catalog.views.webBar.filter((id) => byId.has(id));
    if (this.bar) {
      for (const id of barIds) {
        if (BAR_GROUPS.includes(id) && this.buttons.length) {
          const sep = document.createElement("span");
          sep.className = "bar-sep";
          this.bar.appendChild(sep);
        }
        const btn = this.control(byId.get(id));
        btn.textContent = byId.get(id).short;
        btn.title = describe(byId.get(id));
        if (id === "toolbar") {
          // Like ☰, it opens the sheet, a dialog, and says whether it is open.
          btn.setAttribute("aria-haspopup", "dialog");
          btn.setAttribute("aria-controls", "sheet");
          btn.setAttribute("aria-expanded", "false");
          this.toggles.push(btn);
        }
        this.labels.set(btn, btn);
        btn.addEventListener("click", () => this.press(btn));
        this.bar.appendChild(btn);
        this.buttons.push(btn);
      }
    }

    // The sheet's rows: the bar's order, then any action the bar leaves out,
    // except the sheet's own (its chord is shown beside the title instead). A
    // row shows the label and the chords, so it has no tooltip repeating them,
    // and the chords are hidden from assistive technology, which has them as
    // the row's keyshortcuts: the row's name is its label.
    const box = document.getElementById("sheet-actions");
    const ids = barIds.concat([...byId.keys()].filter((id) => !barIds.includes(id)));
    for (const id of ids) {
      if (!box || id === "toolbar") continue;
      const a = byId.get(id);
      const row = this.control(a);
      row.className = "sheet-row";
      const label = document.createElement("span");
      label.className = "sheet-label";
      label.textContent = a.label;
      const keys = document.createElement("span");
      keys.className = "sheet-keys";
      keys.setAttribute("aria-hidden", "true");
      for (const k of chords(a)) keys.appendChild(kbd(k));
      row.appendChild(label);
      row.appendChild(keys);
      this.labels.set(row, label);
      row.addEventListener("click", () => this.run(row));
      box.appendChild(row);
      this.rows.push(row);
    }

    const self = byId.get("toolbar");
    if (self) {
      const title = document.getElementById("sheet-title");
      const keys = document.getElementById("sheet-keys");
      if (title) title.textContent = self.label;
      if (keys) for (const k of chords(self)) keys.appendChild(kbd(k));
      if (this.menuBtn) {
        this.menuBtn.title = describe(self);
        this.menuBtn.setAttribute("aria-label", self.label);
        shortcuts(this.menuBtn, self);
      }
    }
    if (this.menuBtn) {
      this.toggles.push(this.menuBtn);
      this.menuBtn.addEventListener("click", () => this.input.act("toolbar"));
    }
    if (this.close) this.close.addEventListener("click", () => this.hideSheet());
    if (this.sheet) {
      // The sheet starts faded; keep it out of the tab order until opened.
      this.sheet.inert = true;
      this.sheet.addEventListener("click", (ev) => {
        if (ev.target === this.sheet) this.hideSheet(); // click the backdrop
      });
    }
  }

  // control is the button for one action, in the bar or the sheet: named by
  // the text it shows, with the action's chords as its keyshortcuts.
  control(a) {
    const b = document.createElement("button");
    b.type = "button";
    b.dataset.act = a.id;
    shortcuts(b, a);
    this.actions.set(b, a);
    return b;
  }

  // press runs a bar button's action and flashes it, so a tap has visible
  // feedback even when the device response is off-screen.
  press(btn) {
    const a = this.actions.get(btn);
    if (!this.confirm(btn, a)) return;
    this.input.act(a.id);
    btn.classList.add("done");
    clearTimeout(this.timers.get(btn));
    this.timers.set(btn, setTimeout(() => btn.classList.remove("done"), 350));
  }

  // run runs a sheet row's action. The sheet closes first, as the terminal's
  // action menu does, so what the action did is in view.
  run(row) {
    const a = this.actions.get(row);
    if (!this.confirm(row, a)) return;
    this.hideSheet();
    this.input.act(a.id);
  }

  // confirm is the two-press Quit: the first press of a Quit button or row
  // only arms it, and a second press of it within QUIT_CONFIRM_MS quits. Alt+Q
  // goes straight to Input.act and quits at once.
  confirm(el, a) {
    if (a.id !== "quit") return true;
    if (this.quitArmed === el && performance.now() < this.quitUntil) {
      this.disarmQuit();
      return true;
    }
    this.armQuit(el, a);
    return false;
  }

  // armQuit asks "Quit?" in place. The control keeps the width it had, so the
  // longer text moves nothing beside it.
  armQuit(el, a) {
    this.disarmQuit();
    const width = el.getBoundingClientRect().width;
    el.style.boxSizing = "border-box";
    el.style.width = width + "px";
    const label = this.labels.get(el);
    this.quitText = label.textContent;
    label.textContent = this.quitText + "?";
    this.quitTitle = el.title;
    el.title = a.label + "? Press again to end the session";
    el.classList.add("armed");
    this.quitArmed = el;
    this.quitUntil = performance.now() + QUIT_CONFIRM_MS;
    const timer = setTimeout(() => { if (this.quitTimer === timer) this.disarmQuit(); }, QUIT_CONFIRM_MS);
    this.quitTimer = timer;
    toast("press " + a.short + " again to end the session", QUIT_CONFIRM_MS);
  }

  disarmQuit() {
    const el = this.quitArmed;
    if (!el) return;
    this.quitArmed = null;
    clearTimeout(this.quitTimer);
    this.quitTimer = 0;
    this.labels.get(el).textContent = this.quitText;
    el.title = this.quitTitle;
    el.classList.remove("armed");
    el.style.width = "";
    el.style.boxSizing = "";
  }

  toggleSheet() {
    if (this.sheetOpen) this.hideSheet();
    else this.showSheet();
  }

  // showSheet opens the sheet and moves the focus to its first row.
  showSheet() {
    if (!this.sheet) return;
    this.opener = document.activeElement;
    this.sheet.classList.remove("idle");
    this.sheet.inert = false;
    if (this.bar) this.bar.classList.add("idle");
    this.expanded(true);
    const first = this.controls()[0];
    if (first) first.focus();
  }

  // hideSheet closes the sheet and gives the focus back to what had it, if
  // the focus is still in the sheet (or nowhere): a bye moves it to the
  // Reconnect button, and there it stays.
  hideSheet() {
    if (!this.sheetOpen) return;
    this.disarmQuit();
    const focus = document.activeElement;
    this.sheet.classList.add("idle");
    this.sheet.inert = true; // a faded sheet must not be tabbable
    this.expanded(false);
    const back = this.opener;
    this.opener = null;
    const inSheet = !focus || focus === document.body || this.owns(focus);
    if (inSheet && back && typeof back.focus === "function") back.focus();
  }

  get sheetOpen() {
    return !!this.sheet && !this.sheet.classList.contains("idle");
  }

  // expanded says on ☰ and on the bar's Controls button whether the sheet is open.
  expanded(open) {
    for (const b of this.toggles) b.setAttribute("aria-expanded", open ? "true" : "false");
  }

  // controls are the sheet's buttons, in the order Tab visits them.
  controls() {
    return this.close ? this.rows.concat([this.close]) : this.rows;
  }

  owns(el) {
    return this.controls().includes(el);
  }

  // cycle moves the focus to the sheet's next (dir 1) or previous (-1)
  // button, from the last round to the first, so Tab never leaves the sheet.
  cycle(dir) {
    const list = this.controls();
    if (!list.length) return;
    const at = list.indexOf(document.activeElement);
    const next = at < 0 ? (dir > 0 ? 0 : list.length - 1) : (at + dir + list.length) % list.length;
    list[next].focus();
  }
}

// ---------------------------------------------------------------------------
// connection status: the card over the canvas, the page title and the HUD
// readouts. Pure rules with no DOM access: web/conn_test.mjs lifts this
// section, up to "end of connection status", and runs it on its own.
// ---------------------------------------------------------------------------

const IDLE_MS = 2000;   // no frame painted for this long: the fps readout says "idle"
const GLITCH_MS = 5000; // a decode error or lost frame keeps the state red this long

class ConnState {
  constructor() {
    this.opened = false;    // a hello arrived on some socket: the page was connected once
    this.connected = false; // a hello arrived on the current socket
    this.bye = null;        // the server's reason for ending the session
    this.frame = false;     // a video frame arrived on the current socket
    this.device = "";       // device name from the last hello that had one
    this.retry = false;     // Reconnect was pressed and no hello has come since
  }

  hello(device) {
    this.opened = true;
    this.connected = true;
    this.retry = false;
    if (device) this.device = device;
  }

  video() { this.frame = true; }

  closed() {
    this.connected = false;
    this.frame = false;
  }

  ended(reason) { this.bye = reason || ""; }

  // reconnect is the Reconnect button. The ended session's socket can still be
  // open or closing, and nothing that arrives on it counts any more.
  reconnect() {
    this.bye = null;
    this.connected = false;
    this.retry = true;
  }

  // card is what the status card says, or null while live video is showing.
  // A bye outranks the rest: frames still in flight must not hide it.
  card() {
    if (this.bye !== null) return { text: "Session ended", detail: this.bye, button: "Reconnect" };
    if (!this.connected) {
      if (this.retry) return { text: "Reconnecting…" };
      return { text: this.opened ? "Connection lost. Reconnecting…" : "Connecting…" };
    }
    if (!this.frame) return { text: "Waiting for video from " + (this.device || "the device") + "…" };
    return null;
  }

  title() {
    return this.device ? this.device + " · scterm" : "scterm";
  }
}

// fpsLabel is the HUD's frame rate, or "idle" once no frame has been painted
// for IDLE_MS (msSinceFrame is Infinity before the first one).
function fpsLabel(fps, msSinceFrame) {
  return msSinceFrame >= IDLE_MS ? "idle" : fps.toFixed(1) + " fps";
}

// stateLabel is the HUD's state text and colour. Decode errors and lost
// frames turn it red only while recent, so one glitch does not stay red until
// a reload (the ms values are Infinity when it never happened).
function stateLabel(control, msSinceError, msSinceLost) {
  if (!control) return { text: "control disabled", cls: "warn" };
  if (msSinceError < GLITCH_MS) return { text: "connected · decode errors", cls: "bad" };
  if (msSinceLost < GLITCH_MS) return { text: "connected · frames lost", cls: "bad" };
  return { text: "connected", cls: "ok" };
}

// geomLabel is the HUD's resolution: the canvas, plus the device video size
// when the two differ, or the video size alone before the first frame.
function geomLabel(st) {
  if (st.canvasW) {
    return st.canvasW + "×" + st.canvasH +
      (st.videoW && st.canvasW !== st.videoW ? " ← " + st.videoW + "×" + st.videoH : "");
  }
  return st.videoW ? st.videoW + "×" + st.videoH : "—";
}

// audioLabel is the HUD's sound readout: while the host audio is on, the
// DEVICE's media volume -- what the volume keys change, as a percentage of the
// device's own scale, "—" until the first reading -- and otherwise the host
// audio state ("off", "error"). It is not this browser's volume (the sound
// chip reports that), hence "device vol". The underruns are this page's.
function audioLabel(st, underruns) {
  let text;
  if (st.audio !== "on") text = "audio " + st.audio;
  else if (!st.devVolOk) text = "device vol —";
  else if (st.devVol === 0) text = "device vol muted";
  else text = "device vol " + st.devVol + "%";
  return text + (underruns ? " · u" + underruns : "");
}

// end of connection status

// ---------------------------------------------------------------------------
// wiring
// ---------------------------------------------------------------------------

const net = new Net();
const view = new View(els.canvas);
const pcm = new Pcm();
const input = new Input(net, view);
const toolbar = new Toolbar(input, CATALOG);
// Without this call no listener is ever registered and the page is a picture of
// the device rather than a way to drive it: no pointer, wheel, key or clipboard
// events at all, in both --web and --window. Easy to lose, because the player
// still connects, streams and paints perfectly well without it.
input.attach();
// pointer.js is part of the same bundle, but a missing/failed overlay must
// never take the connection down with it: this file is the one that wires the
// socket, and a throw here would leave a black page with no reconnect path.
if (typeof Pointer !== "undefined") Pointer.attach();

const conn = new ConnState();
let helloDone = false;
let lastStatus = { videoW: 0, videoH: 0 };
let stats = { seq: 0, lastSeq: 0, lost: 0, lostAt: -Infinity, bytes: 0, lastBytes: 0, drawn: 0, at: performance.now() };

// showConn puts the connection state on the page: the card, and the dimmed
// canvas while the picture is not live. The card is a live region, so it is
// only rewritten when its content changes.
let connShown;
function showConn() {
  const card = conn.card();
  const key = JSON.stringify(card);
  if (key === connShown) return;
  connShown = key;
  els.canvas.classList.toggle("dim", card !== null);
  els.conn.classList.toggle("off", card === null);
  els.connText.textContent = card ? card.text : "";
  els.connDetail.textContent = (card && card.detail) || "";
  els.connRetry.hidden = !(card && card.button);
  if (card && card.button) {
    els.connRetry.textContent = card.button;
    // Focused, so Enter or Space presses it (Input leaves keys to the browser
    // once the session has ended).
    els.connRetry.focus();
  }
}

// showFps runs from the status messages, and from the telemetry tick while
// none arrive, so a dead socket reads "idle" instead of the last rate.
function showFps() {
  const since = view.lastDraw ? performance.now() - view.lastDraw : Infinity;
  els.fps.textContent = fpsLabel(view.fps || 0, since) +
    (view.decodeMs ? " · " + view.decodeMs.toFixed(1) + "ms decode" : "") +
    (view.errors ? " · " + view.errors + " errors" : "") +
    (stats.lost ? " · " + stats.lost + " lost" : "");
}

function sendHello() {
  const vp = view.viewport();
  net.send({ op: "hello", w: vp.w, h: vp.h, format: "auto", caps: "audio" });
}

function sendResize() {
  const vp = view.viewport();
  net.send({ op: "resize", w: vp.w, h: vp.h });
}

net.on("hello", (msg) => {
  helloDone = true;
  conn.hello(msg.device);
  els.device.textContent = conn.device || "device";
  document.title = conn.title();
  setState(msg.raw ? "connected (raw)" : "connected", "ok");
  showConn();
});

net.on("status", (msg) => {
  lastStatus = msg;
  els.geom.textContent = geomLabel(msg);
  showFps();
  // The DEVICE's media volume. The old display used msg.gain, but that gain is
  // applied in writePCM16 for the PulseAudio sink while the browser is fed raw
  // PCM by pushPCM -- so it was reporting a control that could not change what
  // you hear.
  els.audio.textContent = audioLabel(msg, pcm.stats.underruns);
  const now = performance.now();
  const st = stateLabel(msg.control, now - view.errorAt, now - stats.lostAt);
  setState(st.text, st.cls);
});

net.on("video", (frame) => {
  if (frame.seq && stats.lastSeq && frame.seq !== stats.lastSeq + 1) {
    if (frame.seq <= stats.lastSeq) {
      // Sequence numbers are per server-side client and restart at 1 when the
      // page reconnects or the device session respawns. Counting that jump as
      // loss drove the counter negative; a restart is not loss, so begin again.
      stats.lost = 0;
    } else {
      stats.lost += frame.seq - stats.lastSeq - 1;
      stats.lostAt = performance.now();
    }
  }
  stats.lastSeq = frame.seq;
  stats.bytes += frame.body.byteLength;
  view.push(frame);
  if (!conn.frame) {
    conn.video();
    showConn();
  }
});

// The server is ending the session; Net has already stopped retrying.
net.on("bye", (msg) => {
  conn.ended(msg.reason);
  showConn();
});

net.onClose = () => {
  conn.closed();
  showConn();
};

els.connRetry.addEventListener("click", () => {
  conn.reconnect();
  showConn();
  net.reconnect();
});

net.on("audio", (buf) => pcm.play(buf));

// showSound puts this browser's own sound state on the sound chip. The
// compatibility note shows for NOTE_MS, the first time sound plays that way.
const NOTE_MS = 6000;
let soundShown;
let soundAct = "";
let compatNote = "due"; // "due", then "shown" for NOTE_MS, then "done"
let soundDismissed = false;
function showSound() {
  if (compatNote === "due" && pcm.mode === "compat" && pcm.heard && pcm.running) {
    compatNote = "shown";
    setTimeout(() => {
      compatNote = "done";
      showSound();
    }, NOTE_MS);
  }
  const chip = soundChip({
    mode: pcm.mode,
    heard: pcm.heard,
    running: pcm.running,
    muted: pcm.localGain === 0,
    note: compatNote === "shown",
    dismissed: soundDismissed,
  });
  soundAct = chip ? chip.act : "";
  const key = JSON.stringify(chip);
  if (key === soundShown) return;
  soundShown = key;
  els.soundChip.classList.toggle("off", chip === null);
  if (!chip) return; // the words stay while it fades out
  els.soundChip.textContent = chip.text;
  els.soundChip.title = chip.title || "";
}

els.soundChip.addEventListener("click", () => {
  if (soundAct === "unlock") {
    pcm.unlock();
  } else if (soundAct === "unmute") {
    input.localMute();
  } else if (soundAct === "explain") {
    toast(COMPAT_NOTE, NOTE_MS);
    compatNote = "done";
  } else if (soundAct === "dismiss") {
    soundDismissed = true;
  }
  showSound();
});

// Arms the gestures that start the sound (Pcm.unlock) and draws the chip.
pcm.onchange = showSound;
pcm.changed();

// Client-side telemetry: proves the page is really painting (and lets
// SCT_DEBUG_WEB=1 on the host log it). Cheap: one small JSON per second.
// The data is also published to window.sctermPlayer so an automated browser
// check can assert on it instead of guessing from pixels.
setInterval(() => {
  if (!net.connected) {
    showFps();
    return;
  }
  const now = performance.now();
  const dts = now - stats.at;
  stats.at = now;
  const fps = dts > 0 ? (view.drawn - stats.drawn) * 1000 / dts : 0;
  const kbps = dts > 0 ? (stats.bytes - stats.lastBytes) * 8 / dts : 0;
  stats.lastBytes = stats.bytes;
  stats.drawn = view.drawn;
  const report = {
    op: "stats",
    frames: view.drawn,
    fps: Math.round(fps * 10) / 10,
    decodeMs: Math.round(view.decodeMs * 100) / 100,
    errors: view.errors,
    kbps: Math.round(kbps),
    lost: stats.lost,
    audio: pcm.stats,
    hidden: document.hidden,
  };
  net.send(report);
  player.state = report;
}, 1000);

// window.sctermPlayer: live client state, for the automated browser check
// (web/check.mjs) and for debugging in the browser console.
const player = {
  state: null,
  view: view,
  pcm: pcm,
  net: net,
  screenshot,
};
window.sctermPlayer = player;

// The hello is the connection's negotiation: it tells the server this client's
// viewport, payload format and caps (audio). It must be sent when the socket is
// OPEN -- and again after every reconnect, because the server builds a fresh
// client (audio off, default viewport) for each new socket.
net.onOpen = () => sendHello();
showConn();
net.connect();

window.addEventListener("resize", () => {
  view.fit(); // refit at once; only the server notification is debounced
  fitHud();   // the bar may have rewrapped: keep the HUD clear of it
  clearTimeout(window.__sctResize);
  window.__sctResize = setTimeout(sendResize, 120);
});

// The HUD and the action bar fade out while the user is not touching
// anything; ☰ stays visible. The bar and ☰ are made inert while it is faded
// (the HUD has nothing to focus), so a faded bar cannot be tabbed into. The
// fade does nothing while the controls sheet is
// open, so nothing turns inert under it: closing the sheet gives the focus
// back to ☰ or the bar's Controls button, and an inert button cannot take it.
// (showSheet hides the bar, but leaves it able to take the focus.)
let hudTimer = 0;
function chrome(on) {
  if (!on && toolbar.sheetOpen) return;
  els.hud.classList.toggle("idle", !on);
  const bar = document.getElementById("bar");
  const mb = document.getElementById("menu-btn");
  if (bar) {
    bar.classList.toggle("idle", !on);
    bar.inert = !on;
  }
  if (mb) mb.inert = !on;
}
// The action bar wraps to more rows on narrow screens, so the HUD's offset
// above it is measured rather than guessed: a fixed offset that clears two
// rows still gets covered by four on a phone. Re-measured whenever the bar
// changes size (ResizeObserver, where available).
function fitHud() {
  const bar = document.getElementById("bar");
  if (!bar || !els.hud) return;
  els.hud.style.bottom = Math.round(bar.getBoundingClientRect().height) + 8 + "px";
}
function pokeHud() {
  chrome(true);
  clearTimeout(hudTimer);
  hudTimer = setTimeout(() => chrome(false), 2500);
}
window.addEventListener("pointermove", pokeHud);
window.addEventListener("pointerdown", pokeHud);
window.addEventListener("keydown", pokeHud);
window.addEventListener("wheel", pokeHud);
fitHud();
if (typeof ResizeObserver !== "undefined") {
  const bar = document.getElementById("bar");
  if (bar) new ResizeObserver(fitHud).observe(bar);
}
pokeHud();

document.addEventListener("visibilitychange", () => {
  if (!document.hidden) {
    pokeHud();
    sendResize();
    net.send({ op: "ping" });
  }
});
// NOTE: closing a tab must NOT end the session -- a second viewer, or a reload,
// would lose the mirror. --window ends 3 s after the last client disconnects
// (webLeaveGrace in web.go), unless a page connects again first, as a reload
// does; closing the window disconnects.

