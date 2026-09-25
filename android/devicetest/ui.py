#!/usr/bin/env python3
"""Minimal uiautomator driver for scterm device tests.

Reads the current screen with `uiautomator dump` and taps elements by text,
so the on-device steps of docs/validation/android-device-tests.md can be
repeated. It only ever runs `adb shell uiautomator dump`, `adb exec-out cat`
of that dump, and `adb shell input tap X Y` on the selected device.

    SERIAL=<adb serial> android/devicetest/ui.py texts
    SERIAL=<adb serial> android/devicetest/ui.py find  REGEX
    SERIAL=<adb serial> android/devicetest/ui.py tap   REGEX [INDEX]
    SERIAL=<adb serial> android/devicetest/ui.py wait  REGEX [SECONDS]
"""
import os
import re
import subprocess
import sys
import time
import xml.etree.ElementTree as ET

ADB = os.environ.get('ADB', os.path.expanduser('~/Library/Android/sdk/platform-tools/adb'))
SERIAL = os.environ.get('SERIAL', '')
DUMP = '/sdcard/scterm-ui.xml'


def adb(*args, timeout=30):
    serial = ['-s', SERIAL] if SERIAL else []
    return subprocess.run([ADB, *serial, *args], capture_output=True, text=True, timeout=timeout)


def dump(retries=3):
    for _ in range(retries):
        adb('shell', 'uiautomator', 'dump', DUMP)
        out = adb('exec-out', 'cat', DUMP).stdout
        if out.strip().startswith('<?xml'):
            return ET.fromstring(out)
        time.sleep(0.5)
    sys.exit('uiautomator dump failed')


def center(bounds):
    x1, y1, x2, y2 = map(int, re.match(r'\[(\d+),(\d+)\]\[(\d+),(\d+)\]', bounds).groups())
    return (x1 + x2) // 2, (y1 + y2) // 2


def matches(node, pattern):
    return pattern.search(node.get('text') or '') or pattern.search(node.get('content-desc') or '')


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else ''
    if cmd == 'texts':
        for n in dump().iter('node'):
            text, desc = n.get('text'), n.get('content-desc')
            if text or desc:
                flags = ' '.join(k for k in ('clickable', 'checked') if n.get(k) == 'true')
                print(f"{text!r} desc={desc!r} {n.get('class')} {n.get('bounds')} {flags}")
    elif cmd == 'find':
        pattern = re.compile(sys.argv[2], re.S)
        found = [n.get('text') or n.get('content-desc') for n in dump().iter('node') if matches(n, pattern)]
        print('\n'.join(found) if found else 'NOT FOUND')
        sys.exit(0 if found else 1)
    elif cmd == 'tap':
        pattern = re.compile(sys.argv[2], re.S)
        index = int(sys.argv[3]) if len(sys.argv) > 3 else 0
        hits = [n for n in dump().iter('node') if matches(n, pattern)]
        if len(hits) <= index:
            sys.exit(f'NOT FOUND: {sys.argv[2]}')
        x, y = center(hits[index].get('bounds'))
        adb('shell', 'input', 'tap', str(x), str(y))
        print(f"tapped {hits[index].get('text') or hits[index].get('content-desc')!r} at {x},{y}")
    elif cmd == 'wait':
        pattern = re.compile(sys.argv[2], re.S)
        deadline = time.time() + float(sys.argv[3] if len(sys.argv) > 3 else 15)
        while time.time() < deadline:
            found = [n.get('text') for n in dump().iter('node') if matches(n, pattern)]
            if found:
                print('\n'.join(t for t in found if t))
                return
            time.sleep(0.5)
        sys.exit(f'TIMEOUT waiting for {sys.argv[2]}')
    else:
        sys.exit(__doc__)


if __name__ == '__main__':
    main()
