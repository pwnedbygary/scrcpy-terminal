#!/usr/bin/env node
// gesture.mjs — drive a touch gesture on the device through the scterm web
// mirror's control channel (the exact path the browser uses: op down/move/up
// with 0..65535 fractional coordinates).
//
// The mirror must already be running:  ./scterm --web
//
// Lock-pattern entry. Digits 1..9 are the 3x3 grid, numbered like a phone
// keypad (1 = top-left, 3 = top-right, 7 = bottom-left, 9 = bottom-right):
//
//   node web/gesture.mjs 1478
//
// Arbitrary swipe, in device pixels:
//
//   node web/gesture.mjs --swipe 960,700,960,250
//
// Why by-coordinate instead of drawing in the canvas: while the lock-screen
// bouncer (pattern/PIN/password entry) is displayed, Android marks that surface
// FLAG_SECURE, so the browser canvas is black exactly when the pattern grid is
// visible. The grid geometry is fixed and readable from the accessibility tree,
// so the gesture is driven numerically instead. (screencap fails the same way:
// exit 1, zero bytes.)
//
// Geometry is read from the device by default (--auto), which is what you want:
// it survives rotation. --grid/--device override it for offline use.

import { execFileSync } from "node:child_process";
import process from "node:process";

// ---------------------------------------------------------------------------
// argument parsing
// ---------------------------------------------------------------------------

const USAGE = `usage: node web/gesture.mjs [options] <pattern-digits>
       node web/gesture.mjs [options] --swipe x0,y0,x1,y1

options:
  --host H          mirror host (default 127.0.0.1)
  --port N          mirror port (default 6969)
  --auto            read grid + display size from adb (default)
  --grid x0,y0,x1,y1   lock-pattern view bounds, device px
  --device WxH      display size, device px
  --step PX         interpolation step between dots (default 45)
  --delay MS        pause between travel events (default 8)
  --dwell N         repeats at each dot, to defeat event coalescing (default 3)
  --settle MS       pause after touch-down and before touch-up (default 60)
  --probe           connect, wait for frames, send nothing, exit
  --back            send the Back op (dismisses the bouncer), no gesture
  --dry-run         print the path without connecting
  -h, --help        this text`;

function parseArgs(argv) {
  const o = {
    host: "127.0.0.1",
    port: 6969,
    step: 45,
    delay: 8,
    dwell: 3,
    dwellDelay: 30,
    settle: 60,
    grid: null,
    device: null,
    auto: null, // null = decide later (auto unless explicit geometry given)
    probe: false,
    dryRun: false,
    back: false,
    swipe: null,
    digits: null,
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const need = () => {
      if (i + 1 >= argv.length) die(`missing value for ${a}`);
      return argv[++i];
    };
    if (a === "-h" || a === "--help") {
      console.log(USAGE);
      process.exit(0);
    } else if (a === "--host") o.host = need();
    else if (a === "--port") o.port = Number(need());
    else if (a === "--auto") o.auto = true;
    else if (a === "--probe") o.probe = true;
    else if (a === "--dry-run") o.dryRun = true;
    else if (a === "--back") o.back = true;
    else if (a === "--step") o.step = Number(need());
    else if (a === "--delay") o.delay = Number(need());
    else if (a === "--dwell") o.dwell = Number(need());
    else if (a === "--settle") o.settle = Number(need());
    else if (a === "--swipe") o.swipe = parsePointList(need());
    else if (a === "--grid") {
      const v = need().split(",").map(Number);
      if (v.length !== 4 || v.some(Number.isNaN)) die("--grid wants x0,y0,x1,y1");
      o.grid = { x0: v[0], y0: v[1], x1: v[2], y1: v[3] };
    } else if (a === "--device") {
      const m = /^(\d+)x(\d+)$/.exec(need());
      if (!m) die("--device wants WxH");
      o.device = { w: Number(m[1]), h: Number(m[2]) };
    } else if (a.startsWith("-")) die(`unknown option ${a}`);
    else if (o.digits === null) o.digits = a;
    else die(`unexpected extra argument ${a}`);
  }
  if (o.auto === null) o.auto = !(o.grid && o.device);
  return o;
}

function die(msg) {
  console.error(`gesture: ${msg}`);
  process.exit(2);
}

function parsePointList(s) {
  const v = s.split(",").map(Number);
  if (v.length !== 4 || v.some(Number.isNaN)) die("--swipe wants x0,y0,x1,y1");
  return { x0: v[0], y0: v[1], x1: v[2], y1: v[3] };
}

// ---------------------------------------------------------------------------
// geometry
// ---------------------------------------------------------------------------

// AOSP LockPatternView lays 9 dots on an evenly divided 3x3 grid, so the dot
// centres are the centre of each cell.
function dotCentre(grid, digit) {
  if (!Number.isInteger(digit) || digit < 1 || digit > 9) {
    die(`pattern digit ${digit} out of range 1..9`);
  }
  const col = (digit - 1) % 3;
  const row = Math.floor((digit - 1) / 3);
  const cw = (grid.x1 - grid.x0) / 3;
  const ch = (grid.y1 - grid.y0) / 3;
  return { x: grid.x0 + cw * (col + 0.5), y: grid.y0 + ch * (row + 0.5) };
}

// Read the live geometry off the device. The accessibility tree is available
// even though the surface is FLAG_SECURE (it is the *pixels* that are withheld,
// not the layout), and the root node's bounds give the current rotation's
// display size.
function readGeometryFromDevice(needGrid) {
  const TMP = "/sdcard/.scterm-gesture-ui.xml";
  try {
    execFileSync("adb", ["shell", "uiautomator", "dump", TMP], { stdio: "ignore" });
    var xml = execFileSync("adb", ["exec-out", "cat", TMP], { encoding: "utf8" });
  } catch (e) {
    die(`could not read geometry from adb (${e.message}); pass --grid and --device`);
  }

  const nodes = [...xml.matchAll(/<node[^>]*>/g)].map((m) => m[0]);
  let device = null;
  const root = /bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"/.exec(nodes[0] || "");
  if (root) device = { w: Number(root[3]), h: Number(root[4]) };

  let grid = null;
  for (const n of nodes) {
    if (!/resource-id="[^"]*lockPatternView"/.test(n)) continue;
    const b = /bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"/.exec(n);
    if (b) grid = { x0: +b[1], y0: +b[2], x1: +b[3], y1: +b[4] };
  }
  // A plain --swipe only needs the display size: it carries its own coordinates.
  if (!grid && needGrid) {
    die(
      "no lockPatternView on screen — the pattern bouncer is not up.\n" +
        "       Swipe up on the lock screen first (or run this once the dots are showing)."
    );
  }
  if (!device) die("could not read display size from the accessibility tree");
  return { grid, device };
}

// ---------------------------------------------------------------------------
// path building
// ---------------------------------------------------------------------------

// Expand waypoints into the list of events to inject. Between dots the path is
// interpolated so a dot the line merely crosses is selected too. At every dot
// the touch dwells for a few repeats, because Android's InputDispatcher
// coalesces motion events for a busy app and keeps only the last of a batch:
// passing over a dot once does not guarantee the app ever observes it.
//
// Pacing matters and was measured on a Retroid Pocket 6 (Android 13). A dense
// slow drag is delivered but discarded by gesture recognition: step=12/delay=12
// raised the lock screen bouncer 1 time in 5, step=12/delay=40 never, while
// step=45/delay=8 worked 6/6 and step=30/delay=8 worked 4/4. Fewer, faster
// samples clear the velocity threshold that a slow crawl does not.
function buildEvents(points, step, dwell, dwellDelay, travelDelay, settleMs) {
  const out = [{ x: points[0].x, y: points[0].y, pause: settleMs }];
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1];
    const b = points[i];
    const dx = b.x - a.x;
    const dy = b.y - a.y;
    const n = Math.max(1, Math.ceil(Math.hypot(dx, dy) / step));
    for (let k = 1; k <= n; k++) {
      out.push({
        x: a.x + (dx * k) / n,
        y: a.y + (dy * k) / n,
        pause: k === n ? 0 : travelDelay,
      });
    }
    // Dwell on intermediate dots so coalescing cannot swallow them. Never dwell
    // on the final waypoint: a stationary hold before the lift removes the
    // velocity a fling needs, which turns a working swipe into a dead one.
    if (i < points.length - 1) {
      for (let d = 0; d < dwell; d++) {
        out.push({ x: b.x, y: b.y, pause: dwellDelay });
      }
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// transport — the same JSON ops the browser sends
// ---------------------------------------------------------------------------

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Connect, perform the handshake, and wait until the server has sent frames.
// Input before that maps to 0,0 because the server derives the canvas geometry
// from the first frame header.
async function connect(opts) {
  const url = `ws://${opts.host}:${opts.port}/ws`;
  const ws = new WebSocket(url);
  ws.binaryType = "arraybuffer";

  let frames = 0;
  let hello = null;
  const closed = new Promise((res) => ws.addEventListener("close", res));

  ws.addEventListener("message", (ev) => {
    if (typeof ev.data === "string") {
      try {
        const m = JSON.parse(ev.data);
        if (m.op === "hello") hello = m;
      } catch {}
    } else {
      frames++;
    }
  });

  await new Promise((res, rej) => {
    ws.addEventListener("open", res);
    ws.addEventListener("error", () => rej(new Error(`cannot connect to ${url}`)));
  });

  ws.send(JSON.stringify({ op: "hello", w: 1280, h: 720, format: "auto", caps: "audio" }));
  const deadline = Date.now() + 8000;
  while (frames < 2 && Date.now() < deadline) await sleep(50);
  if (frames < 2) die("no video frames arrived — is the mirror streaming?");

  console.error(
    `gesture: connected ${opts.host}:${opts.port} device=${hello?.device ?? "?"} frames=${frames}`
  );
  return { ws, closed };
}

async function runBack(opts) {
  const { ws, closed } = await connect(opts);
  ws.send(JSON.stringify({ op: "back" }));
  console.error("gesture: sent back");
  await sleep(500);
  ws.close();
  await closed;
}

async function run(opts, events, device) {
  const { ws, closed } = await connect(opts);

  if (opts.probe) {
    ws.close();
    await closed;
    console.error("gesture: probe ok (no input sent)");
    return;
  }

  console.error(`gesture: display=${device.w}x${device.h}`);

  const norm = (p) => ({
    x: Math.max(0, Math.min(65535, Math.round((p.x / device.w) * 65535))),
    y: Math.max(0, Math.min(65535, Math.round((p.y / device.h) * 65535))),
  });

  // Down on the first event, travel the path, lift on the last.
  const pts = events.map(norm);
  ws.send(JSON.stringify({ op: "down", ...pts[0] }));
  await sleep(opts.settle);
  for (let i = 1; i < pts.length; i++) {
    ws.send(JSON.stringify({ op: "move", ...pts[i] }));
    const pause = events[i].pause;
    if (pause) await sleep(pause);
  }
  await sleep(opts.settle);
  ws.send(JSON.stringify({ op: "up", ...pts[pts.length - 1] }));

  console.error(`gesture: sent down + ${pts.length - 1} moves + up (${pts.length} events)`);
  await sleep(400);
  ws.close();
  await closed;
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

const opts = parseArgs(process.argv.slice(2));

if (opts.back) {
  await runBack(opts);
  process.exit(0);
}

if (!opts.probe && !opts.digits && !opts.swipe) die(`nothing to do\n${USAGE}`);

let geometry;
if (opts.auto && !opts.grid) {
  geometry = readGeometryFromDevice(Boolean(opts.digits));
  geometry.grid ??= { x0: 606, y0: 216, x1: 1314, y1: 924 };
  if (opts.device) geometry.device = opts.device;
} else {
  geometry = {
    grid: opts.grid ?? { x0: 606, y0: 216, x1: 1314, y1: 924 },
    device: opts.device ?? { w: 1920, h: 1080 },
  };
}

let waypoints;
let label;
if (opts.swipe) {
  waypoints = [
    { x: opts.swipe.x0, y: opts.swipe.y0 },
    { x: opts.swipe.x1, y: opts.swipe.y1 },
  ];
  label = "swipe";
} else if (opts.digits) {
  const digits = opts.digits.split("").map(Number);
  waypoints = digits.map((d) => dotCentre(geometry.grid, d));
  label = `pattern ${opts.digits}`;
} else {
  // --probe with no gesture: geometry reporting only, nothing is injected.
  waypoints = [dotCentre(geometry.grid, 5)];
  label = "probe";
}

const events = buildEvents(waypoints, opts.step, opts.dwell, opts.dwellDelay, opts.delay, opts.settle);

if (opts.dryRun || opts.probe) {
  const g = geometry.grid;
  console.error(
    `gesture: ${label} grid=[${g.x0},${g.y0}][${g.x1},${g.y1}] ` +
      `display=${geometry.device.w}x${geometry.device.h}`
  );
  for (let i = 0; i < waypoints.length; i++) {
    const w = waypoints[i];
    const name = opts.digits ? opts.digits[i] : opts.swipe ? (i === 0 ? "start" : "end") : "-";
    console.error(
      `  ${name}: device=(${w.x.toFixed(0)},${w.y.toFixed(0)}) ` +
        `norm=(${Math.round((w.x / geometry.device.w) * 65535)},` +
        `${Math.round((w.y / geometry.device.h) * 65535)})`
    );
  }
  console.error(`  ${events.length} events after interpolation (step=${opts.step}px)`);
}
if (opts.dryRun) process.exit(0);

await run(opts, events, geometry.device);
