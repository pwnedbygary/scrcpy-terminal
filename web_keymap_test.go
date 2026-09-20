package main

import (
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
// These tests are the guard against that drifting apart again, and they read
// the shipped player.js rather than a copy of its table, so a key the page
// names but the server does not implement fails here instead of silently doing
// nothing on the device.

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
		"mute":      91,
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

// playerOps pulls the op names out of the page's own key tables.
func playerOps(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("web/player.js")
	if err != nil {
		t.Fatalf("read player.js: %v", err)
	}
	src := string(b)

	ops := map[string]bool{}
	// The app key table: const APP_KEYS = { F1: "home", ... };
	m := regexp.MustCompile(`(?s)const APP_KEYS = \{(.*?)\n\};`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("cannot find APP_KEYS in web/player.js")
	}
	for _, v := range regexp.MustCompile(`"([a-z]+)"`).FindAllStringSubmatch(m[1], -1) {
		ops[v[1]] = true
	}
	if len(ops) < 11 {
		t.Fatalf("APP_KEYS looks truncated: %v", ops)
	}
	// Everything else the page sends as an op, so a typo is caught here too.
	for _, v := range regexp.MustCompile(`\{ op: "([a-z]+)"`).FindAllStringSubmatch(src, -1) {
		ops[v[1]] = true
	}
	for _, v := range regexp.MustCompile(`op: "([a-z]+)", code`).FindAllStringSubmatch(src, -1) {
		ops[v[1]] = true
	}
	// The mnemonic table's value can be a local action ("keyboard", "toolbar")
	// as well as a server op, so strip the slash entry (the only non-KeyX one)
	// before folding the rest in.
	if m := regexp.MustCompile(`(?s)const MNEMONIC_ACTIONS = \{(.*?)\n\};`).FindStringSubmatch(src); m != nil {
		body := regexp.MustCompile(`Key[A-Z]: "([a-z]+)"`).ReplaceAllString(m[1], `"$1"`)
		for _, v := range regexp.MustCompile(`"([a-z]+)"`).FindAllStringSubmatch(body, -1) {
			ops[v[1]] = true
		}
	}
	return ops
}

// playerMnemonicKeys parses the browser's MNEMONIC_ACTIONS into a normalised
// key (single uppercase letter, or SLASH) -> op map.
func playerMnemonicKeys(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("web/player.js")
	if err != nil {
		t.Fatalf("read player.js: %v", err)
	}
	m := regexp.MustCompile(`(?s)const MNEMONIC_ACTIONS = \{(.*?)\n\};`).
		FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("cannot find MNEMONIC_ACTIONS in web/player.js")
	}
	out := map[string]string{}
	for _, e := range regexp.MustCompile(`([A-Za-z]+): "([a-z]+)"`).FindAllStringSubmatch(m[1], -1) {
		name, op := e[1], e[2]
		if name == "Slash" {
			out["SLASH"] = op
			continue
		}
		name = strings.TrimPrefix(name, "Key")
		out[strings.ToUpper(name)] = op
	}
	if len(out) < 10 {
		t.Fatalf("MNEMONIC_ACTIONS looks truncated: %v", out)
	}
	return out
}

// TestMnemonicOpsMatchTheBrowser is the Alt+<letter> parity test: the browser's
// table and the terminal's appMnemonicOps must name the same chords for the
// same actions. This is the guard that keeps a phone (which has no F-keys)
// working the same way in the TUI, a browser tab and --window.
func TestMnemonicOpsMatchTheBrowser(t *testing.T) {
	web := playerMnemonicKeys(t)

	// One normalised spelling per chord: the terminal's byte uppercased, and
	// "Slash" for the punctuation chord.
	norm := func(s string) string {
		if s == "/" {
			return "SLASH"
		}
		return strings.ToUpper(s)
	}

	if len(web) != len(appMnemonicOps) {
		t.Errorf("chord count: browser has %d, terminal has %d", len(web), len(appMnemonicOps))
	}
	for key, op := range appMnemonicOps {
		want := norm(string(key))
		got, ok := web[want]
		if !ok {
			t.Errorf("Alt+%s: terminal has the chord, the browser does not", want)
			continue
		}
		if got != op {
			t.Errorf("Alt+%s: browser says %q, terminal says %q", want, got, op)
		}
	}
	for key := range web {
		letter := key
		if key == "SLASH" {
			letter = "/"
		}
		if len(letter) != 1 {
			t.Errorf("browser mnemonic %q is not a single key", key)
			continue
		}
		if _, ok := appMnemonicOps[strings.ToLower(letter)[0]]; !ok {
			t.Errorf("browser has Alt+%s, which the terminal does not", key)
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
		case opKeyboard, opMenuKey, "screenshot", "quit":
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

// TestPlayerKeysAreTheTerminalKeys is the parity test: every F-key action the
// terminal has, the page must have as well (except grab, which the page handles
// locally by design), and every op the page names must be one the server can
// actually perform.
func TestPlayerKeysAreTheTerminalKeys(t *testing.T) {
	ops := playerOps(t)

	for i, want := range appFKeyOps {
		if want == "grab" {
			continue // a local mode in the browser: no server op involved
		}
		if !ops[want] {
			t.Errorf("F%d: the terminal sends %q but web/player.js never does", i+1, want)
		}
	}

	// Ops that are not app-level keys but are still real: connection setup,
	// pointer/wheel, text, and the local actions (audio gain, quit, screenshot,
	// the software keyboard, the controls sheet) that never reach the server.
	extra := map[string]bool{
		"hello": true, "resize": true, "ping": true, "stats": true,
		"down": true, "move": true, "up": true, "wheel": true,
		"key": true, "text": true, "quit": true, "gain": true, "screenshot": true,
		"keyboard": true, "toolbar": true,
	}
	for op := range ops {
		if !isAppOp(op) && !extra[op] {
			t.Errorf("web/player.js sends op %q, which the server does not implement", op)
		}
	}
}

// TestToolbarButtonsAreRealActions: every data-act in index.html must be one
// of the app ops, a local player action, or a whitelisted op. A button that
// names something nobody handles would look like a dead button in the bar.
func TestToolbarButtonsAreRealActions(t *testing.T) {
	b, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	html := string(b)

	// Local actions the player handles in act() (and the toolbar's own
	// controls-sheet toggle, which is not an act at all).
	local := map[string]bool{
		"grab": true, "keyboard": true, "toolbar": true, "screenshot": true,
	}
	// Whitelisted server ops that are not app keys (mirrors the extra map in
	// TestPlayerKeysAreTheTerminalKeys).
	extra := map[string]bool{"quit": true}

	acts := regexp.MustCompile(`data-act="([a-z]+)"`).FindAllStringSubmatch(html, -1)
	if len(acts) < 15 {
		t.Fatalf("toolbar looks truncated: %d buttons", len(acts))
	}
	for _, m := range acts {
		act := m[1]
		if !isAppOp(act) && !local[act] && !extra[act] {
			t.Errorf("toolbar button %q has no handler (not an app op, not local)", act)
		}
	}
}

// TestWebOpsReachTheSameActionsAsTheTerminal checks the dispatch actually used
// by the browsers: the ops the page can send for app keys must resolve to the
// same Android keycode the terminal's F-keys produce.
func TestWebOpsReachTheSameActionsAsTheTerminal(t *testing.T) {
	// F1..F7 -> keycode, via the shared table.
	for i := 0; i < 7; i++ {
		fKeyCode := uint32(131 + i)
		op := appFKeyOp(fKeyCode)
		fromFKey := appKeyOps[op]
		fromWeb := headlessKey(op) // the path --web --no-tui uses
		if fromWeb != fromFKey || fromWeb == 0 {
			t.Errorf("F%d: terminal sends %d, browser headless sends %d",
				i+1, fromFKey, fromWeb)
		}
	}
	if strings.TrimSpace(appFKeyOp(142)) != "grab" {
		t.Errorf("F12 should be grab, got %q", appFKeyOp(142))
	}
}
