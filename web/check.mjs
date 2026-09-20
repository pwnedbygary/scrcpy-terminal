#!/usr/bin/env node
// Automated check for a running scterm --web / --window instance.
//
// It drives the player exactly like the browser does (same hello, same event
// shapes) using a hand-rolled websocket client, then asserts on what came
// back: decodable JPEG frames with the negotiated geometry, PCM, status
// messages, and control replies. This is the end-to-end proof that the display
// path works; it needs no browser and no DOM.
//
//   node web/check.mjs [host:port] [seconds]
//
// Exit code 0 = healthy, 1 = something is wrong (details on stdout).

import { createHash } from "node:crypto";
import net from "node:net";

const target = process.argv[2] || "127.0.0.1:6969";
const seconds = Number(process.argv[3] || 5);
// "raw" requests BGR0 instead of JPEG so the pixels themselves can be checked
// (length, letterbox bars, plausibility); "auto"/"jpeg" exercise the normal
// path. Decodable JPEGs alone cannot prove the host sent real imagery.
const mode = process.argv[4] || "auto";
const [host, port] = target.split(":");

// ---------------------------------------------------------------------------
// minimal websocket client (masked frames, like a browser)
// ---------------------------------------------------------------------------

function connectWS(host, port, path = "/ws") {
  return new Promise((resolve, reject) => {
    const sock = net.connect(Number(port), host, () => {
      const key = Buffer.from(crypto.getRandomValues(new Uint8Array(16))).toString("base64");
      sock.write(
        `GET ${path} HTTP/1.1\r\nHost: ${host}:${port}\r\n` +
          `Upgrade: websocket\r\nConnection: Upgrade\r\n` +
          `Sec-WebSocket-Key: ${key}\r\nSec-WebSocket-Version: 13\r\n` +
          `Origin: http://${host}:${port}\r\n\r\n`
      );
      expectKey = key;
    });
    let expectKey = "";
    let buf = Buffer.alloc(0);
    let upgraded = false;

    sock.on("error", reject);
    sock.on("data", (chunk) => {
      buf = Buffer.concat([buf, chunk]);
      if (!upgraded) {
        const end = buf.indexOf("\r\n\r\n");
        if (end < 0) return;
        const head = buf.subarray(0, end).toString("latin1");
        buf = buf.subarray(end + 4);
        if (!/^HTTP\/1\.1 101/.test(head)) {
          reject(new Error("handshake failed:\n" + head));
          return;
        }
        const m = /sec-websocket-accept: *(\S+)/i.exec(head);
        const want = createHash("sha1")
          .update(expectKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
          .digest("base64");
        if (!m || m[1] !== want) {
          reject(new Error(`bad Sec-WebSocket-Accept: ${m && m[1]} != ${want}`));
          return;
        }
        upgraded = true;
        resolve(api);
        drain();
      } else {
        drain();
      }
    });

    const handlers = { text: [], binary: [] };
    function drain() {
      for (;;) {
        if (buf.length < 2) return;
        const fin = (buf[0] & 0x80) !== 0;
        const op = buf[0] & 0x0f;
        let len = buf[1] & 0x7f;
        let off = 2;
        if (len === 126) {
          if (buf.length < 4) return;
          len = buf.readUInt16BE(2);
          off = 4;
        } else if (len === 127) {
          if (buf.length < 10) return;
          len = Number(buf.readBigUInt64BE(2));
          off = 10;
        }
        const masked = (buf[1] & 0x80) !== 0;
        if (masked) {
          if (buf.length < off + 4) return;
          off += 4;
        }
        if (buf.length < off + len) return;
        const payload = buf.subarray(off, off + len);
        buf = buf.subarray(off + len);
        if (!fin) throw new Error("fragmented server frame");
        if (op === 0x01) handlers.text.forEach((h) => h(payload.toString("utf8")));
        else if (op === 0x02) handlers.binary.forEach((h) => h(Buffer.from(payload)));
        else if (op === 0x09) sendFrame(0x0a, payload); // ping -> pong
      }
    }

    function sendFrame(op, payload) {
      const mask = crypto.getRandomValues(new Uint8Array(4));
      const body = Buffer.from(payload);
      for (let i = 0; i < body.length; i++) body[i] ^= mask[i & 3];
      let head;
      if (body.length < 126) head = Buffer.from([0x80 | op, 0x80 | body.length]);
      else if (body.length <= 0xffff) {
        head = Buffer.alloc(4);
        head[0] = 0x80 | op;
        head[1] = 0x80 | 126;
        head.writeUInt16BE(body.length, 2);
      } else {
        head = Buffer.alloc(10);
        head[0] = 0x80 | op;
        head[1] = 0x80 | 127;
        head.writeBigUInt64BE(BigInt(body.length), 2);
      }
      sock.write(Buffer.concat([head, Buffer.from(mask), body]));
    }

    const api = {
      send(obj) {
        sendFrame(0x01, Buffer.from(JSON.stringify(obj)));
      },
      onText(fn) {
        handlers.text.push(fn);
      },
      onBinary(fn) {
        handlers.binary.push(fn);
      },
      close() {
        sendFrame(0x08, Buffer.from([0x03, 0xe8]));
        sock.end();
      },
    };
  });
}

// ---------------------------------------------------------------------------
// check
// ---------------------------------------------------------------------------

const problems = [];
const note = (msg) => console.log("  " + msg);
const bad = (msg) => {
  problems.push(msg);
  console.log("  FAIL " + msg);
};

const base = `http://${host}:${port}`;
const assets = ["/", "/player.js", "/pointer.js", "/player.css", "/pcm-worklet.js"];
console.log(`checking ${base}`);

for (const path of assets) {
  const res = await fetch(base + path);
  const body = await res.text();
  if (!res.ok) bad(`GET ${path} -> ${res.status}`);
  else note(`GET ${path} ${res.status} ${body.length} bytes (${res.headers.get("content-type")})`);
  if (path === "/" && !body.includes("sctermPlayer") && !body.includes("player.js")) {
    bad("index.html does not load the player");
  }
}

const ws = await connectWS(host, port);
note("websocket upgraded");

let frames = 0;
let jpegOK = 0;
let jpegBad = 0;
let bytes = 0;
let seq = 0;
let lost = 0;
let audioBytes = 0;
let canvasW = 0;
let canvasH = 0;
let fps = 0;
let statuses = 0;
let lastJpegMs = 0;
let helloSeen = false;
let firstSize = null;
const starts = [];

// Content validation state (raw/BGR0 mode only).
//
// Decodability is NOT correctness: a JPEG with valid SOI/EOI markers and the
// right dimensions still passed while the host was encoding uninitialized heap
// past the end of an undersized frame buffer. In raw mode the payload length is
// pinned to w*h*4 and the pixels are inspected, so a short buffer or a buffer
// that cannot fill the canvas is caught instead of silently believed.
let rawLenBad = 0;
let rawChecked = 0;
let rawFlat = 0;
let barBad = 0;
let geometryMismatch = 0;
let lastFitW = 0;
let lastFitH = 0;
let audioPeak = 0;
let hostAudio = false;

function checkRawFrame(buf, w, h) {
  rawChecked++;
  const body = buf.subarray(16);
  const want = w * h * 4;
  if (body.length !== want) {
    rawLenBad++;
    bad(`raw frame is ${body.length} bytes, want ${want} (${w}x${h} BGR0)`);
    return;
  }
  // The letterbox bars must be black. If the frame buffer were read past its
  // end, the bars would be whatever heap happened to contain.
  const fitW = lastFitW || w;
  const fitH = lastFitH || h;
  if (fitW < w || fitH < h) {
    for (let x = 0; x < w; x += 7) {
      for (const y of [0, h - 1]) {
        if (fitH === h) continue;
        const p = (y * w + x) * 4;
        if (body[p] | body[p + 1] | body[p + 2]) {
          barBad++;
          bad(`letterbox bar at ${x},${y} is not black (${body[p]},${body[p + 1]},${body[p + 2]})`);
          return;
        }
      }
    }
  }
  // Plausibility: a real screen has many distinct colours. A zeroed or
  // constant buffer is not a screen.
  const seen = new Set();
  const step = Math.max(1, Math.floor((w * h) / 2000)) * 4;
  for (let p = 0; p + 2 < body.length; p += step) {
    seen.add((body[p] << 16) | (body[p + 1] << 8) | body[p + 2]);
    if (seen.size > 64) break;
  }
  if (seen.size <= 1) {
    rawFlat++;
    bad(`raw frame is a single flat colour (${seen.size} distinct sampled colours)`);
  }
}

ws.onText((txt) => {
  let msg;
  try {
    msg = JSON.parse(txt);
  } catch {
    return;
  }
  if (msg.op === "hello") {
    helloSeen = true;
    note(`hello: device=${msg.device} proto=${msg.proto} version=${msg.version} audio=${msg.audio}`);
  } else if (msg.op === "status") {
    statuses++;
    canvasW = msg.canvasW;
    canvasH = msg.canvasH;
    fps = msg.fps;
    lastJpegMs = msg.jpegMs;
    audioPeak = msg.audioPeak || 0;
    hostAudio = !!msg.hostAudio;
    if (statuses === 1) {
      note(
        `status: video=${msg.videoW}x${msg.videoH} canvas=${msg.canvasW}x${msg.canvasH} ` +
          `fit=${msg.fitW}x${msg.fitH} audio=${msg.audio} control=${msg.control}`
      );
    }
    lastFitW = msg.fitW;
    lastFitH = msg.fitH;
  }
});

ws.onBinary((buf) => {
  const kind = buf[0];
  if (kind === 0x01) {
    frames++;
    bytes += buf.length;
    const seqNo = buf.readUInt32BE(1);
    const w = buf.readUInt16BE(5);
    const h = buf.readUInt16BE(7);
    const format = buf[11];
    if (!firstSize) firstSize = [w, h];
    if (seq && seqNo !== seq + 1) lost += seqNo - seq - 1;
    seq = seqNo;
    starts.push(performance.now());

    // The payload geometry must match the canvas the status message advertises:
    // a client told one size while receiving another is how a mismatched frame
    // buffer stays invisible.
    if (canvasW && canvasH && (w !== canvasW || h !== canvasH)) {
      geometryMismatch++;
      bad(`frame is ${w}x${h} but the status canvas is ${canvasW}x${canvasH}`);
    }

    if (format === 1) {
      checkRawFrame(buf, w, h);
    } else {
      const body = buf.subarray(16);
      if (body[0] === 0xff && body[1] === 0xd8 && body[body.length - 2] === 0xff && body[body.length - 1] === 0xd9) {
        jpegOK++;
      } else {
        jpegBad++;
      }
    }
  } else if (kind === 0x02) {
    audioBytes += buf.length - 1;
  }
});

ws.send({ op: "hello", w: 480, h: 1080, format: mode, caps: "audio" });

// exercise the control path: these must not error or wedge the connection
setTimeout(() => ws.send({ op: "down", x: 32767, y: 32767 }), 700);
setTimeout(() => ws.send({ op: "move", x: 33000, y: 33000 }), 800);
setTimeout(() => ws.send({ op: "up", x: 33000, y: 33000 }), 900);
setTimeout(() => ws.send({ op: "wheel", x: 20000, y: 20000, delta: 1 }), 1000);
setTimeout(() => ws.send({ op: "ping" }), 1100);

await new Promise((r) => setTimeout(r, seconds * 1000));
ws.close();

console.log("");
if (!helloSeen) bad("no hello message");
if (statuses === 0) bad("no status messages");
if (frames === 0) bad("no video frames arrived");
if (jpegBad > 0) bad(`${jpegBad} of ${frames} frames were not valid JPEG`);if (lost > 0 && lost > frames) bad(`${lost} frames lost`);
if (audioBytes === 0) bad("no audio PCM arrived (device may be silent, or -audio=false)");
if (canvasW < 2 || canvasH < 2) bad("canvas geometry never established");
if (firstSize && (firstSize[0] !== canvasW || firstSize[1] !== canvasH)) {
  bad(`frame payload ${firstSize} does not match the canvas ${canvasW}x${canvasH}`);
}

const span = seconds;
note(
  `result: ${frames} frames (${jpegOK} valid JPEG), ${(bytes / 1024).toFixed(0)} KB ` +
    `(${(bytes / span / 1024).toFixed(0)} KB/s), canvas ${canvasW}x${canvasH}, ` +
    `audio ${(audioBytes / 1024).toFixed(0)} KB, ${statuses} status, lost=${lost}`
);
if (mode === "raw") {
  note(
    `raw content: ${rawChecked} frames inspected, ${rawLenBad} wrong length, ` +
      `${barBad} non-black bars, ${rawFlat} flat`
  );
  if (rawChecked === 0) bad("raw mode requested but no raw frames arrived");
}
if (frames) {
  note(`average frame interval ${(span * 1000) / frames >= 0 ? ((span * 1000) / frames).toFixed(1) : "?"} ms (device-side pacing)`);
  if (lastJpegMs > 0) {
    note(
      `host jpeg encode ${lastJpegMs.toFixed(2)} ms/frame (ewma) -> ceiling ` +
        `~${Math.floor(1000 / lastJpegMs)} fps single-threaded`
    );
  }
}
if (geometryMismatch > 0) {
  bad(`${geometryMismatch} frames disagreed with the advertised canvas geometry`);
}
if (audioBytes > 0) {
  note(
    `audio: ${hostAudio ? "host sink AND browser" : "browser only (host sink stays silent)"}; ` +
      `device peak ${audioPeak}${audioPeak === 0 ? " (device is sending digital silence)" : ""}`
  );
}
if (canvasW && canvasH) {
  const expect = mode === "raw" ? canvasW * canvasH * 4 : null;
  if (expect !== null && rawLenBad === 0 && rawChecked > 0) {
    note(`raw payload length is exactly ${expect} bytes (${canvasW}x${canvasH} BGR0)`);
  }
}

if (problems.length) {
  console.log(`\n${problems.length} problem(s):`);
  for (const p of problems) console.log("  - " + p);
  process.exit(1);
}
console.log("\nALL CHECKS PASSED");
