#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
scterm — control an Android device, fast. (scrcpy, but text — and web.)

Two front-ends share one high-performance capture pipeline:

  device screen --adb screenrecord--> H264 --ffmpeg--> MJPEG frames
       ├──[ TUI ]  numpy vectorised half-block renderer, incremental redraw
       └──[ WEB ]  single persistent multipart MJPEG stream, zero re-encode

Running modes (pick one, two or all, at launch):
    scterm.py --tui                 terminal UI only (default when on a TTY)
    scterm.py --web [PORT]          web viewer only (headless server)
    scterm.py --tui --web 8000      both together
    scterm.py --both 8000           shorthand for the above

The web viewer works from any browser — phone over Tailscale, desktop over
LAN — and exposes a clean JSON API (see README) that is the natural surface
for a future native mobile app.

Dependencies: python3, Pillow, adb + an authorized device.  numpy + ffmpeg
are optional but strongly recommended (numpy vectorises the renderer,
ffmpeg unlocks the 30-60 fps H264 path; without them a raw 'screencap'
fallback runs at ~5-15 fps).

Controls (TUI):
    Mouse left-click ... tap            Mouse drag ......... swipe
    Mouse wheel ........ scroll         Type ................ text (Enter sends)
    F1 menu  F2 home  F3 back  F4 recents     F5 power  F6 vol+  F7 vol-
    F8 center  F9-12 dpad  Arrows dpad        Esc / Ctrl-C ... quit (Esc twice)
    ? ................. help overlay    Ctrl-T ............ menu overlay
    Ctrl-F ............ fit mode       Ctrl-S / F12 ...... stream ↔ screencap
    Ctrl-Alt-G ........ grab (pointer-lock style key pass-through, Zellij-friendly)

Props to the original scrcpy project — this is a text/streaming homage.
"""

import argparse
import atexit
import io
import json
import os
import re
import select
import shutil
import signal
import subprocess
import sys
import termios
import threading
import time
import tty
import urllib.parse
from collections import deque

try:
    import numpy as np
    HAVE_NUMPY = True
except Exception:
    HAVE_NUMPY = False

from PIL import Image

VERSION = "2.0.0"
BLOCK = "▀"  # half-block: top pixel = fg, bottom pixel = bg

# ---------------------------------------------------------------------------
# palette / terminal helpers
# ---------------------------------------------------------------------------

# theme
ACCENT = (34, 211, 238)      # cyan
ACCENT2 = (167, 139, 250)    # violet
GOOD = (52, 211, 153)        # green
WARN = (251, 191, 36)        # amber
DANGER = (248, 113, 113)     # red
BAR_BG = (13, 17, 23)
PANEL_BG = (17, 24, 32)
BORDER = (31, 41, 55)
TEXT = (229, 231, 235)
DIM = (107, 114, 128)

# Android keycodes
KEY = {
    "MENU": 82, "HOME": 3, "BACK": 4, "APP_SWITCH": 187, "POWER": 26,
    "VOL_UP": 24, "VOL_DOWN": 25, "CENTER": 23, "ENTER": 66, "TAB": 61,
    "DPAD_UP": 19, "DPAD_DOWN": 20, "DPAD_LEFT": 21, "DPAD_RIGHT": 22,
    "DEL": 67, "ESC": 111, "WAKEUP": 224,
}

FIT_MODES = ["contain", "cover", "fill"]
SPARK = "▁▂▃▄▅▆▇█"


def supports_truecolor():
    ct = os.environ.get("COLORTERM", "").lower()
    return "truecolor" in ct or "24bit" in ct


def rgb256(r, g, b):
    """Map an (r,g,b) triplet to the nearest xterm-256 palette index."""
    if r == g == b:
        if r < 8:
            return 16
        if r > 248:
            return 231
        return round((r - 8) / 247 * 24) + 232
    return (16 + 36 * round(r / 255 * 5)
            + 6 * round(g / 255 * 5) + round(b / 255 * 5))


def _sg(rgb, fg=True):
    r, g, b = rgb
    return f"\x1b[{'38' if fg else '48'};2;{r};{g};{b}m"


def style(text, fg=None, bg=None, bold=False, dim=False):
    out = []
    if bold:
        out.append("\x1b[1m")
    if dim:
        out.append("\x1b[2m")
    if fg:
        out.append(_sg(fg, True))
    if bg:
        out.append(_sg(bg, False))
    out.append(text)
    out.append("\x1b[0m")
    return "".join(out)


_SGR_RE = re.compile(r"\x1b\[[0-9;]*m")


def vlen(s):
    """Visible cell width of a (possibly styled) string."""
    return len(_SGR_RE.sub("", s))


def fit_line(parts, width):
    """Join styled parts, truncating at exactly `width` visible cells
    without ever splitting an escape sequence."""
    out = []
    vis = 0
    for p in parts:
        plen = vlen(p)
        if vis + plen > width:
            out.append("\x1b[0m")
            break
        out.append(p)
        vis += plen
    return "".join(out) + " " * (width - vis)


# ---------------------------------------------------------------------------
# fast adb input channel: one persistent `adb shell`, zero per-key subprocess
# ---------------------------------------------------------------------------

class AdbInput:
    """Persistent `adb shell` pipe — input latency drops from ~80ms to ~2ms.

    Fire-and-forget commands; the remote shell is kept alive and reused for
    every tap / swipe / key / text event. Auto-reconnects if adb drops it.
    """

    def __init__(self, serial):
        self.serial = serial
        self.lock = threading.Lock()
        self.proc = None

    def _base(self):
        cmd = ["adb"]
        if self.serial:
            cmd += ["-s", self.serial]
        return cmd

    def _ensure(self):
        if self.proc and self.proc.poll() is None:
            return True
        try:
            self.proc = subprocess.Popen(
                self._base() + ["shell"],
                stdin=subprocess.PIPE, stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL)
        except Exception:
            self.proc = None
            return False
        return self.proc.poll() is None

    def close(self):
        with self.lock:
            if self.proc:
                try:
                    self.proc.terminate()
                except Exception:
                    pass
                self.proc = None

    def send(self, cmd):
        """Send one shell command. Returns True if it was accepted."""
        with self.lock:
            if not self._ensure():
                return False
            try:
                self.proc.stdin.write(cmd.encode() + b"\n")
                self.proc.stdin.flush()
                return True
            except Exception:
                self.proc = None
                return False

    def send_retry(self, cmd):
        if self.send(cmd):
            return True
        return self.send(cmd)  # one reconnect attempt


def shell_cmd(serial, *args):
    """One-shot `adb shell` used for rare/status commands (battery, model...)."""
    cmd = ["adb"]
    if serial:
        cmd += ["-s", serial]
    cmd += ["shell", *args]
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=15)
        return p.stdout
    except Exception:
        return ""


def tap(inp, x, y):
    inp.send_retry(f"input tap {int(round(x))} {int(round(y))}")


def swipe(inp, x1, y1, x2, y2, ms=90):
    inp.send_retry(f"input swipe {int(round(x1))} {int(round(y1))} "
                   f"{int(round(x2))} {int(round(y2))} {int(ms)}")


def key(inp, code):
    inp.send_retry(f"input keyevent {int(code)}")


def touch_down(inp, x, y):
    inp.send_retry(f"input motionevent DOWN {int(round(x))} {int(round(y))}")


def touch_move(inp, x, y):
    inp.send_retry(f"input motionevent MOVE {int(round(x))} {int(round(y))}")


def touch_up(inp, x, y):
    inp.send_retry(f"input motionevent UP {int(round(x))} {int(round(y))}")


def send_text(inp, s, chunk=180):
    """Send text through the persistent shell. `input text` chokes on long
    strings and on some characters, so chunk + shell-quote defensively."""
    if not s:
        return
    for part in [s[i:i + chunk] for i in range(0, len(s), chunk)]:
        esc = "'" + part.replace("'", "'\\''").replace(" ", "%s") + "'"
        inp.send_retry("input text " + esc)


# ---------------------------------------------------------------------------
# device info (one-shot, low frequency)
# ---------------------------------------------------------------------------

def device_size(serial):
    """Current (rotation-aware) display size in pixels.

    `wm size` reports the PHYSICAL panel size (e.g. 1080x1920 on a portrait
    phone) even when an app rotates the display to landscape.  screenrecord /
    screencap capture the CURRENT rotated buffer, so the fit maths, stream
    size and input mapping must use the rotated size (cur=WxH from the
    window manager) or the image is letterboxed and then squeezed sideways
    through portrait fit math.
    """
    out = shell_cmd(serial, "wm", "size")
    m = re.search(r"(\d+)x(\d+)", out)
    if not m:
        return None
    w, h = int(m.group(1)), int(m.group(2))
    cur = shell_cmd(serial, "dumpsys", "window")
    cm = re.search(r"cur=(\d+)x(\d+)", cur)
    if cm:
        w, h = int(cm.group(1)), int(cm.group(2))
    return w, h


def model_name(serial):
    out = shell_cmd(serial, "getprop", "ro.product.model").strip()
    return out or serial or "?"


def device_status(serial):
    """Battery level + screen wakefulness, best effort."""
    out = shell_cmd(serial, "dumpsys", "battery")
    level = status = None
    for line in out.splitlines():
        line = line.strip()
        if line.startswith("level:"):
            try:
                level = int(line.split(":", 1)[1].strip())
            except Exception:
                pass
        elif line.startswith("status:"):
            try:
                status = int(line.split(":", 1)[1].strip())
            except Exception:
                pass
    charging = status == 2
    wake = "mWakefulness=" in shell_cmd(serial, "dumpsys", "power")
    return level, charging, wake


def wake_unlock(serial, stay_awake=True):
    shell_cmd(serial, "input", "keyevent", str(KEY["WAKEUP"]))
    shell_cmd(serial, "wm", "dismiss-keyguard")
    if stay_awake:
        shell_cmd(serial, "svc", "power", "stayon", "true")


def set_stay_awake(serial, on):
    shell_cmd(serial, "svc", "power", "stayon", "true" if on else "false")


def set_rotation(serial, deg):
    """Best-effort rotation lock (0/90/180/270). Falls back silently."""
    idx = {0: 0, 90: 1, 180: 2, 270: 3}.get(deg % 360, 0)
    shell_cmd(serial, "settings", "put", "system", "accelerometer_rotation", "0")
    shell_cmd(serial, "settings", "put", "system", "user_rotation", str(idx))


# ---------------------------------------------------------------------------
# device discovery
# ---------------------------------------------------------------------------

def detect_serial():
    p = subprocess.run(["adb", "devices"], capture_output=True, text=True)
    for line in p.stdout.splitlines()[1:]:
        parts = line.split()
        if len(parts) == 2 and parts[1] == "device":
            return parts[0]
    return None


def list_adb_devices():
    try:
        p = subprocess.run(["adb", "devices"], capture_output=True,
                           text=True, timeout=3)
        out = []
        for line in p.stdout.splitlines()[1:]:
            parts = line.split()
            if len(parts) >= 2:
                out.append([parts[0], parts[1]])
        return out
    except Exception:
        return []


def get_local_subnet():
    import socket
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
        s.close()
        return ".".join(ip.split(".")[:3]) + ".0/24"
    except Exception:
        return "192.168.1.0/24"


def scan_wifi_adb(timeout=0.3):
    """Scan for adb-over-WiFi (port 5555) hosts + Tailscale peers."""
    import socket
    import ipaddress
    import concurrent.futures
    found = []
    try:
        p = subprocess.run(["tailscale", "status", "--json"],
                           capture_output=True, text=True, timeout=2)
        if p.stdout:
            j = json.loads(p.stdout)
            for v in j.get("Peer", {}).values():
                for ip in v.get("TailscaleIPs", []):
                    if "/" not in ip:
                        found.append([ip, "tailscale"])
    except Exception:
        pass
    try:
        net = ipaddress.ip_network(get_local_subnet(), strict=False)

        def probe(ip):
            try:
                s = socket.create_connection((str(ip), 5555), timeout=timeout)
                s.close()
                return str(ip)
            except Exception:
                return None

        hosts = list(net.hosts())[:254]
        with concurrent.futures.ThreadPoolExecutor(max_workers=40) as ex:
            futures = [ex.submit(probe, h) for h in hosts]
            for f in futures:
                r = f.result()
                if r:
                    found.append([r, "wifi"])
    except Exception:
        pass
    return found[:30]


def _has(cmd):
    return shutil.which(cmd) is not None


HAS_FFMPEG = _has("ffmpeg")
HAS_SCRCPY = _has("scrcpy")


# ---------------------------------------------------------------------------
# capture pipeline
# ---------------------------------------------------------------------------

def pick_stream_size(dev, max_w, max_h):
    """Constrain device size to max_w x max_h preserving aspect ratio
    (screenrecord distorts if you don't). Never upscale."""
    dw, dh = dev
    scale = min(1.0, max_w / dw, max_h / dh)
    w = max(2, int(dw * scale) & ~1)    # even, >= 2
    h = max(2, int(dh * scale) & ~1)
    return w, h


class ScreencapSource:
    """Raw-RGBA screencap loop — no ffmpeg needed. ~2-4x faster than
    `screencap -p` because the device skips PNG encoding. Falls back to PNG
    automatically if the raw buffer size doesn't match the expected WxH."""

    fast = False
    kind = "cap"

    def __init__(self, manager):
        self.m = manager
        self.stop = False
        self.size = None

    def _grab(self):
        m = self.m
        serial = m.serial
        if self.size is None:
            self.size = device_size(serial)
            if not self.size:
                self.size = (1080, 1920)
        w, h = self.size
        cmd = ["adb"]
        if serial:
            cmd += ["-s", serial]
        cmd += ["exec-out", "screencap"]
        try:
            p = subprocess.run(cmd, stdout=subprocess.PIPE,
                               stderr=subprocess.DEVNULL, timeout=20)
        except Exception:
            return None
        if p.returncode != 0 or not p.stdout:
            return None
        data = p.stdout
        # raw RGBA is w*h*4, optionally prefixed by a 16-byte header
        # (w, h, format, flags) on some devices
        if len(data) == w * h * 4:
            buf, img = data, (w, h)
        elif len(data) == w * h * 4 + 16:
            try:
                import struct
                hw, hh = struct.unpack("<II", data[:8])
            except Exception:
                hw = hh = 0
            if hw > 0 and hh > 0:
                buf, img = data[16:], (hw, hh)
            else:
                buf = img = None
        else:
            buf = img = None
        if buf is not None:
            try:
                return Image.frombytes("RGBA", img, buf, "raw",
                                       "RGBA", 0, 1).convert("RGB")
            except Exception:
                pass
        # buffer mismatch / PNG path
        try:
            im = Image.open(io.BytesIO(data)).convert("RGB")
            self.size = im.size
            return im
        except Exception:
            return None

    def run(self):
        while not self.stop:
            t0 = time.perf_counter()
            img = self._grab()
            if img is not None:
                self.m._publish(img)
            dt = time.perf_counter() - t0
            time.sleep(max(0.02, min(0.5, dt * 0.6)))

    def latest(self):
        return self.m.latest()

    def close(self):
        self.stop = True


class FfmpegSource:
    """screenrecord(H264) → ffmpeg → MJPEG frames.

    The browser consumes the JPEG bytes directly (zero re-encode); the TUI
    decodes them with PIL (with draft-downscale for cheap terminal-sized
    decodes). Parameter changes (bitrate / fps cap / quality / scale) are
    applied by signalling the pump loop to respawn the pipeline.
    """

    fast = True
    kind = "stream"

    def __init__(self, manager, w, h, bitrate, q, scale_pct, fps_cap):
        self.m = manager
        self.adb = None
        self.ff = None
        self._stop = False
        self.spawned_at = time.time()
        self.plock = threading.Lock()
        with self.plock:
            self.params = dict(w=w, h=h, bitrate=bitrate,
                               q=max(1, min(10, q)), scale=max(25, min(100, scale_pct)),
                               fps_cap=fps_cap)
        self.last_params = dict(self.params)

    # -- parameter updates (thread-safe) -----------------------------------
    def _update(self, **kw):
        with self.plock:
            for k, v in kw.items():
                if k in self.params:
                    self.params[k] = v

    def _needs_restart(self):
        with self.plock:
            return dict(self.params) != self.last_params

    def _snapshot(self):
        with self.plock:
            return dict(self.params)

    def set_bitrate(self, n):
        self._update(bitrate=max(500_000, min(200_000_000, int(n))))

    def set_fps_cap(self, n):
        self._update(fps_cap=max(0, min(120, int(n))))

    def set_quality(self, q):
        self._update(q=max(1, min(10, int(q))))

    def set_scale(self, pct):
        self._update(scale=max(25, min(100, int(pct))))

    # -- pipeline -----------------------------------------------------------
    def _spawn(self):
        p = self._snapshot()
        w, h = p["w"], p["h"]
        serial = self.m.serial
        adb_cmd = ["adb"]
        if serial:
            adb_cmd += ["-s", serial]
        adb_cmd += ["exec-out",
                    f"screenrecord --output-format=h264 --size {w}x{h}"
                    f" --bit-rate {p['bitrate']} --time-limit 179 -"]
        vf = []
        if p["fps_cap"]:
            vf += [f"fps={p['fps_cap']}"]
        if p["scale"] != 100:
            vf += [f"scale=trunc(iw*{p['scale']}/100/2)*2:"
                   f"trunc(ih*{p['scale']}/100/2)*2"]
        ff_cmd = ["ffmpeg", "-hide_banner", "-loglevel", "error",
                  "-strict", "unofficial",
                  "-flags", "low_delay", "-probesize", "32768",
                  "-f", "h264", "-i", "pipe:0"]
        if vf:
            ff_cmd += ["-vf", ",".join(vf)]
        ff_cmd += ["-an", "-c:v", "mjpeg", "-q:v", str(p["q"]),
                   "-f", "image2pipe", "pipe:1"]
        try:
            self.adb = subprocess.Popen(adb_cmd, stdout=subprocess.PIPE,
                                        stderr=subprocess.DEVNULL)
            self.ff = subprocess.Popen(ff_cmd, stdin=self.adb.stdout,
                                       stdout=subprocess.PIPE,
                                       stderr=subprocess.DEVNULL)
            if self.adb.stdout:
                self.adb.stdout.close()
        except Exception:
            self.adb = self.ff = None
        self.last_params = p
        self.spawned_at = time.time()

    def _cleanup(self):
        for p in (self.ff, self.adb):
            if p:
                try:
                    p.terminate()
                except Exception:
                    pass
        for p in (self.ff, self.adb):
            if p:
                try:
                    p.wait(timeout=1)
                except Exception:
                    try:
                        p.kill()
                    except Exception:
                        pass
        self.adb = self.ff = None

    def run(self):
        while not self._stop:
            self._spawn()
            if not self.ff:
                time.sleep(1.0)
                continue
            buf = b""
            idle = 0.0
            while not self._stop:
                r, _, _ = select.select([self.ff.stdout], [], [], 0.2)
                if self._needs_restart():
                    break
                if not r:
                    # screenrecord restarts itself every ~3 min; detect death
                    if self.ff.poll() is not None or self.adb.poll() is not None:
                        idle += 0.2
                        if idle > 1.0:
                            break
                    continue
                idle = 0.0
                chunk = self.ff.stdout.read(65536)
                if not chunk:
                    break
                buf += chunk
                # split complete JPEG frames (SOI…EOI)
                start_pos = 0
                while True:
                    s = buf.find(b"\xff\xd8", start_pos)
                    if s < 0:
                        break
                    e = buf.find(b"\xff\xd9", s + 2)
                    if e < 0:
                        break
                    frame = buf[s:e + 2]
                    buf = buf[e + 2:]
                    start_pos = 0
                    self.m._publish(frame)
            self._cleanup()
            if not self._stop and self._needs_restart():
                time.sleep(0.1)  # let the new screenrecord spin up
            elif not self._stop:
                time.sleep(0.4)

    def latest(self):
        return self.m.latest()

    def close(self):
        self._stop = True
        self._cleanup()


class CaptureManager:
    """Owns the current serial + capture source. Twos kinds of source run
    behind one thread; switching device or capture kind is a stop/start."""

    def __init__(self, serial, max_w, max_h, bitrate, q, scale, fps_cap,
                 stream_ok):
        self.lock = threading.Lock()
        self.serial = serial
        self.max_w = max_w
        self.max_h = max_h
        self.bitrate = bitrate
        self.q = q
        self.scale = scale
        self.fps_cap = fps_cap
        self.stream_ok = stream_ok  # ffmpeg available?
        self.image = None           # PIL Image OR jpeg bytes
        self.is_jpeg = False
        self.seq = 0
        self.first_pub = 0.0
        self.source = None
        self.thread = None
        self.inp = AdbInput(serial)
        self.info = {"model": "?", "battery": None, "charging": False,
                     "wake": True, "size": None}
        self._info_stop = False
        self._info_thread = None
        self._watch_stop = False
        self._watch_thread = None
        if serial:
            self._start_source()
            self._start_info()
        self._start_watchdog()

    # -- lifecycle ----------------------------------------------------------
    def _make_source(self):
        serial = self.serial
        size = None
        if self.stream_ok:
            # clear stale on-device screenrecord from hard-killed sessions
            shell_cmd(serial, "pkill", "-x", "screenrecord")
            time.sleep(0.25)
            size = device_size(serial)
        if size:
            self.info["size"] = size  # device size (input mapping needs it)
            w, h = pick_stream_size(size, self.max_w, self.max_h)
            return FfmpegSource(self, w, h, self.bitrate, self.q,
                                self.scale, self.fps_cap)
        return ScreencapSource(self)

    def _start_source(self):  # call WITHOUT holding self.lock
        self._stop_source()
        self.image = None
        self.seq = 0
        self.first_pub = 0.0
        self.source = self._make_source()
        self.thread = threading.Thread(target=self.source.run, daemon=True)
        self.thread.start()

    def _stop_source(self):  # call WITHOUT holding self.lock
        src = self.source
        if src:
            src.close()
        thr = self.thread
        self.source = None
        self.thread = None
        if thr and thr.is_alive():
            try:
                thr.join(timeout=3)
            except Exception:
                pass

    def switch_serial(self, serial):
        """Switch the active device. Returns False if adb doesn't know it."""
        known = [s for s, _st in list_adb_devices() if s == serial]
        if not known:
            return False
        with self.lock:
            if serial == self.serial:
                return True
            self.serial = serial
            self.inp.close()
            self.inp = AdbInput(serial)
            self.info = {"model": "?", "battery": None, "charging": False,
                         "wake": True, "size": None}
            self._start_info()
        if serial:
            self._start_source()
        else:
            self._stop_source()
        if serial:
            wake_unlock(serial, stay_awake=True)
        return True

    def set_stream_mode(self, want_stream):
        with self.lock:
            self.stream_ok = want_stream and HAS_FFMPEG
        self._start_source()  # swaps kind
        return self.stream_ok

    def is_stream(self):
        return isinstance(self.source, FfmpegSource)

    # -- frame publishing ---------------------------------------------------
    def _publish(self, frame):
        with self.lock:
            if self.image is None:
                self.first_pub = time.time()
            self.image = frame
            self.is_jpeg = isinstance(frame, bytes)
            self.seq += 1

    def latest(self):
        with self.lock:
            return self.image, self.is_jpeg, self.seq

    def dev_size(self):
        """Device pixel size (not the constrained stream size)."""
        sz = self.info.get("size")
        if sz:
            return tuple(sz)
        img, _, _ = self.latest()
        if img is not None and hasattr(img, "size"):
            return img.size
        if self.serial:
            sz = device_size(self.serial)
            if sz:
                self.info["size"] = sz
            return sz or (1920, 1080)
        return (1920, 1080)

    # -- status poller ------------------------------------------------------
    def _start_info(self):
        self._info_stop = True
        if self._info_thread and self._info_thread.is_alive():
            self._info_thread.join(timeout=2)
        self._info_stop = False
        self._info_thread = threading.Thread(target=self._info_loop,
                                             daemon=True)
        self._info_thread.start()

    def _info_loop(self):
        while not self._info_stop:
            serial = self.serial
            if serial:
                try:
                    level, charging, wake = device_status(serial)
                    self.info["battery"] = level
                    self.info["charging"] = charging
                    self.info["wake"] = wake
                except Exception:
                    pass
                if not self.info["model"] or self.info["model"] == "?":
                    self.info["model"] = model_name(serial)
                if not self.info["size"]:
                    self.info["size"] = device_size(serial)
            for _ in range(30):
                if self._info_stop:
                    return
                time.sleep(0.1)

    def close(self):
        self._info_stop = True
        self._watch_stop = True
        self._stop_source()
        self.inp.close()
        for t in (self._info_thread, self._watch_thread):
            if t and t.is_alive():
                try:
                    t.join(timeout=5)
                except Exception:
                    pass

    # -- watchdog: never let a dead/no-op pipeline stall the whole app -----
    def _start_watchdog(self):
        self._watch_stop = False
        self._watch_thread = threading.Thread(target=self._watch_loop,
                                              daemon=True)
        self._watch_thread.start()

    def _watch_loop(self):
        last_restart = 0.0
        while not self._watch_stop:
            time.sleep(2)
            with self.lock:
                src = self.source
                if not isinstance(src, FfmpegSource):
                    continue
                got = self.first_pub
                now = time.time()
            try:
                spawn_age = now - src.spawned_at
            except AttributeError:
                spawn_age = 0
            if got == 0.0:
                # stream never produced a single frame
                if spawn_age > 10:
                    self.set_stream_mode(False)
            elif now - got > 8 and now - last_restart > 10:
                # pipeline stalled (e.g. late screenrecord restart) — respawn
                last_restart = now
                self.restart()
            elif now - got > 20:
                self.set_stream_mode(False)

    def restart(self):
        """Respawn the source pipeline, carrying over any adjusted params.

        Call outside self.lock: the old capture thread is joined here and
        may itself be waiting on the manager lock in _publish."""
        with self.lock:
            src = self.source
            if isinstance(src, FfmpegSource):
                p = src._snapshot()
                self.bitrate = p["bitrate"]
                self.q = p["q"]
                self.scale = p["scale"]
                self.fps_cap = p["fps_cap"]
        self._start_source()


# ---------------------------------------------------------------------------
# renderer — numpy fast path, pure-Python fallback
# ---------------------------------------------------------------------------

class Renderer:
    """Builds terminal row strings from an RGB buffer of (cols, rows*2).

    Output uses run-length colouring (one SGR pair per colour run) which
    keeps escape-code overhead tiny, and each row is a self-contained string
    so the TUI can redraw only the rows that changed.
    """

    __slots__ = ("cols", "rows", "truecolor", "last_run_key", "last_run_len")

    def __init__(self, cols, rows, truecolor):
        self.cols = cols
        self.rows = rows
        self.truecolor = truecolor
        self.last_run_key = None
        self.last_run_len = 0

    # -- row emission -------------------------------------------------------
    def _code(self, key):
        r1, g1, b1, r2, g2, b2 = key
        if self.truecolor:
            return (f"\x1b[38;2;{r1};{g1};{b1}m\x1b[48;2;{r2};{g2};{b2}m")
        return (f"\x1b[38;5;{rgb256(r1,g1,b1)}m\x1b[48;5;{rgb256(r2,g2,b2)}m")

    def _flush_run(self):
        if self.last_run_len:
            self.last_run_key = None
            self.last_run_len = 0

    # numpy path: vectorized pair extraction, unique-key mapping, run breaks
    def frame_numpy(self, img):
        cols, rows = self.cols, self.rows
        arr = np.asarray(img, dtype=np.uint8)          # (rows*2, cols, 3)
        top = arr[0::2].reshape(cols * rows, 3)
        bot = arr[1::2].reshape(cols * rows, 3)
        keys = np.concatenate([top, bot], axis=1)      # (N, 6)
        # run boundaries
        if keys.shape[0] > 1:
            diff = np.any(keys[1:] != keys[:-1], axis=1)
            ends = np.flatnonzero(diff) + 1
        else:
            ends = np.array([], dtype=np.intp)
        if ends.size == 0 or ends[0] != 0:
            starts = np.concatenate([[0], ends])
        else:
            starts = ends.copy()
        run_ends = np.concatenate([starts[1:], [keys.shape[0]]])
        lines = []
        tc = self.truecolor
        for y in range(rows):
            lo, hi = y * cols, (y + 1) * cols
            # select runs that OVERLAP with this row's range [lo, hi)
            sel = (starts < hi) & (run_ends > lo)
            rs = np.maximum(starts[sel], lo) - lo
            re_ = np.minimum(run_ends[sel], hi) - lo
            parts = []
            for s0, e0 in zip(rs, re_):
                k = keys[s0 + lo]
                if tc:
                    r1, g1, b1, r2, g2, b2 = (int(v) for v in k)
                    parts.append(f"\x1b[38;2;{r1};{g1};{b1}m"
                                 f"\x1b[48;2;{r2};{g2};{b2}m")
                else:
                    r1, g1, b1, r2, g2, b2 = (int(v) for v in k)
                    parts.append(f"\x1b[38;5;{rgb256(r1,g1,b1)}m"
                                 f"\x1b[48;5;{rgb256(r2,g2,b2)}m")
                parts.append(BLOCK * (e0 - s0))
            lines.append("".join(parts))
        return lines

    # pure-Python path: per-cell with colour-run caching (works everywhere)
    def frame_py(self, img):
        pix = img.load()
        rows, cols = self.rows, self.cols
        lines = []
        for y in range(rows):
            yy = y << 1
            cells = []
            prev = None
            run = 0
            for x in range(cols):
                r1, g1, b1 = pix[x, yy]
                r2, g2, b2 = pix[x, yy + 1]
                key = (r1, g1, b1, r2, g2, b2)
                if key != prev:
                    if prev is not None:
                        cells.append(self._code(prev))
                    else:
                        cells.append(self._code(key))
                    prev = key
                    run = 1
                else:
                    run += 1
                    cells.append(self._code(key))
                cells.append(BLOCK)
            lines.append("".join(cells))
        return lines

    def frame(self, img):
        if HAVE_NUMPY:
            try:
                return self.frame_numpy(img)
            except Exception:
                pass
        return self.frame_py(img)


# ---------------------------------------------------------------------------
# geometry / mapping (TUI)
# ---------------------------------------------------------------------------

def fit_image(img, cols, rows2, mode):
    """Return (render_img, (out_w, out_h), (offset_x, offset_y)).

    offset = paste offset in canvas coords (contain/fill) or crop offset in
    resized coords (cover); input mapping needs it to stay in sync.
    """
    dw, dh = img.size
    if mode == "fill":
        return img.resize((cols, rows2), Image.LANCZOS), (cols, rows2), (0, 0)
    if mode == "cover":
        scale = max(cols / dw, rows2 / dh)
        ow = max(1, int(dw * scale))
        oh = max(1, int(dh * scale))
        resized = img.resize((ow, oh), Image.LANCZOS)
        left = (ow - cols) // 2
        top = (oh - rows2) // 2
        return resized.crop((left, top, left + cols, top + rows2)), \
            (cols, rows2), (left, top)
    scale = min(cols / dw, rows2 / dh)
    ow = max(1, int(dw * scale))
    oh = max(1, int(dh * scale))
    ox = (cols - ow) // 2
    oy = (rows2 - oh) // 2
    buf = Image.new("RGB", (cols, rows2), (8, 8, 8))
    buf.paste(img.resize((ow, oh), Image.LANCZOS), (ox, oy))
    return buf, (ow, oh), (ox, oy)


def map_to_device(dev_size, mx, my, cols, rows, mode):
    """Convert 1-based canvas char coords to device pixel coords."""
    dw, dh = dev_size
    rows2 = rows * 2
    if mode == "fill":
        ox, oy, ow, oh = 0, 0, cols, rows2
    elif mode == "cover":
        scale = max(cols / dw, rows2 / dh)
        ow, oh = max(1, int(dw * scale)), max(1, int(dh * scale))
        ox, oy = (ow - cols) // 2, (oh - rows2) // 2
    else:
        scale = min(cols / dw, rows2 / dh)
        ow, oh = max(1, int(dw * scale)), max(1, int(dh * scale))
        ox, oy = (cols - ow) // 2, (rows2 - oh) // 2
    if mode == "cover":
        fx = (mx - 1 + ox + 0.5) / ow
        fy = ((my - 1) * 2 + oy + 0.5) / oh
    else:
        fx = (mx - 1 - ox + 0.5) / ow
        fy = ((my - 1) * 2 - oy + 0.5) / oh
    dx = int(round(fx * dw))
    dy = int(round(fy * dh))
    return max(0, min(dw - 1, dx)), max(0, min(dh - 1, dy))


def device_to_term(dev_size, dx, dy, cols, rows, mode):
    """Inverse of map_to_device: device -> 1-based canvas cell."""
    dw, dh = dev_size
    rows2 = rows * 2
    if mode == "fill":
        ox, oy, ow, oh = 0, 0, cols, rows2
    elif mode == "cover":
        scale = max(cols / dw, rows2 / dh)
        ow, oh = max(1, int(dw * scale)), max(1, int(dh * scale))
        ox, oy = (ow - cols) // 2, (oh - rows2) // 2
    else:
        scale = min(cols / dw, rows2 / dh)
        ow, oh = max(1, int(dw * scale)), max(1, int(dh * scale))
        ox, oy = (cols - ow) // 2, (rows2 - oh) // 2
    if mode == "cover":
        mx = int(round(dx / dw * ow - ox + 1 - 0.5))
        my = int(round(dy / dh * oh / 2 - oy / 2 + 1 - 0.5))
    else:
        mx = int(round(dx / dw * ow + ox + 1 - 0.5))
        my = int(round(dy / dh * oh / 2 + oy / 2 + 1 - 0.5))
    return max(1, min(cols, mx)), max(1, min(rows, my))


# ---------------------------------------------------------------------------
# terminal input parsing
# ---------------------------------------------------------------------------

FKEY_CSI = {
    "11~": "F1", "12~": "F2", "13~": "F3", "14~": "F4", "15~": "F5",
    "17~": "F6", "18~": "F7", "19~": "F8", "20~": "F9",
    "21~": "F10", "23~": "F11", "24~": "F12",
}
FKEY_SS3 = {"P": "F1", "Q": "F2", "R": "F3", "S": "F4"}
ARROWS = {"A": "up", "B": "down", "C": "right", "D": "left"}


def parse_input(data):
    """Decode a byte string from the terminal into a list of events."""
    events = []
    n = len(data)
    i = 0
    while i < n:
        c = data[i]
        if c == 0x1b:  # ESC
            if i + 1 < n and data[i + 1] == 0x07:  # Ctrl+Alt+G
                events.append(("grab_toggle",))
                i += 2
                continue
            if i + 1 >= n:
                events.append(("esc",))
                i += 1
            elif data[i + 1] == ord("["):
                j = i + 2
                if j < n and data[j] == ord("<"):  # SGR mouse
                    k = j + 1
                    while k < n and data[k] not in (ord("M"), ord("m")):
                        k += 1
                    if k < n and data[k] in (ord("M"), ord("m")):
                        parts = data[j + 1:k].decode(errors="ignore").split(";")
                        if len(parts) == 3:
                            b, mx, my = (int(p) for p in parts)
                            events.append(("mouse", b, mx, my,
                                           data[k] == ord("m")))
                        i = k + 1
                        continue
                    else:
                        i = k + 1
                        continue
                k = j
                while k < n and 0x30 <= data[k] <= 0x3f:
                    k += 1
                if k < n:
                    seq = data[j:k + 1].decode(errors="ignore")
                    if seq in FKEY_CSI:
                        events.append(("fkey", FKEY_CSI[seq]))
                    elif seq in ARROWS:
                        events.append(("dpad", ARROWS[seq]))
                    i = k + 1
                    continue
                i = k + 1
                continue
            elif data[i + 1] == ord("O"):  # SS3 (F1-F4)
                if i + 2 < n and chr(data[i + 2]) in FKEY_SS3:
                    events.append(("fkey", FKEY_SS3[chr(data[i + 2])]))
                    i += 3
                    continue
                i += 2
                continue
            else:
                events.append(("esc",))
                i += 1
                continue
        elif c in (0x0d, 0x0a):
            events.append(("enter",))
            i += 1
        elif c in (0x7f, 0x08):
            events.append(("backspace",))
            i += 1
        elif c == 0x03:  # Ctrl-C
            events.append(("quit",))
            i += 1
        elif c == 0x04:  # Ctrl-D
            events.append(("quit",))
            i += 1
        elif c == 0x06:  # Ctrl-F
            events.append(("fit",))
            i += 1
        elif c == 0x14:  # Ctrl-T
            events.append(("menu",))
            i += 1
        elif c == 0x13:  # Ctrl-S
            events.append(("stream_toggle",))
            i += 1
        elif c == 0x07:  # Ctrl-G
            events.append(("grab_toggle",))
            i += 1
        elif 32 <= c < 127:
            events.append(("char", chr(c)))
            i += 1
        else:
            i += 1
    return events


# ---------------------------------------------------------------------------
# TUI
# ---------------------------------------------------------------------------

HELP_ROWS = [
    ("Mouse", "click tap · drag touch · wheel scroll"),
    ("Type", "text … Enter sends · Backspace deletes"),
    ("Keys", "F1 menu · F2 home · F3 back · F4 recents"),
    ("Keys", "F5 power · F6 vol+ · F7 vol- · F8 center"),
    ("Keys", "F9-12 dpad · arrows dpad · enter/backspace"),
    ("View", "Ctrl-F fit · Ctrl-S stream ↔ screencap"),
    ("Menu", "Ctrl-T menu overlay · ? help · Esc×2 quit"),
    ("Grab", "Ctrl-Alt-G / F12 pass keys to device (Zellij)"),
]

MENU_ITEMS = []


def build_menu(args, m, chrome_on):
    items = [
        ("fit", "Fit mode", FIT_MODES, args.fit, "%s"),
        ("stream", "Capture", ["stream", "screencap"],
         "stream" if (m.is_stream() if m else args.stream) else "screencap",
         "%s"),
        ("fps", "TUI fps", ["8", "12", "20", "30", "60"], str(args.fps), "%s"),
        ("bitrate", "Bitrate", ["2", "4", "6", "8", "12", "20", "40"],
         str(args.bitrate // 1_000_000), "%s Mb/s"),
        ("q", "JPEG quality", ["1", "2", "3", "4", "5", "6", "8"],
         str(args.jpeg_quality), "%s"),
        ("colors", "Colors", ["auto", "truecolor", "256"],
         args.colors, "%s"),
        ("chrome", "Chrome bars", ["on", "off"],
         "on" if chrome_on else "off", "%s"),
        ("stay", "Stay awake", ["on", "off"],
         "on" if args.stay_awake else "off", "%s"),
        ("wake", "Wake device", None, None, "action"),
        ("help", "Help overlay", None, None, "action"),
        ("quit", "Quit", None, None, "action"),
    ]
    return items


class TUI:
    def __init__(self, args, m):
        self.args = args
        self.m = m
        self.truecolor = args.truecolor
        self.running = True
        self.fit = args.fit
        self.chrome = args.chrome_bars
        self.grab = False
        self.menu_open = False
        self.menu_idx = 0
        self.help_until = 0.0
        self.quit_until = 0.0
        self.text_buf = []
        self.pending_tap = None
        self.pending_until = 0.0
        self.press = None
        self.move = None
        self.dragged = False
        self.last_touch_ts = None
        self.dot = None
        self.dot_until = 0.0
        self.last_seq = -1
        self.prev_rows = []
        self.prev_top = None
        self.prev_bottom = None
        self.spark = deque([0.5] * 12, maxlen=12)
        self.frames = 0
        self.t0 = time.time()
        self.proc_fps = 0.0
        self.proc_ms = 0.0
        self.geom = (0, 0)

    # ------------------------------------------------------------------ bar
    def top_bar(self, cols, rows, dev, rend, ms, fps, cap_mode):
        serial = self.m.serial or "?"
        model = self.m.info.get("model") or serial
        bat = self.m.info.get("battery")
        parts = [
            style(f" {model} ", fg=(0, 0, 0), bg=ACCENT, bold=True),
            style(f" {serial} ", fg=DIM),
            style(f" {dev[0]}×{dev[1]} ", fg=TEXT),
            style(f" {cap_mode} ", fg=ACCENT if cap_mode == "stream" else WARN),
            style(f" {fps:4.1f} fps ", fg=TEXT),
            style(f" {ms:4.1f} ms ", fg=(GOOD if ms < 40 else WARN)),
            style(f" fit {self.fit} ", fg=DIM),
            style(f" {rows}×{cols} ", fg=DIM),
        ]
        if bat is not None:
            batc = GOOD if bat > 20 else DANGER
            parts.append(style(f" bat {bat}% ", fg=batc))
        if self.grab:
            parts.append(style(" GRAB ", fg=(0, 0, 0), bg=ACCENT2, bold=True))
        return fit_line(parts, cols)

    def bottom_bar(self, cols, m):
        spark = "".join(SPARK[min(7, max(0, int(v * 7)))] for v in self.spark)
        right = f" {spark}"
        hints = []
        if time.time() < self.quit_until:
            hints.append(style(" Press Esc again to quit ", fg=(0, 0, 0), bg=DANGER, bold=True))
        elif self.menu_open:
            pass
        elif self.pending_tap and time.time() < self.pending_until:
            x, y = self.pending_tap
            hints.append(style(f" TAP {x},{y} · Enter confirm · arrows nudge ",
                               fg=ACCENT, bg=PANEL_BG))
        elif self.text_buf:
            hints.append(style(" text: " + "".join(self.text_buf) + " ", fg=WARN))
        else:
            hints.append(style(" Click=tap  Drag=touch  Wheel=scroll  "
                               "Type=text  ?=help  ^T=menu  Esc×2=quit ",
                               fg=DIM))
        return fit_line(hints + [right], cols)

    # -------------------------------------------------------------- overlays
    def draw_menu(self, cols, lines, items, idx):
        w = min(44, max(28, cols - 4))
        x = max(1, (cols - w) // 2)
        y0 = max(1, (lines - (len(items) + 4)) // 2)
        box = []
        title = style("┌─ scterm " + VERSION + " ", fg=ACCENT)
        title = fit_line([title, style("─" * (w - vlen(title) - 1) + "┐",
                                       fg=ACCENT)], w)
        box.append((y0, title))
        for i, (_key, label, _opts, val, fmt) in enumerate(items):
            if i == idx:
                left = style(" ▶ " + label, fg=(0, 0, 0), bg=ACCENT,
                             bold=True)
            else:
                left = style("   " + label, fg=TEXT)
            if fmt == "action":
                right = style(" ↵ ", fg=DIM)
            elif _opts:
                right = style(" " + (fmt % val), fg=ACCENT2, bold=True)
            else:
                right = style(" " + (fmt % val), fg=DIM)
            pad = max(1, w - vlen(left) - vlen(right))
            box.append((y0 + i + 1, fit_line([left, " " * pad, right], w)))
        box.append((y0 + len(items) + 1,
                    fit_line([style("└" + "─" * (w - 2) + "┘", fg=ACCENT)], w)))
        return [f"\x1b[{yy};{x}H{s}" for yy, s in box]

    def draw_help(self, cols, lines, m):
        w = min(66, max(40, cols - 4))
        x = max(1, (cols - w) // 2)
        n = len(HELP_ROWS) + 4
        y0 = max(1, (lines - n) // 2)
        out = [f"\x1b[{y0};{x}H" + fit_line(
            [style("┌─ help ─", fg=ACCENT),
             style("─" * (w - vlen("┌─ help ─") - 1) + "┐", fg=ACCENT)], w)]
        for i, (k, v) in enumerate(HELP_ROWS):
            s = style(f"│ {k:<8}", fg=ACCENT, bold=True) + style(v, fg=TEXT)
            s = fit_line([s, style("│", fg=DIM)], w)
            out.append(f"\x1b[{y0 + i + 1};{x}H{s}")
        out.append(f"\x1b[{y0 + n - 1};{x}H" + fit_line(
            [style("└" + "─" * (w - 2) + "┘", fg=ACCENT)], w))
        return out

    # ------------------------------------------------------------------ main
    def handle_input(self, events, cols, rows, m, args):
        for ev in events:
            kind = ev[0]
            if kind == "quit":
                self.running = False
            elif kind == "esc":
                if self.menu_open:
                    self.menu_open = False
                elif time.time() < self.quit_until:
                    self.running = False
                else:
                    self.quit_until = time.time() + 3.0
                    self.last_seq = -1
            elif kind == "menu":
                self.menu_open = not self.menu_open
                self.menu_idx = 0
            elif kind == "fit":
                self.fit = FIT_MODES[(FIT_MODES.index(self.fit) + 1) % 3]
                self.last_seq = -1
                self.prev_rows = []  # force a full redraw in the new mode
                self.prev_top = None
            elif kind == "stream_toggle":
                m.set_stream_mode(not m.is_stream())
                self.last_seq = -1
            elif kind == "grab_toggle":
                self.grab = not self.grab
                self.last_seq = -1
                if os.environ.get("ZELLIJ"):
                    try:
                        subprocess.run(
                            ["zellij", "action", "switch-mode",
                             "locked" if self.grab else "normal"],
                            stdout=subprocess.DEVNULL,
                            stderr=subprocess.DEVNULL, timeout=1)
                    except Exception:
                        pass
            elif self.menu_open and kind in ("dpad", "char", "enter", "backspace"):
                self._menu_nav(kind, ev, m, args)
            elif kind == "char":
                if ev[1] == "?":
                    now = time.time()
                    if now < self.help_until:
                        self.help_until = 0.0
                    else:
                        self.help_until = now + 4.0
                    self.quit_until = 0.0
                else:
                    self.text_buf.append(ev[1])
            elif kind == "enter":
                if self.pending_tap and time.time() < self.pending_until:
                    # finger is already DOWN (live touch): finishing the
                    # gesture at the confirmed spot = a tap there
                    if self.press:
                        touch_up(m.inp, *self.pending_tap)
                        self.press = None
                        self.move = None
                        self.dragged = False
                        self.last_touch_ts = None
                    self.pending_tap = None
                elif self.text_buf:
                    send_text(m.inp, "".join(self.text_buf))
                    self.text_buf.clear()
                else:
                    key(m.inp, KEY["ENTER"])
            elif kind == "backspace":
                if self.pending_tap and time.time() < self.pending_until:
                    self.pending_tap = None
                elif self.text_buf:
                    self.text_buf.pop()
                else:
                    key(m.inp, KEY["DEL"])
            elif kind == "fkey":
                if args.zellij and not self.grab and ev[1] != "F12":
                    continue
                if ev[1] == "F12":
                    self.grab = not self.grab
                    self.last_seq = -1
                    continue
                if self.text_buf:
                    send_text(m.inp, "".join(self.text_buf))
                    self.text_buf.clear()
                codes = {"F1": "MENU", "F2": "HOME", "F3": "BACK",
                         "F4": "APP_SWITCH", "F5": "POWER",
                         "F6": "VOL_UP", "F7": "VOL_DOWN",
                         "F8": "CENTER", "F9": "DPAD_UP",
                         "F10": "DPAD_DOWN", "F11": "DPAD_LEFT"}
                if ev[1] in codes:
                    key(m.inp, KEY[codes[ev[1]]])
            elif kind == "dpad" and not self.menu_open:
                if self.pending_tap and time.time() < self.pending_until:
                    x, y = self.pending_tap
                    s = 12
                    if ev[1] == "up":
                        y -= s
                    elif ev[1] == "down":
                        y += s
                    elif ev[1] == "left":
                        x -= s
                    else:
                        x += s
                    dw, dh = m.dev_size()
                    self.pending_tap = (max(0, min(dw - 1, x)),
                                        max(0, min(dh - 1, y)))
                    self.pending_until = time.time() + 3.0
                    self.last_seq = -1
                else:
                    if self.text_buf:
                        send_text(m.inp, "".join(self.text_buf))
                        self.text_buf.clear()
                    key(m.inp, KEY["DPAD_" + ev[1].upper()])
            elif kind == "mouse":
                self._mouse(ev, cols, rows, m)

    def _menu_nav(self, kind, ev, m, args):
        items = build_menu(args, m, self.chrome)
        n = len(items)
        if kind == "dpad":
            if ev[1] == "up":
                self.menu_idx = (self.menu_idx - 1) % n
            elif ev[1] == "down":
                self.menu_idx = (self.menu_idx + 1) % n
            return
        if kind == "backspace":
            self.menu_open = False
            return
        if kind == "char" and ev[1] in ("q", "Q", "x", "X"):
            self.menu_open = False
            return
        if kind != "enter":
            return
        key_, label, opts, val, fmt = items[self.menu_idx]
        if key_ == "quit":
            self.running = False
        elif key_ == "help":
            self.menu_open = False
            self.help_until = time.time() + 6.0
        elif key_ == "wake":
            wake_unlock(m.serial, stay_awake=args.stay_awake)
        elif key_ == "fit":
            self.fit = FIT_MODES[(FIT_MODES.index(self.fit) + 1) % 3]
            self.last_seq = -1
            self.prev_rows = []  # force a full redraw in the new mode
            self.prev_top = None
        elif key_ == "stream":
            m.set_stream_mode(not m.is_stream())
            self.last_seq = -1
        elif key_ == "fps":
            args.fps = int(opts[(opts.index(str(args.fps)) + 1) % len(opts)])
        elif key_ == "bitrate":
            args.bitrate = int(opts[(opts.index(str(args.bitrate // 1_000_000))
                                     + 1) % len(opts)]) * 1_000_000
            if m.is_stream():
                m.source.set_bitrate(args.bitrate)
        elif key_ == "q":
            args.jpeg_quality = int(opts[(opts.index(str(args.jpeg_quality))
                                          + 1) % len(opts)])
            if m.is_stream():
                m.source.set_quality(args.jpeg_quality)
        elif key_ == "colors":
            args.colors = opts[(opts.index(args.colors) + 1) % len(opts)]
            self.truecolor = (args.colors == "truecolor"
                              or (args.colors == "auto" and supports_truecolor()))
        elif key_ == "chrome":
            self.chrome = not self.chrome
        elif key_ == "stay":
            args.stay_awake = not args.stay_awake
            if m.serial:
                set_stay_awake(m.serial, args.stay_awake)
        self.last_seq = -1

    def _mouse(self, ev, cols, rows, m):
        args = self.args
        if args.zellij and not self.grab:
            return
        if self.menu_open:
            return
        b, mx, my, released = ev[1], ev[2], ev[3], ev[4]
        btn = b & 3
        motion = b & 32
        wheel = b & 64
        if m.serial is None:
            return
        dw, dh = m.dev_size()
        # canvas rows start below chrome bars
        top_rows = 1 if self.chrome else 0
        cy = my - top_rows
        if cy < 1 or cy > rows:
            return
        if wheel:
            if self.text_buf:
                send_text(m.inp, "".join(self.text_buf))
                self.text_buf.clear()
            cx = dw // 2
            if b & 1:
                swipe(m.inp, cx, int(dh * 0.35), cx, int(dh * 0.65), 90)
            else:
                swipe(m.inp, cx, int(dh * 0.65), cx, int(dh * 0.35), 90)
            return
        if btn == 0 and not released:
            if self.text_buf:
                send_text(m.inp, "".join(self.text_buf))
                self.text_buf.clear()
            x, y = map_to_device((dw, dh), mx, cy, cols, rows, self.fit)
            self.pending_tap = (x, y)
            self.pending_until = time.time() + 4.0
            self.press = (mx, cy)
            self.move = (mx, cy)
            self.dragged = False
            self.last_seq = -1
            # finger on the glass: DOWN immediately, so the app reacts live
            touch_down(m.inp, x, y)
            self.last_touch_ts = time.time()
        elif motion and btn == 0 and self.press:
            x, y = map_to_device((dw, dh), mx, cy, cols, rows, self.fit)
            self.pending_tap = (x, y)
            self.pending_until = time.time() + 4.0
            self.move = (mx, cy)
            self.dragged = True
            self.last_seq = -1
            # stream MOVE events at ~35 Hz max — the shell pipe is fast, but
            # terminal mouse floods at up to 125 Hz which apps can't use
            now = time.time()
            if self.last_touch_ts is None or \
                    now - self.last_touch_ts >= 0.028:
                touch_move(m.inp, x, y)
                self.last_touch_ts = now
        elif btn == 0 and released:
            if self.press:
                tx, ty = (self.pending_tap or map_to_device(
                    (dw, dh), self.move[0], self.move[1],
                    cols, rows, self.fit))
                touch_up(m.inp, tx, ty)
                self.pending_tap = None
            self.press = None
            self.move = None
            self.dragged = False
            self.last_touch_ts = None

    # ------------------------------------------------------------------ draw
    def draw(self, rows_strs, cols, lines, m, dev, rend, ms, cap_mode):
        top_rows = 1 if self.chrome else 0
        rows = lines - 1 - top_rows
        out = []
        # rows changed?
        changed = []
        for i, s in enumerate(rows_strs):
            if i >= len(self.prev_rows) or self.prev_rows[i] != s:
                changed.append(i)
        full = (len(self.prev_rows) != len(rows_strs)
                or len(changed) > len(rows_strs) // 2)
        if full:
            # start BELOW the chrome top bar (row 1) so a full redraw can't
            # overwrite it; the top bar is drawn separately afterwards
            out.append(f"\x1b[{1 + top_rows};1H")
            for s in rows_strs:
                out.append(s)
                out.append("\r\n")
        elif changed:
            for i in changed:
                out.append(f"\x1b[{i + 1 + top_rows};1H{s}")

        if self.chrome:
            top = self.top_bar(cols, rows, dev, rend, ms, self.proc_fps,
                               cap_mode)
            if top != self.prev_top:
                out.append(f"\x1b[1;1H{top}")
                self.prev_top = top
        bottom = self.bottom_bar(cols, m)
        if bottom != self.prev_bottom:
            out.append(f"\x1b[{lines};1H{bottom}")
            self.prev_bottom = bottom

        # cursor dot
        if self.dot and time.time() < self.dot_until:
            tx, ty = self.dot
            out.append(f"\x1b[{ty + top_rows};{tx}H"
                       f"\x1b[38;5;45m●\x1b[0m")
        # overlays
        if self.menu_open:
            items = build_menu(self.args, m, self.chrome)
            out += self.draw_menu(cols, lines, items, self.menu_idx)
        elif time.time() < self.help_until:
            out += self.draw_help(cols, lines, m)
        if out:
            out.append("\x1b[H")
            sys.stdout.write("".join(out))
            sys.stdout.flush()
        self.prev_rows = rows_strs

    def decode_image(self, img, cols, rows2):
        """JPEG bytes -> PIL image, draft-downscaled for terminal size."""
        if isinstance(img, bytes):
            im = Image.open(io.BytesIO(img))
            try:
                im.draft("RGB", (cols * 2, rows2 * 2))
            except Exception:
                pass
            return im.convert("RGB")
        return img

    def run_loop(self, m, args):
        interval = 1.0 / max(1, args.fps)
        cols, lines = self.geom
        last_draw = 0.0
        startup = time.time()
        while self.running:
            timeout = 0.005 if m.is_stream() else 0.05
            rlist, _, _ = select.select([sys.stdin], [], [], timeout)
            cols_now, lines_now = get_term()
            if rlist:
                data = os.read(sys.stdin.fileno(), 4096)
                rows_now = max(1, lines_now - 1 - (1 if self.chrome else 0))
                for ev in parse_input(data):
                    self.handle_input([ev], cols_now, rows_now, m, args)
            rows = max(1, lines_now - 1 - (1 if self.chrome else 0))
            if rows < 4 or cols_now < 10:
                time.sleep(0.1)
                self.geom = (cols_now, lines_now)
                continue
            img, is_jpeg, seq = m.latest()
            if img is None:
                # fall back to raw capture if the stream never produced
                if isinstance(m.source, FfmpegSource) and \
                        time.time() - startup > 8 and self.frames == 0:
                    m.set_stream_mode(False)
                    args.stream = False
                time.sleep(0.05)
                continue
            now = time.time()
            if seq != self.last_seq and now - last_draw >= interval:
                last_draw = now
                dev = m.dev_size()
                rows2 = rows * 2
                pil = self.decode_image(img, cols_now, rows2)
                render_img, (ow, oh), (ox, oy) = fit_image(
                    pil, cols_now, rows2, self.fit)
                r = Renderer(cols_now, rows, self.truecolor)
                t0 = time.perf_counter()
                rows_strs = r.frame(render_img)
                ms = (time.perf_counter() - t0) * 1000
                self.last_seq = seq
                self.frames += 1
                fps = self.frames / (now - self.t0)
                self.proc_fps = fps
                self.proc_ms = ms
                self.spark.append(min(1.0, max(0.0, ms / 50.0)))
                cap_mode = "stream" if m.is_stream() else "cap"
                if self.pending_tap and time.time() < self.pending_until:
                    try:
                        tx, ty = device_to_term(dev, self.pending_tap[0],
                                                self.pending_tap[1],
                                                cols_now, rows, self.fit)
                        self.dot = (tx, ty)
                        self.dot_until = self.pending_until
                    except Exception:
                        self.dot = None
                self.draw(rows_strs, cols_now, lines_now, m, dev,
                          (ow, oh), ms, cap_mode)
            else:
                time.sleep(0.002 if m.is_stream() else interval)
        m.close()


def get_term():
    try:
        cols, lines = os.get_terminal_size()
    except OSError:
        cols, lines = 80, 24
    if cols < 4 or lines < 4:
        cols, lines = 80, 24
    return cols, lines


def pick_device_interactive():
    devs = list_adb_devices()
    print("\n=== Select device ===", file=sys.stderr)
    for i, (s, st) in enumerate(devs):
        print(f"  {i+1}) {s} [{st}]", file=sys.stderr)
    print("  s) Scan WiFi/Tailscale for ADB (port 5555)", file=sys.stderr)
    print("  i) Enter IP/hostname (e.g. 192.168.1.42:5555)", file=sys.stderr)
    print("  q) Quit", file=sys.stderr)
    while True:
        try:
            c = input("Choice [1/s/i/q]: ").strip().lower()
        except EOFError:
            return None
        if c == "q":
            return None
        if c == "s":
            print("Scanning...", file=sys.stderr)
            found = scan_wifi_adb()
            if not found:
                print("No WiFi ADB devices found.", file=sys.stderr)
                continue
            for i, (ip, src) in enumerate(found):
                print(f"  {i+1}) {ip} [{src}]", file=sys.stderr)
            try:
                sel = input(f"Connect to [1-{len(found)} or ENTER to rescan]: ").strip()
            except EOFError:
                return None
            if sel.isdigit() and 1 <= int(sel) <= len(found):
                ip = found[int(sel) - 1][0]
                subprocess.run(["adb", "connect", f"{ip}:5555"],
                               stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL, timeout=5)
                time.sleep(0.5)
                return f"{ip}:5555"
        if c == "i":
            try:
                ip = input("IP/hostname [:5555]: ").strip()
            except EOFError:
                return None
            if ip:
                if ":" not in ip:
                    ip += ":5555"
                subprocess.run(["adb", "connect", ip],
                               stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL, timeout=5)
                time.sleep(0.5)
                return ip
        if c.isdigit() and 1 <= int(c) <= len(devs):
            return devs[int(c) - 1][0]
        print("Invalid choice", file=sys.stderr)


# ---------------------------------------------------------------------------
# web front-end
# ---------------------------------------------------------------------------

WEB_HTML = r"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no,viewport-fit=cover">
<title>scterm — device stream</title>
<style>
:root{
  --bg:#0a0e13;--panel:#121822;--panel2:#1a2331;--line:#243142;
  --text:#e6ecf3;--dim:#8b98a9;--accent:#22d3ee;--accent2:#a78bfa;
  --good:#34d399;--warn:#fbbf24;--danger:#f87171;
}
*{box-sizing:border-box;-webkit-tap-highlight-color:transparent}
html,body{margin:0;height:100%;background:
  radial-gradient(1200px 600px at 70% -10%, #16233a 0%, transparent 60%),
  radial-gradient(900px 500px at 10% 110%, #1c1433 0%, transparent 55%), var(--bg);
  color:var(--text);font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;overflow:hidden}
#wrap{position:fixed;top:0;left:0;right:0;bottom:0;display:flex;align-items:center;justify-content:center}
#v{width:100%;height:100%;object-fit:contain;touch-action:none;-webkit-user-select:none;user-select:none;cursor:crosshair}
#v.dim{opacity:.25}
/* ---- top chrome ---- */
#bar{position:fixed;top:0;left:0;right:0;padding:8px 10px 8px 52px;display:flex;gap:6px;flex-wrap:wrap;
  align-items:center;z-index:10;background:rgba(10,14,19,.72);backdrop-filter:blur(12px) saturate(1.4);
  border-bottom:1px solid var(--line);transform:translateY(-104%);transition:transform .25s ease;min-height:48px}
#bar.open{transform:translateY(0)}
#menuBtn{position:fixed;top:8px;left:10px;z-index:13;width:36px;height:36px;border-radius:10px;
  background:linear-gradient(135deg,var(--accent),var(--accent2));color:#04121a;border:none;
  font-size:17px;font-weight:700;display:flex;align-items:center;justify-content:center;
  backdrop-filter:blur(8px);box-shadow:0 2px 12px rgba(34,211,238,.25)}
button.ctl{background:var(--panel);color:var(--text);border:1px solid var(--line);border-radius:9px;
  padding:6px 11px;font-size:13px;cursor:pointer;transition:all .12s}
button.ctl:hover{background:var(--panel2);border-color:var(--accent)}
button.ctl.on{background:linear-gradient(135deg,#0e3b45,#14204d);border-color:var(--accent);color:#c9f6ff}
button.ctl.danger.on{background:linear-gradient(135deg,#4a1220,#3d0f1d);border-color:var(--danger);color:#ffd7dd}
.grp{display:flex;align-items:center;gap:7px;background:var(--panel);border:1px solid var(--line);
  border-radius:9px;padding:4px 8px;font-size:12px;color:var(--dim)}
.grp input[type=range]{width:110px;accent-color:var(--accent);height:3px}
.grp span{min-width:44px;color:var(--text);font-variant-numeric:tabular-nums}
select.ctl{background:var(--panel);color:var(--text);border:1px solid var(--line);border-radius:9px;padding:6px 8px;font-size:13px}
/* ---- HUD ---- */
#hud{position:fixed;bottom:10px;left:10px;display:flex;gap:8px;align-items:center;z-index:11;
  font-size:12px;font-variant-numeric:tabular-nums}
.chip{background:rgba(13,18,26,.82);border:1px solid var(--line);border-radius:999px;padding:5px 11px;
  color:var(--dim);backdrop-filter:blur(8px);display:flex;gap:7px;align-items:center}
.chip b{color:var(--text);font-weight:600}
.chip .dot{width:7px;height:7px;border-radius:50%;background:var(--dim);display:inline-block}
.chip.live .dot{background:var(--good);box-shadow:0 0 8px var(--good)}
/* ---- bottom touch bar ---- */
#touchbar{position:fixed;bottom:0;left:0;right:0;display:none;gap:8px;justify-content:center;
  padding:10px 12px calc(10px + env(safe-area-inset-bottom));z-index:12;
  background:linear-gradient(0deg,rgba(8,11,16,.9),transparent)}
#touchbar input{flex:1;max-width:420px;background:var(--panel);border:1px solid var(--line);
  color:var(--text);border-radius:10px;padding:9px 12px;font-size:15px;outline:none}
#touchbar input:focus{border-color:var(--accent)}
/* ---- modals ---- */
.modal{position:fixed;inset:0;background:rgba(4,7,10,.72);display:none;align-items:center;justify-content:center;z-index:100;backdrop-filter:blur(6px)}
.modal.open{display:flex}
.panel{background:linear-gradient(180deg,var(--panel),#0d1219);border:1px solid var(--line);border-radius:18px;
  padding:20px;min-width:min(420px,92vw);max-height:86vh;overflow:auto;box-shadow:0 24px 60px rgba(0,0,0,.5)}
.panel h3{margin:0 0 14px;font-size:15px;letter-spacing:.4px;display:flex;justify-content:space-between;align-items:center}
.panel h3 .x{cursor:pointer;color:var(--dim);font-size:18px}
.dev{display:flex;justify-content:space-between;align-items:center;gap:10px;padding:10px 12px;margin:6px 0;
  background:var(--panel2);border:1px solid var(--line);border-radius:11px;font-size:13px;cursor:pointer}
.dev:hover{border-color:var(--accent)}
.dev .st{color:var(--good);font-size:11px}
.dev .st.offline{color:var(--dim)}
.row{display:flex;gap:8px;margin-top:12px}
.row input{flex:1;background:var(--panel2);border:1px solid var(--line);color:var(--text);
  border-radius:9px;padding:8px 10px;font-size:13px;outline:none}
.toast{position:fixed;left:50%;bottom:64px;transform:translateX(-50%) translateY(20px);z-index:120;
  background:var(--panel2);border:1px solid var(--accent);color:var(--text);padding:9px 18px;
  border-radius:999px;font-size:13px;opacity:0;transition:all .25s;pointer-events:none;box-shadow:0 8px 30px rgba(0,0,0,.4)}
.toast.show{opacity:1;transform:translateX(-50%) translateY(0)}
#spinner{position:fixed;top:50%;left:50%;width:34px;height:34px;margin:-17px;border-radius:50%;
  border:3px solid var(--line);border-top-color:var(--accent);animation:spin 1s linear infinite;z-index:9}
@keyframes spin{to{transform:rotate(360deg)}}
#noconn{position:fixed;inset:0;display:none;align-items:center;justify-content:center;flex-direction:column;
  gap:10px;z-index:8;color:var(--dim);text-align:center;padding:20px}
#noconn b{color:var(--text);font-size:15px}
</style>
</head>
<body>
<button id=menuBtn aria-label="menu">☰</button>
<div id=bar>
  <button class="ctl" onclick="k(3)">🏠 Home</button>
  <button class="ctl" onclick="k(4)">⬅ Back</button>
  <button class="ctl" onclick="k(187)">🗂 Recents</button>
  <button class="ctl" onclick="k(26)">⏻ Power</button>
  <button class="ctl" onclick="k(24)">🔊+</button>
  <button class="ctl" onclick="k(25)">🔊−</button>
  <button class="ctl" onclick="rot()">↻ Rotate</button>
  <button class="ctl" id=kbBtn onclick="toggleKB()" title="on-screen keyboard">⌨</button>
  <button class="ctl" id=grabBtn onclick="toggleGrab()">🎯 Capture keys</button>
  <button class="ctl" id=abtn onclick="toggleAudio()" style="display:none">🔈 Audio off</button>
  <button class="ctl" onclick="openPicker()">📱 Devices</button>
  <button class="ctl" onclick="toggleFS()">⛶ Fullscreen</button>
</div>
<div class="grp" style="position:fixed;top:60px;right:10px;z-index:12;flex-direction:column;display:none" id=settings>
  <div class="grp">FPS <input id=fr type=range min=5 max=60 value=30 step=1><span id=fv>30</span></div>
  <div class="grp">Bitrate <input id=br type=range min=1 max=80 value=8 step=1><span id=bv>8</span> Mb/s</div>
  <div class="grp">Quality <input id=qr type=range min=1 max=10 value=4 step=1><span id=qv>4</span></div>
  <div class="grp">Scale <input id=sr type=range min=25 max=100 value=100 step=5><span id=sv>100</span>%</div>
</div>
<div id=wrap><img id=v draggable=false alt="device stream"></div>
<div id=spinner style="display:none"></div>
<div id=noconn><b>Stream not started</b><span>choose a device</span><button class="ctl" onclick="openPicker()">Pick device</button></div>
<div id=hud>
  <span class="chip" id=liveC><span class=dot></span><span id=liveT>connecting</span></span>
  <span class="chip">⚡ <b id=fps>--</b> fps</span>
  <span class="chip">🖥 <b id=res>--×--</b></span>
  <span class="chip">🔋 <b id=bat>--</b></span>
</div>
<div id=touchbar>
  <input id=textin placeholder="Type… (Enter sends to device)" autocomplete=off>
  <button class="ctl" onclick="k(4)">Back</button>
  <button class="ctl" onclick="k(3)">Home</button>
  <button class="ctl" onclick="k(187)">Recents</button>
</div>
<div class="modal" id=picker><div class="panel">
  <h3>Select device <span class=x onclick="closePicker()">✕</span></h3>
  <div id=plist></div>
  <div class="row"><input id=pip placeholder="IP or host:5555"><button class="ctl" onclick="doConnect()">Connect</button></div>
  <div class="row"><button class="ctl" onclick="doScan()">Scan WiFi/Tailscale</button>
  <button class="ctl" onclick="refreshDevices()">Refresh USB</button></div>
  <div style="margin-top:14px;font-size:11px;color:var(--dim)">USB · WiFi ADB · Tailscale — API: /api/status, /input/*, /settings/*</div>
</div></div>
<div class="toast" id=toast></div>
<script>
const img=document.getElementById('v');
const $=id=>document.getElementById(id);
let grab=false,kb=false,targetFps=30,audioOn=false,audioEl=null,lastBattery=null;
let st=null; // /api/status cache
function toast(t){const e=$('toast');e.textContent=t;e.classList.add('show');
  clearTimeout(toast._t);toast._t=setTimeout(()=>e.classList.remove('show'),1600);}
function api(p,body){return fetch(p,{method:body?'POST':'GET',
  headers:body?{'Content-Type':'application/json'}:undefined,body:body?JSON.stringify(body):undefined});}
function k(code){api('/input/key?code='+code);}
/* ---- controls ---- */
function toggleFS(){if(!document.fullscreenElement)document.documentElement.requestFullscreen?.().catch(()=>{});else document.exitFullscreen();}
function rot(){api('/settings/rotate',{deg:((st&&st.rot||0)+90)%360}).then(()=>{toast('Rotated');pollStatus();});}
function toggleGrab(){grab=!grab;const b=$('grabBtn');b.textContent=grab?'🎯 Keys: on':'🎯 Capture keys';
  b.classList.toggle('on',grab);if(grab)img.requestPointerLock?.();}
function toggleKB(){kb=!kb;const b=$('kbBtn');b.classList.toggle('on',kb);
  $('touchbar').style.display=kb?'flex':'none';if(kb)$('textin').focus();}
function toggleFSbtn(){}
async function toggleAudio(){
  if(!st||!st.audio_avail){toast('scrcpy not found on server');return;}
  audioOn=!audioOn;const b=$('abtn');
  b.textContent=audioOn?'🔊 Audio on':'🔈 Audio off';
  b.classList.toggle('on',audioOn);
  await api('/input/audio',{enabled:audioOn});
  if(audioOn){
    if(!audioEl){audioEl=document.createElement('audio');audioEl.autoplay=true;audioEl.src='/audio.opus';document.body.appendChild(audioEl);}
    else audioEl.src='/audio.opus';
  } else if(audioEl){audioEl.pause();audioEl.removeAttribute('src');}
}
$('fr').addEventListener('change',e=>{targetFps=+e.target.value;api('/settings/fps',{fps:targetFps});});
$('br').addEventListener('change',e=>api('/settings/bitrate',{bitrate:+e.target.value*1e6}));
$('qr').addEventListener('change',e=>api('/settings/quality',{q:+e.target.value}));
$('sr').addEventListener('change',e=>api('/settings/scale',{scale:+e.target.value}));
for(const[inp,out]of[['fr','fv'],['br','bv'],['qr','qv'],['sr','sv']])
  $(inp).addEventListener('input',e=>$(out).textContent=e.target.value+(inp==='br'?'':''));
/* ---- device picker ---- */
function closePicker(){$('picker').classList.remove('open');}
function openPicker(){$('picker').classList.add('open');refreshDevices();}
function refreshDevices(){fetch('/api/devices').then(r=>r.json()).then(j=>{
  const d=$('plist');d.innerHTML=j.map(x=>`<div class="dev" onclick="pick('${x[0].replace(/'/g,"\\'")}')">
    <span>${x[0]}</span><span class="st ${x[1]==='device'?'':'offline'}">${x[1]}</span></div>`).join('')||'<div style="color:var(--dim)">no devices</div>';});}
async function doScan(){const b=$('plist');b.innerHTML='<div>scanning…</div>';
  const j=await(await api('/api/scan')).json();
  b.innerHTML=j.map(x=>`<div class="dev" onclick="pick('${x[0].replace(/'/g,"\\'")}')">
    <span>${x[0]}</span><span class="st">${x[1]}</span></div>`).join('')||'<div style="color:var(--dim)">nothing found</div>';}
async function doConnect(){const ip=$('pip').value.trim();if(!ip)return;
  await api('/api/connect',{serial:ip});$('pip').value='';toast('connecting '+ip);pollStatus();}
async function pick(s){await api('/api/connect',{serial:s});closePicker();toast('switching to '+s);pollStatus();}
/* ---- status ---- */
async function pollStatus(){
  try{const j=await(await fetch('/api/status')).json();st=j;
    $('res').textContent=(j.size&&j.size[0]+'×'+j.size[1])||'--×--';
    $('bat').textContent=j.battery!=null?j.battery+'%':'-';
    $('abtn').style.display=j.audio_avail?'':'none';
    if(j.serial){$('noconn').style.display='none';}
    else{$('noconn').style.display='flex';$('liveC').classList.remove('live');}
  }catch(e){}
}
setInterval(pollStatus,3000);pollStatus();
/* ---- pointer: tap / drag ---- */
function pos(e){
  const r=img.getBoundingClientRect();
  const t=e.touches?e.touches[0]:e;
  const iw=img.naturalWidth||1920,ih=img.naturalHeight||1080;
  const scale=Math.min(r.width/iw,r.height/ih);
  const cw=iw*scale,ch=ih*scale;
  const ox=r.left+(r.width-cw)/2,oy=r.top+(r.height-ch)/2;
  return[Math.max(0,Math.min(1,(t.clientX-ox)/cw)),Math.max(0,Math.min(1,(t.clientY-oy)/ch))];
}
let a=null,cur=null,dot=null;
function showCur(x,y){
  if(!dot){dot=document.createElement('div');dot.style.cssText='position:fixed;width:18px;height:18px;border:2px solid '+
    'var(--accent);border-radius:50%;background:rgba(34,211,238,.18);transform:translate(-50%,-50%);'+
    'pointer-events:none;z-index:20;box-shadow:0 0 10px rgba(34,211,238,.5)';document.body.appendChild(dot);}
  const r=img.getBoundingClientRect(),iw=img.naturalWidth||1920,ih=img.naturalHeight||1080;
  const scale=Math.min(r.width/iw,r.height/ih),cw=iw*scale,ch=ih*scale;
  const ox=r.left+(r.width-cw)/2,oy=r.top+(r.height-ch)/2;
  dot.style.left=(ox+x*cw)+'px';dot.style.top=(oy+y*ch)+'px';dot.style.display='block';
}
function hideCur(){if(dot)dot.style.display='none';}
img.addEventListener('pointerdown',e=>{const p=pos(e);a=p;cur=p;showCur(p[0],p[1]);e.preventDefault();});
img.addEventListener('pointermove',e=>{if(!cur)return;const p=pos(e);cur=p;showCur(p[0],p[1]);e.preventDefault();});
img.addEventListener('pointerup',e=>{
  if(!cur||!a)return;
  const p=pos(e),dx=Math.abs(p[0]-a[0]),dy=Math.abs(p[1]-a[1]);
  if(Math.hypot(dx,dy)<0.01)api('/input/tap',{x:p[0],y:p[1]});
  else api('/input/swipe',{x1:a[0],y1:a[1],x2:p[0],y2:p[1]});
  cur=null;a=null;setTimeout(hideCur,350);e.preventDefault();});
img.addEventListener('pointercancel',()=>{cur=null;a=null;hideCur();});
/* ---- keyboard capture ---- */
const fmap={F1:131,F2:3,F3:4,F4:187,F5:26,F6:24,F7:25,F8:23,F9:19,F10:20,F11:21,F12:22};
function isGrabHotkey(e){return e.ctrlKey&&e.altKey&&e.code==='KeyG';}
document.addEventListener('keydown',e=>{
  if(isGrabHotkey(e)){e.preventDefault();toggleGrab();return;}
  if(!grab)return;
  if(e.key.startsWith('F')||e.ctrlKey){
    e.preventDefault();
    if(fmap[e.key])k(fmap[e.key]);
    else if(e.ctrlKey&&e.key.length===1)api('/input/key',{code:e.key.toUpperCase().charCodeAt(0)});
  }
  if(e.key==='Escape'&&grab){e.preventDefault();toggleGrab();}
});
document.addEventListener('pointerlockchange',()=>{if(!document.pointerLockElement)grab=false;});
$('textin').addEventListener('keydown',e=>{
  if(e.key==='Enter'&&e.target.value){api('/input/text',{text:e.target.value});e.target.value='';}
});
/* ---- stream ---- */
img.src='/stream.mjpg';
let last=performance.now(),n=0,fps=0;
img.addEventListener('load',()=>{n++;$('liveC').classList.add('live');$('liveT').textContent='live';});
setInterval(()=>{const now=performance.now();fps=Math.round(n*1000/(now-last));
  $('fps').textContent=fps;n=0;last=now;},1000);
/* show settings bar when top bar open on coarse pointers */
const isCoarse=matchMedia('(pointer:coarse)').matches;
if(isCoarse)$('settings').style.display='flex';
document.getElementById('menuBtn').addEventListener('click',()=>document.getElementById('bar').classList.toggle('open'));
</script>
</body>
</html>
"""


class WebApp:
    """Embedded HTTP server: one MJPEG stream + JSON control API.

    Deliberately dependency-free (stdlib only) and REST-shaped so the API
    can be re-targeted by a native app later.
    """

    def __init__(self, m, port, bind, fps_cap, jpeg_quality):
        import http.server
        import socketserver
        self.m = m
        self.port = port
        self.bind = bind
        self.fps_cap = fps_cap
        self.jpeg_quality = jpeg_quality
        self.audio_lock = threading.Lock()
        self.audio_proc = None
        self.rot = 0
        self.html = WEB_HTML.encode("utf-8")
        self.httpd = None
        self.server = socketserver.ThreadingTCPServer
        self._httpd_cls = self.server

    # -- small helpers used by both handlers -------------------------------
    def _send_json(self, h, obj, code=200):
        data = json.dumps(obj).encode()
        h.send_response(code)
        h.send_header("Content-Type", "application/json")
        h.send_header("Content-Length", str(len(data)))
        h.end_headers()
        h.wfile.write(data)

    def _dev_size(self):
        img, is_jpeg, _ = self.m.latest()
        if img is not None and hasattr(img, "size"):
            return img.size
        if self.m.info.get("size"):
            return tuple(self.m.info["size"])
        s = self.m.serial
        return device_size(s) if s else (1920, 1080)

    def _status(self):
        img, is_jpeg, seq = self.m.latest()
        src = self.m.source
        return {
            "version": VERSION,
            "serial": self.m.serial,
            "model": self.m.info.get("model"),
            "size": self.m.info.get("size"),
            "battery": self.m.info.get("battery"),
            "charging": self.m.info.get("charging"),
            "wake": self.m.info.get("wake"),
            "source": (src.kind if src else "none"),
            "ffmpeg": HAS_FFMPEG,
            "audio_avail": HAS_SCRCPY,
            "audio_on": bool(self.audio_proc and
                            self.audio_proc.poll() is None),
            "bitrate": (src._snapshot().get("bitrate")
                        if self.m.is_stream() else None),
            "q": (src._snapshot().get("q") if self.m.is_stream() else None),
            "scale": (src._snapshot().get("scale")
                      if self.m.is_stream() else None),
            "web_fps": self.fps_cap,
            "rot": self.rot,
            "devices": list_adb_devices(),
        }

    # -- audio --------------------------------------------------------------
    def _audio_start(self):
        if not HAS_SCRCPY or not self.m.serial:
            return False
        with self.audio_lock:
            self._audio_stop_locked()
            try:
                cmd = ["scrcpy", "-s", self.m.serial, "--no-video",
                       "--no-playback", "--audio-codec=opus",
                       "--audio-bit-rate=128000", "--record-format=opus",
                       "--record", "-"]
                self.audio_proc = subprocess.Popen(
                    cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
                return True
            except Exception:
                self.audio_proc = None
                return False

    def _audio_stop_locked(self):
        p = self.audio_proc
        self.audio_proc = None
        if p:
            try:
                p.terminate()
                p.wait(timeout=2)
            except Exception:
                try:
                    p.kill()
                except Exception:
                    pass

    def _audio_stop(self):
        with self.audio_lock:
            self._audio_stop_locked()

    # -- handler ------------------------------------------------------------
    def make_handler(self):
        import http.server

        app = self

        class Handler(http.server.BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            # ------------------------------------------------------- helpers
            def _body_json(self):
                length = int(self.headers.get("Content-Length", 0) or 0)
                body = self.rfile.read(length) if length else b""
                try:
                    return json.loads(body) if body else {}
                except Exception:
                    return {}

            def _send(self, data, ctype, code=200, extra=None):
                self.send_response(code)
                self.send_header("Content-Type", ctype)
                self.send_header("Content-Length", str(len(data)))
                self.send_header("Cache-Control", "no-store")
                for k, v in (extra or {}).items():
                    self.send_header(k, v)
                self.end_headers()
                try:
                    self.wfile.write(data)
                except Exception:
                    pass

            # ---------------------------------------------------------- GET
            def do_GET(self):
                path = urllib.parse.urlparse(self.path).path
                if path in ("/", "/index.html"):
                    self._send(app.html, "text/html; charset=utf-8",
                               extra={"X-Frame-Options": "DENY"})
                elif path == "/favicon.ico":
                    self.send_response(204)
                    self.end_headers()
                elif path == "/stream.mjpg":
                    self._stream_mjpg()
                elif path == "/audio.opus":
                    self._stream_audio()
                elif path == "/api/status":
                    app._send_json(self, app._status())
                elif path == "/api/devices":
                    app._send_json(self, list_adb_devices())
                else:
                    self.send_error(404)

            def _stream_mjpg(self):
                self.send_response(200)
                self.send_header("Content-Type",
                                 "multipart/x-mixed-replace; boundary=frame")
                self.send_header("Cache-Control", "no-cache, no-store")
                self.send_header("Connection", "keep-alive")
                self.end_headers()
                last_sent = 0.0
                last_seq = -1
                pace = 1.0 / max(1, app.fps_cap)
                try:
                    while True:
                        img, is_jpeg, seq = app.m.latest()
                        if img is None:
                            time.sleep(0.25)
                            continue
                        now = time.time()
                        if seq == last_seq:
                            time.sleep(0.002)
                            continue
                        if now - last_sent < pace:
                            time.sleep(0.002)
                            continue
                        last_seq = seq
                        last_sent = now
                        if is_jpeg:
                            data = img
                        else:  # raw capture fallback: encode here
                            buf = io.BytesIO()
                            im = img
                            if max(im.size) > 1280:
                                im = im.copy()
                                im.thumbnail((1280, 1280), Image.BILINEAR)
                            im.save(buf, format="JPEG",
                                    quality=app.jpeg_quality)
                            data = buf.getvalue()
                        self.wfile.write(
                            b"--frame\r\nContent-Type: image/jpeg\r\n"
                            b"Content-Length: " + str(len(data)).encode() +
                            b"\r\n\r\n" + data + b"\r\n")
                except (BrokenPipeError, ConnectionResetError, OSError):
                    pass

            def _stream_audio(self):
                if not app.audio_proc or app.audio_proc.poll() is not None:
                    if not app._audio_start():
                        self.send_error(404)
                        return
                self.send_response(200)
                self.send_header("Content-Type", "audio/ogg")
                self.send_header("Cache-Control", "no-cache")
                self.end_headers()
                try:
                    while (app.audio_proc and
                           app.audio_proc.poll() is None):
                        chunk = app.audio_proc.stdout.read(4096)
                        if not chunk:
                            break
                        self.wfile.write(chunk)
                except (BrokenPipeError, ConnectionResetError, OSError):
                    pass

            # --------------------------------------------------------- POST
            def do_POST(self):
                parsed = urllib.parse.urlparse(self.path)
                path = parsed.path
                qs = urllib.parse.parse_qs(parsed.query)
                j = self._body_json()
                m = app.m
                if path == "/input/key":
                    code = (qs.get("code", [""])[0]
                            or str(j.get("code", "")))
                    if code:
                        key(m.inp, int(code))
                elif path == "/input/tap":
                    dw, dh = app._dev_size()
                    x = float(j.get("x", 0.5))
                    y = float(j.get("y", 0.5))
                    tap(m.inp, x * dw, y * dh)
                elif path == "/input/swipe":
                    dw, dh = app._dev_size()
                    swipe(m.inp, float(j.get("x1", 0)) * dw,
                          float(j.get("y1", 0)) * dh,
                          float(j.get("x2", 0)) * dw,
                          float(j.get("y2", 0)) * dh, 120)
                elif path == "/input/text":
                    text = str(j.get("text", ""))
                    send_text(m.inp, text)
                elif path == "/input/audio":
                    on = bool(j.get("enabled", False))
                    if on:
                        if not app._audio_start():
                            app._send_json(self, {"ok": False,
                                                  "err": "no scrcpy"})
                            return
                    else:
                        app._audio_stop()
                elif path == "/settings/bitrate":
                    v = j.get("bitrate", 0)
                    if v and m.is_stream():
                        m.source.set_bitrate(v)
                        m.restart()
                elif path == "/settings/fps":
                    v = int(j.get("fps", 0))
                    if v:
                        app.fps_cap = max(5, min(60, v))
                        if m.is_stream():
                            m.source.set_fps_cap(0)  # pacing is client-side
                elif path == "/settings/quality":
                    v = j.get("q", 0)
                    if v and m.is_stream():
                        m.source.set_quality(v)
                        m.restart()
                elif path == "/settings/scale":
                    v = j.get("scale", 0)
                    if v and m.is_stream():
                        m.source.set_scale(v)
                        m.restart()
                elif path == "/settings/rotate":
                    deg = int(j.get("deg", 90))
                    app.rot = deg % 360
                    if m.serial:
                        set_rotation(m.serial, app.rot)
                elif path == "/settings/wake":
                    if m.serial:
                        wake_unlock(m.serial, stay_awake=True)
                elif path == "/settings/stayawake":
                    on = bool(j.get("on", True))
                    if m.serial:
                        set_stay_awake(m.serial, on)
                elif path == "/api/connect":
                    ser = str(j.get("serial", "")).strip()
                    if ser:
                        if ":" in ser or "." in ser:
                            subprocess.run(
                                ["adb", "connect", ser],
                                stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, timeout=5)
                            time.sleep(0.5)
                        if not m.switch_serial(ser):
                            app._send_json(self, {"ok": False,
                                                  "err": "unknown serial"})
                            return
                elif path == "/api/scan":
                    app._send_json(self, scan_wifi_adb())
                    return
                app._send_json(self, {"ok": True})

            def log_message(self, *a):
                pass

        return Handler

    # -- server lifecycle ---------------------------------------------------
    def start(self):
        import socketserver
        cls = type("ScrcpyTCPServer",
                   (socketserver.ThreadingTCPServer,),
                   {"allow_reuse_address": True, "daemon_threads": True})
        try:
            self.httpd = cls((self.bind, self.port), self.make_handler())
        except OSError as e:
            print(f"[web] cannot bind {self.bind}:{self.port}: {e}",
                  file=sys.stderr)
            raise SystemExit(2)
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def close(self):
        self._audio_stop()
        if self.httpd:
            try:
                self.httpd.shutdown()
                self.httpd.server_close()
            except Exception:
                pass


# ---------------------------------------------------------------------------
# launch
# ---------------------------------------------------------------------------

def run_tui(args, m):
    truecolor = (args.colors == "truecolor"
                 or (args.colors == "auto" and supports_truecolor()))
    args.truecolor = truecolor

    # wake + unlock + stay awake
    if not args.no_wake:
        wake_unlock(m.serial, stay_awake=args.stay_awake)

    fd = sys.stdin.fileno()
    old = None
    try:
        old = termios.tcgetattr(fd)
        tty.setraw(fd)
        has_tty = True
    except Exception:
        has_tty = False

    _cleaned = False

    def cleanup():
        nonlocal _cleaned
        if _cleaned:
            return
        _cleaned = True
        if has_tty and old is not None:
            try:
                termios.tcsetattr(fd, termios.TCSADRAIN, old)
            except Exception:
                pass
        try:
            sys.stdout.write("\x1b[?25h\x1b[?1000l\x1b[?1002l\x1b[?1006l")
            if not args.zellij:
                sys.stdout.write("\x1b[?1049l")
            sys.stdout.write("\x1b[?7h\x1b[0m\n")
            sys.stdout.flush()
        except Exception:
            pass
        m.close()
        if os.environ.get("ZELLIJ"):
            try:
                subprocess.run(["zellij", "action", "switch-mode", "normal"],
                               stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL, timeout=1)
            except Exception:
                pass

    atexit.register(cleanup)
    for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        try:
            signal.signal(sig, lambda s, f: (cleanup(), os._exit(0)))
        except Exception:
            pass

    if args.zellij:
        sys.stdout.write("\x1b[2J\x1b[H\x1b[?1000h\x1b[?1002h\x1b[?1006h"
                         "\x1b[?7l")
    else:
        sys.stdout.write("\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1002h"
                         "\x1b[?1006h\x1b[?7l\x1b[2J")
    sys.stdout.flush()

    ui = TUI(args, m)
    ui.geom = get_term()
    try:
        ui.run_loop(m, args)
    finally:
        cleanup()
        os._exit(0)


def main():
    ap = argparse.ArgumentParser(
        description="Control an Android device: terminal TUI and/or web "
                    "viewer, sharing one fast capture pipeline.")
    ap.add_argument("--tui", action="store_true",
                    help="run the terminal UI (default when stdin is a TTY)")
    ap.add_argument("--web", nargs="?", const=8000, type=int, metavar="PORT",
                    help="run the web viewer only (headless server; "
                         "default port 8000)")
    ap.add_argument("--both", nargs="?", const=8000, type=int, metavar="PORT",
                    help="run terminal UI AND web viewer (default port 8000)")
    ap.add_argument("--bind", default="0.0.0.0",
                    help="web bind address (default 0.0.0.0)")
    ap.add_argument("-s", "--serial", default=None,
                    help="device serial (default: first adb device)")
    ap.add_argument("--pick", action="store_true",
                    help="show device picker (USB + WiFi scan + IP)")
    ap.add_argument("--fps", type=int, default=12,
                    help="TUI target refresh rate (default 12)")
    ap.add_argument("--web-fps", type=int, default=30,
                    help="web stream frame cap (default 30)")
    ap.add_argument("--fit", choices=FIT_MODES, default="contain",
                    help="how to fit the screen in the TUI (default contain)")
    ap.add_argument("--colors", choices=["auto", "truecolor", "256"],
                    default="auto",
                    help="TUI colour mode (default auto-detect)")
    ap.add_argument("--stream", dest="stream", action="store_true",
                    default=None, help="use H264 streaming (default when "
                                       "ffmpeg is installed)")
    ap.add_argument("--no-stream", dest="stream", action="store_false",
                    help="force raw screencap capture (no ffmpeg needed)")
    ap.add_argument("--max-size", default="1280x1280", metavar="WxH",
                    help="max stream resolution, aspect preserved "
                         "(default 1280x1280)")
    ap.add_argument("--bitrate", type=int, default=6_000_000, metavar="N",
                    help="screenrecord bit rate in bps (default 6 Mbps)")
    ap.add_argument("--jpeg-quality", type=int, default=4, metavar="Q",
                    help="MJPEG quality 1-10 for the web stream (default 4)")
    ap.add_argument("--web-scale", type=int, default=100, metavar="PCT",
                    help="stream downscale %% for the web (default 100)")
    ap.add_argument("--zellij", action="store_true",
                    help="Zellij/phone-friendly: no alt-screen, no mouse")
    ap.add_argument("--chrome-bars", action="store_true", default=None,
                    help="show the TUI status bars (default on in TTY)")
    ap.add_argument("--no-wake", action="store_true",
                    help="don't wake/unlock the device on launch")
    ap.add_argument("--no-stay-awake", dest="stay_awake",
                    action="store_false",
                    help="don't keep the device screen on while running")
    ap.set_defaults(stay_awake=True)
    ap.add_argument("--debug", action="store_true",
                    help="write diagnostics to /tmp/scterm_debug.log")
    ap.add_argument("--version", action="version", version=f"scterm {VERSION}")
    args = ap.parse_args()

    # auto-detect Zellij — skip alt-screen, let Zellij handle mouse routing
    if not args.zellij and os.environ.get("ZELLIJ"):
        args.zellij = True

    debug_f = None
    if args.debug:
        try:
            debug_f = open("/tmp/scterm_debug.log", "w", buffering=1)
        except OSError:
            pass

    def dbg(*a):
        if debug_f:
            debug_f.write("[%s] %s\n" % (time.strftime("%H:%M:%S"),
                                         " ".join(map(str, a))))

    # ------------------------------------------------------------- modes
    want_web = args.web is not None or args.both is not None
    want_tui = args.tui or args.both is not None
    web_port = args.both if args.both is not None else args.web
    if not want_web and not want_tui:
        # interactive default: TUI if we have a TTY
        try:
            sys.stdin.fileno()
            sys.stdout.isatty()
            want_tui = True
        except Exception:
            want_web = web_port = 8000
    if not want_web and web_port is None and args.web is not None:
        web_port = args.web

    dbg("modes", "tui" if want_tui else "-", "web" if want_web else "-",
        "port", web_port)

    # ------------------------------------------------------------- serial
    serial = args.serial
    if want_tui:
        if args.pick:
            serial = pick_device_interactive()
            if not serial:
                print("No device selected.", file=sys.stderr)
                sys.exit(1)
        elif not serial:
            serial = detect_serial()
            if not serial:
                print("No adb device found. Connect + authorize a device.",
                      file=sys.stderr)
                sys.exit(1)
    elif not serial:
        # web-only: prefer autodetect, else wait for the web picker
        serial = detect_serial()
        if not serial:
            print("[web] no device yet — pick one in the web UI",
                  file=sys.stderr)

    # ------------------------------------------------------------- capture
    stream_ok = HAS_FFMPEG if args.stream is None else (args.stream and HAS_FFMPEG)
    try:
        mw, mh = (int(x) for x in
                  re.split(r"[xX]", args.max_size.strip(), maxsplit=1))
    except Exception:
        mw, mh = 1280, 1280
    m = CaptureManager(serial, mw, mh, args.bitrate, args.jpeg_quality,
                       args.web_scale, 0, stream_ok)
    dbg("capture", "ffmpeg", HAS_FFMPEG, "stream_ok", stream_ok,
        "serial", serial)

    # ------------------------------------------------------------- web
    if want_web:
        web = WebApp(m, web_port or 8000, args.bind, args.web_fps,
                     args.jpeg_quality)
        web.start()
        try:
            tip = subprocess.run(["tailscale", "ip", "-4"],
                                 capture_output=True, text=True,
                                 timeout=2).stdout.strip().splitlines()
            tip = tip[0] if tip else None
        except Exception:
            tip = None
        where = f"http://{tip}:{web_port}" if tip else f"http://{args.bind}:{web_port}"
        print(f"[web] viewer on {where}", file=sys.stderr)

    # ------------------------------------------------------------- tui
    if want_tui:
        try:
            is_tty = sys.stdin.isatty() and sys.stdout.isatty()
        except Exception:
            is_tty = False
        if not is_tty:
            # e.g. `--both` under systemd — fall back to web-only silently
            print("[tui] no TTY — web only", file=sys.stderr)
        else:
            if args.chrome_bars is None:
                args.chrome_bars = True
            try:
                run_tui(args, m)
            except Exception:
                import traceback
                if args.debug:
                    try:
                        with open("/tmp/scterm_error.log", "w") as f:
                            f.write(traceback.format_exc())
                    except OSError:
                        pass
                raise
            finally:
                if want_web:
                    web.close()
            return
    else:
        # headless web: park until killed
        try:
            while True:
                time.sleep(1)
        except KeyboardInterrupt:
            pass
        finally:
            if want_web:
                web.close()
            m.close()
            os._exit(0)


if __name__ == "__main__":
    main()