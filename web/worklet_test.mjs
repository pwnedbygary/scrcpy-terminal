//   node web/worklet_test.mjs
//
// Runs the real web/pcm-worklet.js with the AudioWorklet globals stubbed and
// pushes real PCM bursts through it, asserting on what comes out of process().
//
// This exists because the worklet held the whole browser audio path and nothing
// tested it: feed() computed the frame count as byteLength>>1 (samples, not
// stereo frames), so the Int16Array it built asked for twice the elements the
// buffer held and threw "RangeError: Invalid typed array length" on every
// burst. The processor survived, the ring buffer stayed empty, and the page
// played silence forever -- while the HUD said "connected", the server said the
// audio was flowing and Chromium even held an open PulseAudio stream.

import { readFileSync } from "node:fs";
import vm from "node:vm";

const src = readFileSync(new URL("./pcm-worklet.js", import.meta.url), "utf8");

let pass = 0, fail = 0;
function check(name, got, want) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g === w) { pass++; console.log(`  ok   ${name}`); }
  else { fail++; console.log(`  FAIL ${name}\n         got  ${g}\n         want ${w}`); }
}

// AudioWorkletGlobalScope stubs. `frames` is the global frame counter the
// worklet uses to rate-limit its stat reporting; `rate` is sampleRate.
const scope = {};
class AudioWorkletProcessor {
  constructor() {
    this.port = { onmessage: null, postMessage: (m) => scope.posted.push(m) };
  }
}
scope.AudioWorkletProcessor = AudioWorkletProcessor;
scope.registerProcessor = (name, cls) => { scope.cls = cls; scope.name = name; };
scope.currentFrame = 0;
scope.sampleRate = 48000;
scope.posted = [];
vm.createContext(scope);
vm.runInContext(src, scope);

check("registers as scterm-pcm", scope.name, "scterm-pcm");

const P = scope.cls;
const OPTIONS = { processorOptions: { targetFrames: 4608, maxFrames: 11520 } };

// One 20ms burst of s16le stereo 48kHz: 960 frames, 3840 bytes -- exactly what
// the server sends (web.go pushes the decoded PCM straight through).
const BURST_FRAMES = 960;
function burst(seed) {
  const buf = new ArrayBuffer(BURST_FRAMES * 4);
  const v = new Int16Array(buf);
  for (let i = 0; i < BURST_FRAMES; i++) {
    v[i * 2] = ((i + seed) % 1000) - 500;       // left
    v[i * 2 + 1] = -(((i + seed) % 1000) - 500); // right
  }
  return buf;
}

function run(blocks) {
  const p = new P(OPTIONS);
  const out = [];
  for (let b = 0; b < blocks; b++) {
    let threw = null;
    try { p.port.onmessage({ data: { pcm: burst(b * 7) } }); } catch (e) { threw = String(e); }
    if (threw) return { threw, out, p }; // report it, do not crash the run
    const left = new Float32Array(128), right = new Float32Array(128);
    p.process([], [[left, right]]);
    out.push([left, right]);
  }
  return { threw: null, out, p };
}

console.log("feeding PCM");
{
  const r = run(8); // 160ms, past the 96ms start threshold
  check("a burst is accepted, not thrown away", r.threw, null);
  const { p } = r;
  check("the ring buffer holds the audio", p.count > 0, true);
  check("and it decided to play", p.started, true);

  // The samples must come back out unchanged, scaled to float.
  const p2 = new P(OPTIONS);
  p2.port.onmessage({ data: { pcm: burst(0) } });
  for (let i = 0; i < 6; i++) p2.port.onmessage({ data: { pcm: burst(0) } });
  const left = new Float32Array(128), right = new Float32Array(128);
  p2.process([], [[left, right]]);
  const want = (((0) % 1000) - 500) / 32768;
  check("left channel is the sample the host sent", Math.abs(left[0] - want) < 1e-6, true);
  check("right channel too (inverted by the test data)", Math.abs(right[0] + want) < 1e-6, true);
  check("audio is not silence", left.some((v) => v !== 0), true);
}

console.log("buffering");
{
  // Below the target the worklet stays quiet on purpose: starting early would
  // underrun immediately and click.
  const p = new P(OPTIONS);
  p.port.onmessage({ data: { pcm: burst(0) } }); // 960 frames < 4608
  const left = new Float32Array(128), right = new Float32Array(128);
  p.process([], [[left, right]]);
  check("under the target it plays silence", left.every((v) => v === 0), true);
  check("and has not started", p.started, false);
}

console.log("sizes and edge cases");
{
  // Every burst size the server can actually produce must be handled: whole
  // frames only, and a buffer that is not a multiple of four bytes is dropped
  // rather than throwing.
  const sizes = [4, 8, 384, 3840, 3844, 16384];
  let bad = null;
  for (const bytes of sizes) {
    const p = new P(OPTIONS);
    try {
      p.port.onmessage({ data: { pcm: new ArrayBuffer(bytes) } });
      p.process([], [[new Float32Array(128), new Float32Array(128)]]);
    } catch (e) { bad = bytes + ": " + e; }
  }
  check("no burst size throws", bad, null);

  const p = new P(OPTIONS);
  let threw = null;
  try {
    p.port.onmessage({ data: {} });
    p.port.onmessage({ data: { pcm: new ArrayBuffer(2) } }); // half a frame
    p.port.onmessage({ data: null });
  } catch (e) { threw = String(e); }
  check("empty and odd payloads are ignored", threw, null);

  // Overflow: the host running ahead must drop the oldest audio, never grow.
  const q = new P(OPTIONS);
  for (let i = 0; i < 40; i++) q.port.onmessage({ data: { pcm: burst(i) } });
  check("the buffer is capped at maxFrames", q.count <= 11520, true);
  check("overflow is counted, not hidden", q.dropped > 0, true);
}

console.log("underruns");
{
  // Drain everything, then ask for more: the worklet pads with silence and
  // counts one underrun per starved block instead of stalling.
  const p = new P(OPTIONS);
  for (let i = 0; i < 6; i++) p.port.onmessage({ data: { pcm: burst(0) } });
  let guard = 0;
  while (p.count > 0 && guard++ < 200) {
    p.process([], [[new Float32Array(128), new Float32Array(128)]]);
  }
  const before = p.underruns;
  const left = new Float32Array(128), right = new Float32Array(128);
  p.process([], [[left, right]]);
  check("a starved block is silence", left.every((v) => v === 0), true);
  check("and is counted as an underrun", p.underruns, before + 1);
}

console.log("stats reporting");
{
  scope.posted.length = 0;
  scope.currentFrame = 0;
  const p = new P(OPTIONS);
  p.port.onmessage({ data: { pcm: burst(0) } });
  // The worklet reports at most once a second, keyed off currentFrame.
  for (let i = 0; i < 400; i++) {
    scope.currentFrame += 128;
    p.process([], [[new Float32Array(128), new Float32Array(128)]]);
  }
  const stats = scope.posted.filter((m) => m && m.stats);
  check("stats are reported about once a second", stats.length >= 1 && stats.length <= 3, true);
  check("stats carry the queue depth", typeof stats[0].stats.queued, "number");
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
