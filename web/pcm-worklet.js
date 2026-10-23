// scterm PCM sink: raw interleaved s16le stereo 48 kHz in, floats out.
//
// This is the whole audio path: no Opus, no decoder, no jitter buffer. Audio
// arrives from the host every ~20 ms and is played from a ring buffer that
// holds a target of ~96 ms, which absorbs host-side scheduling jitter without
// adding perceptible delay. Underruns are padded with silence (never a stall)
// and reported so the HUD can show them.
//
// The page also loads this file as a classic script, before player.js. An
// AudioWorklet exists only in secure contexts (https, localhost), so a page
// opened over plain http from another machine has none; player.js then drains
// this same ring from a ScriptProcessorNode on the main thread, where there is
// no AudioWorkletProcessor to extend and nothing to register.
//
// The page asks for a 48 kHz context, so the ring normally plays frame for
// frame. A browser that runs the context at another rate anyway (an older
// Safari's webkitAudioContext takes no options) would play the stream slow or
// fast, and the host would run ahead of it; processorOptions.ratio, the
// stream's frames per output frame, makes pull() resample instead.
"use strict";

const SctermPCMBase = typeof AudioWorkletProcessor === "function" ? AudioWorkletProcessor : class {};

class SctermPCM extends SctermPCMBase {
  constructor(options) {
    super();
    const opt = (options && options.processorOptions) || {};
    this.target = opt.targetFrames || 4608;   // 96 ms @ 48 kHz
    this.max = opt.maxFrames || 11520;        // 240 ms
    this.ratio = opt.ratio > 0 ? opt.ratio : 1; // stream frames per output frame
    this.frac = 0;                             // how far past `read` playback is
    this.buf = new Float32Array(this.max * 2); // interleaved stereo
    this.read = 0;
    this.write = 0;
    this.count = 0; // frames available
    this.started = false;
    this.underruns = 0;
    this.dropped = 0;
    if (this.port) this.port.onmessage = (e) => this.feed(e.data);
    this.reportAt = 0;
  }

  feed(data) {
    if (!data || !data.pcm) return;
    const bytes = data.pcm;
    // Whole stereo frames: 4 bytes each (s16 left + s16 right). This used to be
    // byteLength >> 1, i.e. samples rather than frames, so the Int16Array below
    // asked for twice as many elements as the buffer holds and threw
    // "RangeError: Invalid typed array length" on EVERY burst. The processor
    // survived the throw, so the ring buffer simply stayed empty: the browser
    // showed a live audio stream (and a PulseAudio sink-input) while playing
    // nothing but silence.
    const n = bytes.byteLength >> 2;
    if (n <= 0) return;
    const src = new Int16Array(bytes, 0, n * 2);
    const scale = 1 / 32768;
    for (let i = 0; i < n; i++) {
      if (this.count >= this.max) {
        // The host is running ahead of real time: drop the oldest frame.
        this.read = (this.read + 1) % this.max;
        this.count--;
        this.dropped++;
      }
      const w = this.write * 2;
      this.buf[w] = src[i * 2] * scale;
      this.buf[w + 1] = src[i * 2 + 1] * scale;
      this.write = (this.write + 1) % this.max;
      this.count++;
    }
    if (!this.started && this.count >= this.target) this.started = true;
  }

  process(inputs, outputs) {
    const out = outputs[0];
    this.pull(out[0], out[1] || out[0]);
    this.report();
    return true;
  }

  // pull plays the next left.length frames into left and right (the same
  // array for both on a mono output), as silence while buffering or starved.
  pull(left, right) {
    const frames = left.length;

    if (!this.started || this.count === 0) {
      this.starve(left, right, 0);
      return;
    }
    if (this.ratio !== 1) {
      this.resample(left, right);
      return;
    }

    for (let i = 0; i < frames; i++) {
      if (this.count === 0) {
        this.starve(left, right, i);
        return;
      }
      const r = this.read * 2;
      left[i] = this.buf[r];
      right[i] = this.buf[r + 1];
      this.read = (this.read + 1) % this.max;
      this.count--;
    }
  }

  // starve plays silence from frame `from` to the end of the block: nothing
  // arrives while a block plays. A block the stream could not fill is one
  // underrun, however many frames it lacked, so the count means the same
  // whether or not the ring resamples; before playback starts it is only
  // buffering.
  starve(left, right, from) {
    left.fill(0, from);
    if (right !== left) right.fill(0, from);
    if (this.started) this.underruns++;
  }

  // resample plays the stream at `ratio` stream frames per output frame, each
  // output frame a straight line between the two stream frames it falls
  // between. It needs both, so the last frame waits for the next burst.
  resample(left, right) {
    for (let i = 0; i < left.length; i++) {
      while (this.frac >= 1 && this.count > 1) {
        this.frac -= 1;
        this.read = (this.read + 1) % this.max;
        this.count--;
      }
      if (this.count < 2) {
        this.starve(left, right, i);
        return;
      }
      const a = this.read * 2;
      const b = ((this.read + 1) % this.max) * 2;
      const f = this.frac;
      left[i] = this.buf[a] + (this.buf[b] - this.buf[a]) * f;
      right[i] = this.buf[a + 1] + (this.buf[b + 1] - this.buf[a + 1]) * f;
      this.frac += this.ratio;
    }
  }

  stats() {
    return { underruns: this.underruns, dropped: this.dropped, queued: this.count };
  }

  report() {
    const now = currentFrame;
    if (now - this.reportAt < sampleRate) return; // ~1 Hz
    this.reportAt = now;
    this.port.postMessage({ stats: this.stats() });
  }
}

if (typeof registerProcessor === "function") registerProcessor("scterm-pcm", SctermPCM);
