package main

import (
	"maps"
	"net/http"
	"strings"
	"testing"
)

// TestWebPageRefusesFrames: the page tells the browser not to show it in a
// frame of another page: frame-ancestors 'none' in its Content-Security-Policy,
// and X-Frame-Options: DENY for browsers that do not read frame-ancestors. The
// page passes the Host check when it is opened as 127.0.0.1, so without them
// any website could put the live page in a frame and trick the viewer into
// clicks and keys that reach the device. The policy keeps every directive it
// had.
func TestWebPageRefusesFrames(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	resp, body := askAs(t, h.url(), "/", h.url(), "", false)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `<canvas id="screen"`) {
		t.Fatalf("GET /: status %d, want the page", resp.StatusCode)
	}
	if got := resp.Header.Values("X-Frame-Options"); len(got) != 1 || got[0] != "DENY" {
		t.Errorf("X-Frame-Options %q, want DENY, once", got)
	}
	policies := resp.Header.Values("Content-Security-Policy")
	if len(policies) != 1 {
		t.Fatalf("Content-Security-Policy %q, want one", policies)
	}
	got := map[string]string{}
	for _, directive := range strings.Split(policies[0], ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		if _, twice := got[name]; twice {
			t.Errorf("the policy has %s twice, and a browser reads only the first: %q", name, policies[0])
		}
		got[name] = strings.Join(fields[1:], " ")
	}
	want := map[string]string{
		"default-src":     "'none'",
		"script-src":      "'self' 'unsafe-inline'",
		"style-src":       "'self' 'unsafe-inline'",
		"connect-src":     "'self' ws: wss:",
		"img-src":         "'self' blob: data:",
		"media-src":       "'none'",
		"worker-src":      "'self' blob:",
		"frame-ancestors": "'none'",
	}
	if !maps.Equal(got, want) {
		t.Errorf("Content-Security-Policy %q,\nwant the directives %q", policies[0], want)
	}
}
