#!/usr/bin/env python3
"""Activates the scterm helper for the serving run shown on screen.

Only the port, token and scid are read from the command the app displays,
each checked against a strict pattern, and substituted into the fixed
template below (the one HelperBackend.activationCommand prints), so nothing
else from the screen reaches a shell.

    SERIAL=<adb serial> android/devicetest/activate_helper.py
"""
import re
import sys

import ui

SHOWN = re.compile(r"HelperMain (?P<port>\d{1,5}) (?P<token>[0-9a-fA-F]{64}) 4\.1 scid=(?P<scid>[0-9a-f]{8}) ")
TEMPLATE = (
    "CLASSPATH={apk} nohup app_process / io.github.pwnedbygary.scterm.helper.HelperMain "
    "{port} {token} 4.1 scid={scid} tunnel_forward=false cleanup=false power_on=false "
    "video_codec=h264 video_codec_options=priority=0,latency=1,max-bframes=0,vendor.qti-ext-enc-low-latency.enable=1 "
    "audio_codec=opus {audio} max_size=1920 log_level=info >/data/local/tmp/scterm-helper.log 2>&1 &"
)


def main():
    texts = [n.get('text') or '' for n in ui.dump().iter('node')]
    found = next((m for m in map(SHOWN.search, texts) if m), None)
    if not found or not 0 < int(found['port']) < 65536:
        sys.exit('no helper command on screen')
    path = ui.adb('shell', 'pm', 'path', 'io.github.pwnedbygary.scterm').stdout.strip()
    apk = re.fullmatch(r'package:(/data/app/[\w.=/~+-]+/base\.apk)', path)
    if not apk:
        sys.exit(f'unexpected apk path: {path!r}')
    # Same choice as HelperBackend.AUDIO_SOURCE.
    sdk = int(ui.adb('shell', 'getprop', 'ro.build.version.sdk').stdout.strip() or 0)
    audio = 'audio_source=playback audio_dup=true' if sdk >= 33 else 'audio_source=output'
    ui.adb('shell', TEMPLATE.format(apk=apk[1], audio=audio, **found.groupdict()))
    print(f"helper started for port {found['port']}")


if __name__ == '__main__':
    main()
