#!/usr/bin/env python3
"""Serves one APK as scterm's "latest release", in GitHub's JSON, to test the
in-app update without publishing anything:

    android/devicetest/update_server.py NEW.apk 0.0.2 [PORT]
    adb reverse tcp:8765 tcp:8765
    ./gradlew :app:assembleDebug -Pscterm.updateUrl=http://127.0.0.1:8765/latest

Install that debug build, then "Check for updates" in the app. NEW.apk must be
signed like the installed app (the debug key, for debug builds) and have a
higher version code: -Pscterm.version=0.0.2 gives it version code 2.
"""
import hashlib
import http.server
import json
import os
import sys


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    apk, version = sys.argv[1], sys.argv[2]
    port = int(sys.argv[3]) if len(sys.argv) > 3 else 8765
    with open(apk, 'rb') as f:
        data = f.read()
    name = f'scterm-android-{version}.apk'
    release = {
        'tag_name': f'v{version}',
        'html_url': f'http://127.0.0.1:{port}/',
        'body': f'Test release {version}, served from {os.path.basename(apk)}.',
        'assets': [{
            'name': name,
            'browser_download_url': f'http://127.0.0.1:{port}/{name}',
            'size': len(data),
            'digest': 'sha256:' + hashlib.sha256(data).hexdigest(),
        }],
    }

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path == '/latest':
                body, kind = json.dumps(release).encode(), 'application/json'
            elif self.path == '/' + name:
                body, kind = data, 'application/vnd.android.package-archive'
            else:
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header('Content-Type', kind)
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    print(f'serving {name} ({len(data)} bytes) at http://127.0.0.1:{port}/latest', flush=True)
    http.server.HTTPServer(('127.0.0.1', port), Handler).serve_forever()


if __name__ == '__main__':
    main()
