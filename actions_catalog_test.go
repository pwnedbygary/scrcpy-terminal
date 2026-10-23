package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestActionDispatchIsTheGoldenWire performs every device action in the
// catalog and compares what reaches the control socket with expectations that
// do not come from the catalog: Android's published KeyEvent keycodes, laid out
// the way the hand-derived wire fixture lays out its keycode messages, and the
// fixture's own bytes for the scrcpy control messages.
func TestActionDispatchIsTheGoldenWire(t *testing.T) {
	fixture := map[string]string{}
	for _, c := range loadWireFixtures(t).Control {
		fixture[c.Name] = c.Hex
	}
	wire := func(name string) string {
		h, ok := fixture[name]
		if !ok {
			t.Fatalf("the wire fixture has no %q", name)
		}
		return h
	}
	// A keycode press is a down then an up, with no repeat and no meta state.
	keyPress := func(keycode uint32) string {
		return fmt.Sprintf("0000%08x0000000000000000", keycode) +
			fmt.Sprintf("0001%08x0000000000000000", keycode)
	}
	if down := keyPress(3)[:28]; down != wire("keycode_down_home") {
		t.Fatalf("keycode layout %s disagrees with the fixture's keycode_down_home %s", down, wire("keycode_down_home"))
	}
	// developer.android.com/reference/android/view/KeyEvent
	want := map[string]string{
		"home":       keyPress(3),   // KEYCODE_HOME
		"menu":       keyPress(82),  // KEYCODE_MENU
		"appswitch":  keyPress(187), // KEYCODE_APP_SWITCH
		"power":      keyPress(26),  // KEYCODE_POWER
		"voldown":    keyPress(25),  // KEYCODE_VOLUME_DOWN
		"volup":      keyPress(24),  // KEYCODE_VOLUME_UP
		"mute":       keyPress(164), // KEYCODE_VOLUME_MUTE; KEYCODE_MUTE (91) is the microphone
		"back":       wire("back_down") + wire("back_up"),
		"notif":      wire("expand_notification_panel"),
		"settings":   wire("expand_settings_panel"),
		"collapse":   wire("collapse_panels"),
		"rotate":     wire("rotate_device"),
		"resetvideo": wire("reset_video"),
	}
	for _, a := range actionCatalog.Device {
		expected, ok := want[a.ID]
		if !ok {
			t.Errorf("catalog device action %q has no golden wire expectation", a.ID)
			continue
		}
		act, ok := deviceActionFor(a.ID)
		if !ok {
			t.Errorf("deviceActionFor(%q) found no device action", a.ID)
			continue
		}
		conn := &capConn{}
		if err := performDeviceAction(newController(conn), act); err != nil {
			t.Errorf("%s: %v", a.ID, err)
			continue
		}
		if got := hex.EncodeToString(bytes.Join(conn.captured, nil)); got != expected {
			t.Errorf("%s puts %s on the wire, want %s", a.ID, got, expected)
		}
	}
	for id := range want {
		if _, ok := deviceActionFor(id); !ok {
			t.Errorf("golden expectation %q is not a device action in the catalog", id)
		}
	}
	for _, a := range actionCatalog.Local {
		if _, ok := deviceActionFor(a.ID); ok {
			t.Errorf("local action %q resolves to a device action", a.ID)
		}
	}
	if err := performDeviceAction(nil, actionCatalog.byID["home"]); err == nil {
		t.Error("a device action without a control socket reported success")
	}
}

// TestActionCatalogRejectsMalformedData: each front end would mishandle these
// differently, so the loader refuses them before any table is built.
func TestActionCatalogRejectsMalformedData(t *testing.T) {
	raw := string(actionsCatalogJSON)
	cases := []struct{ name, old, new string }{
		{"two actions share a chord", `"mnemonic": "t"`, `"mnemonic": "h"`},
		{"two chords differ only in case", `"mnemonic": "t"`, `"mnemonic": "H"`},
		{"a chord that is not ASCII", `"mnemonic": "t"`, `"mnemonic": "é"`},
		{"a control message listed twice", `["rotate_device"]`, `["rotate_device", "rotate_device"]`},
		{"Back listed three times", `["back_or_screen_on", "back_or_screen_on"]`, `["back_or_screen_on", "back_or_screen_on", "back_or_screen_on"]`},
		{"a gap in the F-keys", `"id": "collapse", "mnemonic": "c", "fkey": 11, `, `"id": "collapse", "mnemonic": "c", `},
		{"an F-key past F12", `"fkey": 12`, `"fkey": 13`},
		{"a control message the client cannot send", `["rotate_device"]`, `["spin_device"]`},
		{"a keycode action without a keycode", `"keycode": 3, `, ``},
		{"an unknown kind", `"kind": "app"`, `"kind": "gadget"`},
		{"an empty short label", `"short": "Home"`, `"short": ""`},
		{"a view naming no action", `"tuiMenu": ["home"`, `"tuiMenu": ["homer"`},
		{"a view listing an action twice", `"tuiMenu": ["home"`, `"tuiMenu": ["home", "home"`},
	}
	for _, tc := range cases {
		if !strings.Contains(raw, tc.old) {
			t.Fatalf("%s: the catalog no longer contains %q; update this case", tc.name, tc.old)
		}
		bad := strings.Replace(raw, tc.old, tc.new, 1)
		if _, err := loadActionsCatalog([]byte(bad)); err == nil {
			t.Errorf("%s: loadActionsCatalog accepted it", tc.name)
		}
	}
	if _, err := loadActionsCatalog(actionsCatalogJSON); err != nil {
		t.Fatalf("the shipped catalog does not load: %v", err)
	}
}

// protocol/fixtures/actions.json is the one definition of the app-level
// actions; the Android controller is tested against the same file, so the
// terminal, browser and Android tables cannot drift apart silently.
func TestAppActionsMatchSharedCatalog(t *testing.T) {
	raw, err := os.ReadFile("protocol/fixtures/actions.json")
	if err != nil {
		t.Fatal(err)
	}
	type action struct {
		ID       string `json:"id"`
		Mnemonic string `json:"mnemonic"`
		FKey     int    `json:"fkey"`
		Keycode  uint32 `json:"keycode"`
	}
	var catalog struct {
		Device []action `json:"device"`
		Local  []action `json:"local"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}

	mnemonics := map[byte]string{}
	fkeys := map[int]string{}
	for _, a := range append(append([]action{}, catalog.Device...), catalog.Local...) {
		if a.Mnemonic != "" {
			mnemonics[a.Mnemonic[0]] = a.ID
		}
		if a.FKey != 0 {
			fkeys[a.FKey] = a.ID
		}
	}
	if !reflect.DeepEqual(appMnemonicOps, mnemonics) {
		t.Errorf("appMnemonicOps = %v\ncatalog mnemonics = %v", appMnemonicOps, mnemonics)
	}
	if len(appFKeyOps) != len(fkeys) {
		t.Errorf("appFKeyOps has %d entries, catalog %d", len(appFKeyOps), len(fkeys))
	}
	for i, op := range appFKeyOps {
		if fkeys[i+1] != op {
			t.Errorf("F%d = %q, catalog says %q", i+1, op, fkeys[i+1])
		}
	}

	withKeycode := map[string]bool{}
	for _, a := range catalog.Device {
		if a.Keycode != 0 {
			withKeycode[a.ID] = true
			if got := appKeyOps[a.ID]; got != a.Keycode {
				t.Errorf("appKeyOps[%q] = %d, catalog says %d", a.ID, got, a.Keycode)
			}
		} else if !appActionOps[a.ID] {
			t.Errorf("catalog action %q is neither a keycode nor an app action op", a.ID)
		}
	}
	for op := range appKeyOps {
		if !withKeycode[op] {
			t.Errorf("appKeyOps[%q] is not in the catalog", op)
		}
	}
}
