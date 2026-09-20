// scterm PCM sink: raw interleaved s16le stereo 48 kHz in, floats out.
//
// This is the whole audio path: no Opus, no decoder, no jitter buffer. Audio
// arrives from the host every ~20 ms and is played from a ring buffer that
// holds a target of ~96 ms, which absorbs host-side scheduling jitter without
// adding perceptible delay. Underruns are padded with silence (never a stall)
// and reported so the HUD can show them.

class SctermPCM extends AudioWorkletProcessor {
  constructor(options) {
    super();
    const opt = (options && options.processorOptions) || {};
    this.target = opt.targetFrames || 4608;   // 96 ms @ 48 kHz
    this.max = opt.maxFrames || 11520;        // 240 ms
    this.buf = new Float32Array(this.max * 2); // interleaved stereo
    this.read = 0;
    this.write = 0;
    this.count = 0; // frames available
    this.started = false;
    this.underruns = 0;
    this.dropped = 0;
    this.port.onmessage = (e) => this.feed(e.data);
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
    const left = out[0];
    const right = out[1] || out[0];
    const frames = left.length;

    if (!this.started || this.count === 0) {
      left.fill(0);
      if (right !== left) right.fill(0);
      if (this.started && this.count === 0) this.underruns++;
      this.report();
      return true;
    }

    for (let i = 0; i < frames; i++) {
      if (this.count === 0) {
        this.underruns++;
        left[i] = 0;
        right[i] = 0;
        continue;
      }
      const r = this.read * 2;
      left[i] = this.buf[r];
      right[i] = this.buf[r + 1];
      this.read = (this.read + 1) % this.max;
      this.count--;
    }
    this.report();
    return true;
  }

  report() {
    const now = currentFrame;
    if (now - this.reportAt < sampleRate) return; // ~1 Hz
    this.reportAt = now;
    this.port.postMessage({
      stats: { underruns: this.underruns, dropped: this.dropped, queued: this.count },
    });
  }
}

registerProcessor("scterm-pcm", SctermPCM);
