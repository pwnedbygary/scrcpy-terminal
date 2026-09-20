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
};

const MSG_VIDEO = 0x01;
const MSG_AUDIO = 0x02;
const MSG_HELLO = 0x03;
const VIDEO_HEADER = 16;

const FMT_JPEG = 0;
const FMT_RAW = 1;

let toastTimer = 0;
function toast(msg, ms) {
  els.toast.textContent = msg;
  els.toast.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => els.toast.classList.remove("show"), ms || 1600);
}

function setState(text, cls) {
  els.state.textContent = text;
  els.state.className = cls || "";
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
// audio: raw PCM into an AudioWorklet ring buffer
// ---------------------------------------------------------------------------

class Pcm {
  constructor() {
    this.ctx = null;
    this.node = null;
    this.gain = null;
    this.ready = false;
    this.failed = false;
    // Local playback gain (Alt+M / Alt+- / Alt+=). Kept here rather than only on
    // the node because the node is created asynchronously, on the first audio
    // packet -- muting before any sound has arrived still has to stick.
    this.localGain = 1;
    this.stats = { underruns: 0, dropped: 0, queued: 0 };
  }

  setLocalGain(v) {
    this.localGain = Math.max(0, Math.min(2, v));
    if (this.gain) this.gain.gain.value = this.localGain;
  }

  async start() {
    if (this.ready || this.failed) return;
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) { this.failed = true; return; }
    try {
      const ctx = new AC({ sampleRate: 48000, latencyHint: "interactive" });
      await ctx.audioWorklet.addModule("/pcm-worklet.js");
      const node = new AudioWorkletNode(ctx, "scterm-pcm", {
        numberOfInputs: 0,
        numberOfOutputs: 1,
        outputChannelCount: [2],
        processorOptions: { targetFrames: 4608, maxFrames: 11520 },
      });
      const gain = ctx.createGain();
      gain.gain.value = this.localGain;
      node.connect(gain).connect(ctx.destination);
      node.port.onmessage = (e) => {
        if (e.data && e.data.stats) this.stats = e.data.stats;
      };
      this.ctx = ctx;
      this.node = node;
      this.gain = gain;
      this.ready = true;
      this.resume();
    } catch (e) {
      this.failed = true;
    }
  }

  resume() {
    if (this.ctx && this.ctx.state === "suspended") {
      this.ctx.resume().catch(() => {});
    }
  }

  push(buf) {
    if (!this.ready) return;
    // Transfer the underlying buffer: no copy on the main thread.
    this.node.port.postMessage({ pcm: buf }, [buf]);
  }
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

// The app-level keys, identical to the terminal's (input.go fKeyMap +
// appkeys.go mapAppFKey): F1 home, F2 menu, F3 recents, F4 power, F5 vol-,
// F6 vol+, F7 mute, F8 rotate, F9 notifications, F10 settings, F11 collapse,
// F12 grab. The values are the same op names the server already understands,
// and the server turns them into the same Android keycodes for both modes.
const APP_KEYS = {
  F1: "home", F2: "menu", F3: "appswitch", F4: "power",
  F5: "voldown", F6: "volup", F7: "mute",
  F8: "rotate", F9: "notif", F10: "settings", F11: "collapse",
  F12: "grab",
};

// Alt+F1..F12: the same actions. A bare F-key is the terminal's binding, but an
// OS, a window manager or a phone keyboard may never deliver one.
const ALT_FKEYS = APP_KEYS;

// Ctrl+A..Z -> Android letter keycodes 29..54, as handleByte() sends them.
const LETTER_KEYCODES = {};
for (let i = 0; i < 26; i++) {
  LETTER_KEYCODES["Key" + String.fromCharCode(65 + i)] = 29 + i;
}

// Accelerators that belong to the BROWSER, not the page: with grab off they are
// left alone, because closing the window ends a --window session. Grab (F12,
// the terminal's own key) hands them to the device instead.
const CTRL_BROWSER_KEYS = new Set(["KeyW", "KeyT", "KeyN", "KeyR", "KeyQ"]);

// Alt+<letter> mnemonic device actions. This is the primary binding on a phone
// keyboard (Unexpected Keyboard has Ctrl and Alt, but no F-key row), and the
// TUI uses exactly the same table (appkeys.go appMnemonicOps) — so Alt+H is
// Home whether you are in a terminal, a browser tab or --window. Keep the two
// tables in step: web_keymap_test.go parses this one and compares.
//
// Alt+M is deliberately NOT here: it stays the LOCAL mute. The device's own
// mute has no chord (F7 and the action bar carry it).
//
// Slash is a punctuation chord: a phone keyboard reports it by character and
// codeOf() normalises it (PUNCT_CODES) before this lookup, so Alt+/ opens the
// controls sheet there too. The ☰ button remains the always-available path.
const MNEMONIC_ACTIONS = {
  KeyB: "back", KeyC: "collapse", KeyD: "voldown", KeyE: "settings",
  KeyG: "grab", KeyH: "home", KeyI: "keyboard", KeyK: "resetvideo",
  KeyN: "notif", KeyP: "power", KeyQ: "quit", KeyR: "rotate",
  KeyS: "screenshot", KeyT: "appswitch", KeyU: "volup",
  Slash: "toolbar",
};

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
      // Right click = Android back, like the TUI.
      this.net.send({ op: "back" });
    });

    c.addEventListener("wheel", (ev) => {
      ev.preventDefault();
      this.lastPos = this.view.norm(ev);
      this.wheelAcc += -ev.deltaY;
      const notch = 50;
      if (Math.abs(this.wheelAcc) >= notch) {
        const notches = Math.trunc(this.wheelAcc / notch);
        this.wheelAcc -= notches * notch;
        this.sendPos("wheel", { delta: notches });
      }
    }, { passive: false });

    document.addEventListener("paste", (ev) => {
      const text = (ev.clipboardData || window.clipboardData).getData("text");
      if (text) {
        ev.preventDefault();
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
    // The controls sheet is documentation, not a mode: Esc closes it, and it
    // never swallows input aimed at the device.
    if (down && ev.key === "Escape" && toolbar.sheetOpen) {
      ev.preventDefault();
      toolbar.hideSheet();
      return;
    }
    // `name` is the physical key, or the nearest equivalent a phone keyboard
    // offers: soft keyboards routinely leave `code` empty, which used to make
    // every hotkey below unreachable from one.
    const name = codeOf(ev);

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
      if (name === "KeyG") {
        if (down) this.toggleGrab();
        ev.preventDefault();
        return;
      }
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
        this.net.send({ op: action });
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
      if (down) this.net.send({ op: "back" });
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

  sendKey(code, down, ev) {
    if (!down) {
      this.net.send({ op: "key", code: code, meta: 0 });
      return;
    }
    if (ev.repeat) return; // the device handles repeats itself
    let meta = 0;
    if (ev.shiftKey) meta |= 0x01;
    if (ev.altKey) meta |= 0x02;
    if (ev.ctrlKey) meta |= 0x1000;
    if (ev.metaKey) meta |= 0x10000; // AMETA_META_ON
    this.net.send({ op: "key", code: code, meta: meta });
  }

  // act runs one app-level action. Device ops are just an op for the server,
  // which maps them to the same Android keycode the terminal would send; the
  // few local ones (grab, keyboard, toolbar, screenshot) are handled here.
  // Toolbar button presses call this too, so button and chord are the same call.
  act(op) {
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

  toggleGrab() {
    this.grab = !this.grab;
    toast(this.grab
      ? "grab: browser keys go to the device"
      : "grab off: Ctrl+W/T/N/R/Q stay in the browser");
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
// The bar is the no-F-key, no-chord path to every action: one button per op,
// wired through Input.act so a click and its keyboard chord are literally the
// same call. The sheet is documentation only -- it runs nothing, so it cannot
// disagree with the bindings it lists.
// ---------------------------------------------------------------------------

class Toolbar {
  constructor(input) {
    this.input = input;
    this.bar = document.getElementById("bar");
    this.sheet = document.getElementById("sheet");
    this.timers = new WeakMap();
    this.buttons = Array.prototype.slice.call(
      document.querySelectorAll("#bar button[data-act]"));
    for (const btn of this.buttons) {
      btn.addEventListener("click", () => this.press(btn));
    }
    const close = document.getElementById("sheet-close");
    if (close) close.addEventListener("click", () => this.hideSheet());
    const menuBtn = document.getElementById("menu-btn");
    if (menuBtn) menuBtn.addEventListener("click", () => this.toggleSheet());
    if (this.sheet) {
      // The sheet starts faded; keep it out of the tab order until opened.
      this.sheet.inert = true;
      this.sheet.addEventListener("click", (ev) => {
        if (ev.target === this.sheet) this.hideSheet(); // click the backdrop
      });
    }
  }

  // press runs the button's action and flashes it, so a tap has visible
  // feedback even when the device response is off-screen.
  press(btn) {
    const act = btn.dataset.act;
    if (act === "toolbar") { this.toggleSheet(); return; }
    this.input.act(act);
    btn.classList.add("done");
    clearTimeout(this.timers.get(btn));
    this.timers.set(btn, setTimeout(() => btn.classList.remove("done"), 350));
  }

  toggleSheet() {
    if (!this.sheet) return;
    const hide = !this.sheet.classList.contains("idle");
    if (hide) { this.hideSheet(); return; }
    this.sheet.classList.remove("idle");
    this.sheet.inert = false;
    if (this.bar) this.bar.classList.add("idle");
  }

  hideSheet() {
    if (!this.sheet) return;
    this.sheet.classList.add("idle");
    this.sheet.inert = true; // a faded sheet must not be tabbable
  }

  get sheetOpen() {
    return !!this.sheet && !this.sheet.classList.contains("idle");
  }
}

// ---------------------------------------------------------------------------
// wiring
// ---------------------------------------------------------------------------

const net = new Net();
const view = new View(els.canvas);
const pcm = new Pcm();
const input = new Input(net, view);
const toolbar = new Toolbar(input);
// Without this call no listener is ever registered and the page is a picture of
// the device rather than a way to drive it: no pointer, wheel, key or clipboard
// events at all, in both --web and --window. Easy to lose, because the player
// still connects, streams and paints perfectly well without it.
input.attach();
// pointer.js is part of the same bundle, but a missing/failed overlay must
// never take the connection down with it: this file is the one that wires the
// socket, and a throw here would leave a black page with no reconnect path.
if (typeof Pointer !== "undefined") Pointer.attach();

let helloDone = false;
let lastStatus = { videoW: 0, videoH: 0 };
let stats = { seq: 0, lastSeq: 0, lost: 0, bytes: 0, lastBytes: 0, drawn: 0, at: performance.now() };

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
  els.device.textContent = msg.device || "device";
  setState(msg.raw ? "connected (raw)" : "connected", "ok");
});

net.on("status", (msg) => {
  lastStatus = msg;
  els.device.textContent = (msg.videoW && msg.videoH) ? (msg.videoW + "×" + msg.videoH) : (msg.device || "device");
  els.geom.textContent = msg.canvasW + "×" + msg.canvasH +
    (msg.videoW && msg.canvasW !== msg.videoW ? " ← " + msg.videoW + "×" + msg.videoH : "");
  els.fps.textContent = (view.fps || 0).toFixed(1) + " fps" +
    (view.decodeMs ? " · " + view.decodeMs.toFixed(1) + "ms decode" : "") +
    (view.errors ? " · " + view.errors + " errors" : "") +
    (stats.lost ? " · " + stats.lost + " lost" : "");
  // Show the DEVICE's media volume. The old display used msg.gain, but that gain
  // is applied in writePCM16 for the PulseAudio sink while the browser is fed
  // raw PCM by pushPCM -- so it was reporting a control that could not change
  // what you hear. devVol is the volume the volume keys actually change, as a
  // percentage of the device's own scale (index 0 = muted).
  let level;
  if (!msg.devVolOk) {
    level = "—";
  } else if (msg.devVol === 0) {
    level = "muted";
  } else {
    level = msg.devVol + "%";
  }
  els.audio.textContent = "audio " + (msg.audio === "on" ? level : msg.audio) +
    (pcm.stats.underruns ? " · u" + pcm.stats.underruns : "");
  if (!msg.control) {
    setState("control disabled", "warn");
  } else if (view.errors) {
    setState("connected · decode errors", "bad");
  } else if (stats.lost > 30) {
    setState("connected · " + stats.lost + " dropped", "warn");
  } else {
    setState("connected", "ok");
  }
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
    }
  }
  stats.lastSeq = frame.seq;
  stats.bytes += frame.body.byteLength;
  view.push(frame);
});

net.on("audio", (buf) => {
  pcm.start().then(() => pcm.push(buf));
});

// Client-side telemetry: proves the page is really painting (and lets
// SCT_DEBUG_WEB=1 on the host log it). Cheap: one small JSON per second.
// The data is also published to window.sctermPlayer so an automated browser
// check can assert on it instead of guessing from pixels.
setInterval(() => {
  if (!net.connected) return;
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
net.connect();

window.addEventListener("resize", () => {
  view.fit(); // refit at once; only the server notification is debounced
  fitHud();   // the bar may have rewrapped: keep the HUD clear of it
  clearTimeout(window.__sctResize);
  window.__sctResize = setTimeout(sendResize, 120);
});

// The HUD and the action bar fade out while the user is not touching
// anything. The floating menu button fades with them (the same .idle class),
// so all chrome appears and disappears as one layer. Hidden chrome is also
// made inert, so a faded bar cannot be tabbed into.
let hudTimer = 0;
function chrome(on) {
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

// A tap on the page is the user gesture audio needs.
window.addEventListener("pointerdown", () => pcm.start(), { once: true });
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) {
    pokeHud();
    sendResize();
    net.send({ op: "ping" });
  }
});
// NOTE: closing a tab must NOT end the session -- a second viewer, or a reload,
// would lose the mirror. --window ends when the last client disconnects, and
// closing the window disconnects.

