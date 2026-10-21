package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The browser's app-level keys and the terminal's F-keys must be the same
// actions. They were not: F1..F12 in the browser went to Android as Android's
// own F1..F12 keycodes, while the terminal turned the very same keys into
// HOME/MENU/APP_SWITCH/... -- so a key that worked in the TUI did nothing in
// the window, and "F5" meant volume-down in one place and F5 in the other.
//
// These tests are the guard against that drifting apart again. The page builds
// its key tables from the action catalog the server puts into index.html, so
// they run the shipped player.js's own derivation in node, fed the catalog
// this binary embeds, and compare what it builds with the terminal's tables.

// TestAppFKeysAreTheSharedOps pins F1..F12 to the shared table.
func TestAppFKeysAreTheSharedOps(t *testing.T) {
	for i, want := range appFKeyOps {
		code := uint32(131 + i) // what the terminal reports for F1..F12
		if got := appFKeyOp(code); got != want {
			t.Errorf("F%d (keycode %d): got %q, want %q", i+1, code, got, want)
		}
	}
	if got := appFKeyOp(130); got != "" {
		t.Errorf("keycode 130 is not an F-key, got %q", got)
	}
	if got := appFKeyOp(143); got != "" {
		t.Errorf("keycode 143 is not an F-key, got %q", got)
	}
}

// TestAppKeyOpsAreAndroidKeycodes pins the keycode half of the table to the
// Android constants, so a renumbering cannot pass unnoticed.
func TestAppKeyOpsAreAndroidKeycodes(t *testing.T) {
	want := map[string]uint32{
		"home":      3,
		"menu":      82,
		"appswitch": 187,
		"power":     26,
		"voldown":   25,
		"volup":     24,
		"mute":      164,
	}
	for op, code := range want {
		got, ok := appKeyOps[op]
		if !ok {
			t.Errorf("op %q missing from appKeyOps", op)
			continue
		}
		if got != code {
			t.Errorf("op %q: got keycode %d, want %d", op, got, code)
		}
		if !isAppOp(op) {
			t.Errorf("op %q is not recognised as an app op", op)
		}
	}
	// Every op the F-key table names must be dispatchable, or a key would be
	// mapped to an action that does nothing.
	for _, op := range appFKeyOps {
		if !isAppOp(op) {
			t.Errorf("F-key action %q is not dispatchable (isAppOp says no)", op)
		}
	}
	// And the action-only ops must not claim a keycode: applyAppOp would send a
	// bogus keycode 0 for them if they did.
	for op := range appActionOps {
		if _, ok := appKeyOps[op]; ok {
			t.Errorf("op %q is in both tables", op)
		}
	}
}

// browserKeyTables are the key tables web/player.js builds from the action
// catalog, keyed by KeyboardEvent.code as its key handler reads them.
type browserKeyTables struct {
	Alt      map[string]string `json:"alt"`      // Alt+<key>
	FKeys    map[string]string `json:"fkeys"`    // F1..F12
	AltFKeys map[string]string `json:"altFkeys"` // Alt+F1..F12
}

// playerKeyTables runs the page's own derivation (its input section, through
// web/page_fixture.mjs) in node, fed the catalog this binary embeds.
func playerKeyTables(t *testing.T) browserKeyTables {
	t.Helper()
	var tables browserKeyTables
	nodePageFixture(t, "F.playerKeyTables(catalog)", &tables)
	if len(tables.Alt) == 0 || len(tables.FKeys) == 0 {
		t.Fatalf("web/player.js built no key tables from the catalog: %+v", tables)
	}
	return tables
}

// chordOfCode is the character a KeyboardEvent.code value names on a US
// layout (UI Events KeyboardEvent code values, w3.org/TR/uievents-code), for
// the keys a chord can be: KeyA..KeyZ, Digit0..Digit9 and the punctuation.
func chordOfCode(code string) (byte, bool) {
	if len(code) == 4 && strings.HasPrefix(code, "Key") && code[3] >= 'A' && code[3] <= 'Z' {
		return code[3] + 'a' - 'A', true
	}
	if len(code) == 6 && strings.HasPrefix(code, "Digit") && code[5] >= '0' && code[5] <= '9' {
		return code[5], true
	}
	c, ok := map[string]byte{
		"Backquote": '`', "Minus": '-', "Equal": '=', "BracketLeft": '[', "BracketRight": ']',
		"Backslash": '\\', "Semicolon": ';', "Quote": '\'', "Comma": ',', "Period": '.', "Slash": '/',
	}[code]
	return c, ok
}

// TestMnemonicOpsMatchTheBrowser is the Alt+<letter> parity test: the browser's
// table and the terminal's appMnemonicOps must name the same chords for the
// same actions. This is the guard that keeps a phone (which has no F-keys)
// working the same way in the TUI, a browser tab and --window.
func TestMnemonicOpsMatchTheBrowser(t *testing.T) {
	web := playerKeyTables(t).Alt

	if len(web) != len(appMnemonicOps) {
		t.Errorf("chord count: browser has %d, terminal has %d", len(web), len(appMnemonicOps))
	}
	inBrowser := map[byte]bool{}
	for code, op := range web {
		c, ok := chordOfCode(code)
		if !ok {
			t.Errorf("browser chord Alt+%s is not a key the terminal can have", code)
			continue
		}
		inBrowser[c] = true
		want, ok := appMnemonicOps[c]
		if !ok {
			t.Errorf("browser has Alt+%s, which the terminal does not", code)
			continue
		}
		if op != want {
			t.Errorf("Alt+%s: browser says %q, terminal says %q", code, op, want)
		}
	}
	for c, op := range appMnemonicOps {
		if !inBrowser[c] {
			t.Errorf("Alt+%c (%s): terminal has the chord, the browser does not", c, op)
		}
	}
}

// TestMnemonicEntriesAreDispatchable checks every table entry actually does
// something, so a chord cannot name an action no front end implements.
func TestMnemonicEntriesAreDispatchable(t *testing.T) {
	for key, op := range appMnemonicOps {
		switch op {
		case "keyboard", "toolbar", "screenshot", "quit":
			continue // local actions, not server ops
		}
		if !isAppOp(op) {
			t.Errorf("Alt+%c maps to %q, which no dispatch path implements", key, op)
		}
	}
	// The menu's rows and the chord table must agree: every row op is either a
	// local action or a real op, and every chord appears in the menu with the
	// same letter (except the letterless Menu-key row).
	for _, it := range menuItems {
		switch it.Op {
		case "keyboard", "screenshot", "quit":
			continue
		}
		if !isAppOp(it.Op) {
			t.Errorf("menu row %q maps to %q, which no dispatch path implements", it.Label, it.Op)
		}
	}
	for key, op := range appMnemonicOps {
		if op == "toolbar" {
			continue // the menu itself has no row
		}
		it, ok := menuItemForKey(key)
		if !ok {
			t.Errorf("Alt+%c (%s) has no menu row", key, op)
			continue
		}
		if it.Op != op {
			t.Errorf("Alt+%c: chord table says %q, menu row says %q", key, op, it.Op)
		}
	}
}

// TestPlayerKeysAreTheTerminalKeys is the F-key parity test: the page's F1..F12
// and Alt+F1..F12 run exactly the terminal's F-key actions (F12 is grab, which
// the page toggles itself).
func TestPlayerKeysAreTheTerminalKeys(t *testing.T) {
	tables := playerKeyTables(t)
	if len(tables.FKeys) != len(appFKeyOps) || len(tables.AltFKeys) != len(appFKeyOps) {
		t.Errorf("F-keys: the browser has %d (Alt: %d), the terminal %d",
			len(tables.FKeys), len(tables.AltFKeys), len(appFKeyOps))
	}
	for i, want := range appFKeyOps {
		key := fmt.Sprintf("F%d", i+1)
		if got := tables.FKeys[key]; got != want {
			t.Errorf("%s: the browser runs %q, the terminal %q", key, got, want)
		}
		if got := tables.AltFKeys[key]; got != want {
			t.Errorf("Alt+%s: the browser runs %q, the terminal %q", key, got, want)
		}
	}
}

// TestPlayerNamesOnlyRealOps: the ops web/player.js sends by name rather than
// from the catalog must be ones the server implements, and the actions it runs
// by name must be catalog actions, so a typo fails here instead of silently
// doing nothing on the device.
func TestPlayerNamesOnlyRealOps(t *testing.T) {
	b, err := os.ReadFile("web/player.js")
	if err != nil {
		t.Fatalf("read player.js: %v", err)
	}
	src := string(b)
	find := func(re string) []string {
		var out []string
		for _, m := range regexp.MustCompile(re).FindAllStringSubmatch(src, -1) {
			out = append(out, m[1])
		}
		return out
	}

	// Ops that are not catalog actions but are still real: connection setup,
	// pointer/wheel, keys and text, and the host's audio gain.
	extra := map[string]bool{
		"hello": true, "resize": true, "ping": true, "stats": true,
		"down": true, "move": true, "up": true, "wheel": true,
		"key": true, "text": true, "gain": true,
	}
	ops := append(find(`\bop: "([a-z_]+)"`), find(`sendPos\("([a-z_]+)"`)...)
	if len(ops) < 8 {
		t.Fatalf("found only %v as ops in web/player.js", ops)
	}
	for _, op := range ops {
		if !isAppOp(op) && !extra[op] {
			t.Errorf("web/player.js sends op %q, which the server does not implement", op)
		}
	}
	acts := find(`\bact\("([a-z_]+)"\)`)
	if len(acts) == 0 {
		t.Fatal("found no action web/player.js runs by name")
	}
	for _, id := range acts {
		if _, ok := actionCatalog.byID[id]; !ok {
			t.Errorf("web/player.js runs action %q, which is not in the catalog", id)
		}
	}
}

// TestWebOpsReachTheSameActionsAsTheTerminal checks the dispatch actually used
// by the browsers: every device action the page can name puts the same bytes
// on the control socket as the terminal's dispatch (applyAppOp, behind the
// F-keys, the chords, the menu and the bar), through the browser dispatcher
// and through the one a web server without an app loop uses.
func TestWebOpsReachTheSameActionsAsTheTerminal(t *testing.T) {
	for _, act := range actionCatalog.Device {
		term := &capConn{}
		newTestApp(newController(term)).applyAppOp(act.ID)
		if len(term.captured) == 0 {
			t.Errorf("%s: the terminal sent nothing", act.ID)
		}
		want := bytes.Join(term.captured, nil)

		browser := &capConn{}
		withApp := &webServer{app: newTestApp(newController(browser)), volKick: make(chan struct{}, 1)}
		withApp.applyControl(nil, &webEvent{Op: act.ID})
		if got := bytes.Join(browser.captured, nil); !bytes.Equal(got, want) {
			t.Errorf("%s: browser dispatcher sent % x, terminal % x", act.ID, got, want)
		}

		headless := &capConn{}
		noApp := &webServer{ctrl: newController(headless), volKick: make(chan struct{}, 1)}
		noApp.applyControl(nil, &webEvent{Op: act.ID})
		if got := bytes.Join(headless.captured, nil); !bytes.Equal(got, want) {
			t.Errorf("%s: headless dispatcher sent % x, terminal % x", act.ID, got, want)
		}
	}
	for _, op := range []string{"nope", "", "HOME", "menukey", "keyboard", "screenshot", "toolbar"} {
		browser, headless := &capConn{}, &capConn{}
		(&webServer{app: newTestApp(newController(browser)), volKick: make(chan struct{}, 1)}).applyControl(nil, &webEvent{Op: op})
		(&webServer{ctrl: newController(headless), volKick: make(chan struct{}, 1)}).applyControl(nil, &webEvent{Op: op})
		if len(browser.captured)+len(headless.captured) != 0 {
			t.Errorf("op %q reached the device", op)
		}
	}
	if strings.TrimSpace(appFKeyOp(142)) != "grab" {
		t.Errorf("F12 should be grab, got %q", appFKeyOp(142))
	}
}

// failConn is a control socket whose writes fail after the first okWrites.
type failConn struct {
	capConn
	okWrites int
}

func (f *failConn) Write(p []byte) (int, error) {
	if len(f.captured) >= f.okWrites {
		return 0, errors.New("broken pipe")
	}
	return f.capConn.Write(p)
}

// TestVolumeActionsRefreshTheDeviceVolume: after a volume key the page's
// device volume is re-read at once, through either dispatcher, even when the
// write fails part-way (the key-down may already have reached the device) or
// there is no control socket. Other actions do not ask for a refresh.
func TestVolumeActionsRefreshTheDeviceVolume(t *testing.T) {
	controllers := map[string]func() *controller{
		"working":            func() *controller { return newController(&capConn{}) },
		"no controller":      func() *controller { return nil },
		"first write fails":  func() *controller { return newController(&failConn{}) },
		"second write fails": func() *controller { return newController(&failConn{okWrites: 1}) },
	}
	for name, ctrl := range controllers {
		for _, op := range []string{"volup", "voldown", "mute", "home"} {
			want := op != "home"
			withApp := &webServer{app: newTestApp(ctrl()), volKick: make(chan struct{}, 1)}
			withApp.applyControl(nil, &webEvent{Op: op})
			if got := len(withApp.volKick) == 1; got != want {
				t.Errorf("%s, %s, browser dispatcher: refresh asked = %v, want %v", name, op, got, want)
			}
			noApp := &webServer{ctrl: ctrl(), volKick: make(chan struct{}, 1)}
			noApp.applyControl(nil, &webEvent{Op: op})
			if got := len(noApp.volKick) == 1; got != want {
				t.Errorf("%s, %s, headless dispatcher: refresh asked = %v, want %v", name, op, got, want)
			}
		}
	}
}

// TestFailedKeyActionIsReported: a keycode action that cannot be written is
// reported on stderr, as the terminal always has; without a control socket
// (--control=false) there is nothing to report.
func TestFailedKeyActionIsReported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ctrl   *controller
		report bool
	}{
		{"failing socket", newController(&failConn{}), true},
		{"no controller", nil, false},
	} {
		out := captureStderr(t, func() { newTestApp(tc.ctrl).applyAppOp("home") })
		if got := strings.Contains(out, "scterm: inject key:"); got != tc.report {
			t.Errorf("%s: reported = %v, want %v (stderr %q)", tc.name, got, tc.report, out)
		}
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what
// it wrote. Tests are not parallel in this package, so the swap is safe.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return string(out)
}
