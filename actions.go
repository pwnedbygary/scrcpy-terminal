package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// the action catalog: one definition, every frontend
//
// protocol/fixtures/actions.json defines the app-level action surface: the
// Alt+letter chords, the F1..F12 row, what each action puts on the wire, each
// action's labels, and the order the TUI menu, the TUI action bar and the web
// toolbar list them in.
//
// The Go tables (appMnemonicOps, appKeyOps, appActionOps, appFKeyOps,
// menuItems, barItems) are derived from it at init, so adding an action is a
// catalog edit plus one dispatch case rather than another copy of a table. The
// web page gets the same file in index.html (webui.go) and builds its key
// tables, toolbar and controls sheet from it. The catalog is pinned from the
// other side by independent golden expectations
// (TestActionDispatchIsTheGoldenWire: published Android keycodes plus the
// hand-derived scrcpy wire fixture), by the Kotlin enum test in
// android/protocol, and by web_keymap_test.go, which runs the page's own
// derivation in node and compares it with the Go tables. CI runs all three.
// ---------------------------------------------------------------------------

//go:embed protocol/fixtures/actions.json
var actionsCatalogJSON []byte

// Action kinds: what performing the action does.
const (
	kindKeycode  = "keycode"  // an Android keycode, sent as a down/up pair
	kindControl  = "control"  // exactly the scrcpy control messages it names
	kindApp      = "app"      // frontend state every frontend shares (grab, quit)
	kindFrontend = "frontend" // each frontend implements its own effect
)

type catalogAction struct {
	ID       string   `json:"id"`
	Mnemonic string   `json:"mnemonic"`
	FKey     int      `json:"fkey"`
	Keycode  uint32   `json:"keycode"`
	Kind     string   `json:"kind"`
	Label    string   `json:"label"`
	Short    string   `json:"short"`
	Control  []string `json:"control"`
}

type actionsCatalog struct {
	Version int             `json:"version"`
	Device  []catalogAction `json:"device"`
	Local   []catalogAction `json:"local"`
	Views   struct {
		TUIMenu []string `json:"tuiMenu"`
		TUIBar  []string `json:"tuiBar"`
		WebBar  []string `json:"webBar"`
	} `json:"views"`

	byID map[string]catalogAction
}

// loadActionsCatalog parses the catalog and rejects a malformed one before any
// table is built: a chord that names nothing, a duplicate letter, or a view
// pointing at an action that does not exist are data errors, and the front ends
// would each fail differently.
func loadActionsCatalog(raw []byte) (*actionsCatalog, error) {
	var c actionsCatalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("action catalog: %w", err)
	}
	c.byID = map[string]catalogAction{}

	mnemonics := map[string]string{}
	fkeys := map[int]string{}
	add := func(a catalogAction) error {
		if a.ID == "" {
			return fmt.Errorf("action catalog: an entry has no id")
		}
		if _, dup := c.byID[a.ID]; dup {
			return fmt.Errorf("action catalog: duplicate id %q", a.ID)
		}
		switch a.Kind {
		case kindKeycode:
			if a.Keycode == 0 {
				return fmt.Errorf("action %q is kind %q with no keycode", a.ID, a.Kind)
			}
			if len(a.Control) > 0 {
				return fmt.Errorf("action %q is kind %q but names control messages", a.ID, a.Kind)
			}
		case kindControl:
			if len(a.Control) == 0 {
				return fmt.Errorf("action %q is kind %q but names no control message", a.ID, a.Kind)
			}
			if a.Keycode != 0 {
				return fmt.Errorf("action %q is kind %q but carries a keycode", a.ID, a.Kind)
			}
			listed := map[string]int{}
			for _, name := range a.Control {
				if _, ok := controlSenders[name]; !ok {
					return fmt.Errorf("action %q names control %q, which the client cannot send", a.ID, name)
				}
				listed[name]++
			}
			// back_or_screen_on listed twice is one press, down then up, which
			// performDeviceAction sends once as the full press. Any other repeat
			// would mean one message here and several to a client that sends
			// each listed message, such as the Android one.
			for name, n := range listed {
				if n > 1 && (name != "back_or_screen_on" || n != 2) {
					return fmt.Errorf("action %q lists control %q %d times", a.ID, name, n)
				}
			}
		case kindApp, kindFrontend:
			if a.Keycode != 0 || len(a.Control) > 0 {
				return fmt.Errorf("action %q is kind %q but carries wire effects", a.ID, a.Kind)
			}
		default:
			return fmt.Errorf("action %q has unknown kind %q", a.ID, a.Kind)
		}
		if a.Mnemonic != "" {
			if len(a.Mnemonic) != 1 || a.Mnemonic[0] <= ' ' || a.Mnemonic[0] > '~' {
				return fmt.Errorf("action %q mnemonic %q is not one visible ASCII character", a.ID, a.Mnemonic)
			}
			// Alt+H and Alt+h are the same chord (appMnemonicOps folds case).
			chord := strings.ToLower(a.Mnemonic)
			if owner, dup := mnemonics[chord]; dup {
				return fmt.Errorf("action catalog: %q and %q share the chord Alt+%s", owner, a.ID, chord)
			}
			mnemonics[chord] = a.ID
		}
		if a.FKey != 0 {
			if a.FKey < 1 || a.FKey > 12 {
				return fmt.Errorf("action %q has F-key %d, outside F1..F12", a.ID, a.FKey)
			}
			if owner, dup := fkeys[a.FKey]; dup {
				return fmt.Errorf("action catalog: %q and %q share F%d", owner, a.ID, a.FKey)
			}
			fkeys[a.FKey] = a.ID
		}
		if a.Label == "" || a.Short == "" {
			return fmt.Errorf("action %q has no label or short label", a.ID)
		}
		c.byID[a.ID] = a
		return nil
	}
	for _, a := range append(append([]catalogAction{}, c.Device...), c.Local...) {
		if err := add(a); err != nil {
			return nil, err
		}
	}

	// F-keys must be F1..Fn with no gap: appFKeyOp indexes the derived list by
	// F-key, and a hole would silently shift every later F-key onto the wrong
	// action.
	for i := 1; i <= len(fkeys); i++ {
		if _, ok := fkeys[i]; !ok {
			return nil, fmt.Errorf("action catalog: no action has F%d, and F-keys must be contiguous", i)
		}
	}

	views := map[string][]string{"tuiMenu": c.Views.TUIMenu, "tuiBar": c.Views.TUIBar, "webBar": c.Views.WebBar}
	for name, ids := range views {
		if len(ids) == 0 {
			return nil, fmt.Errorf("action catalog: view %q is empty", name)
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if _, ok := c.byID[id]; !ok {
				return nil, fmt.Errorf("view %q lists %q, which is not an action", name, id)
			}
			if seen[id] {
				return nil, fmt.Errorf("view %q lists %q twice", name, id)
			}
			seen[id] = true
		}
	}
	return &c, nil
}

func mustLoadActionsCatalog(raw []byte) *actionsCatalog {
	c, err := loadActionsCatalog(raw)
	if err != nil {
		panic(err) // embedded data: a failure here is a build-time data error
	}
	return c
}

var actionCatalog = mustLoadActionsCatalog(actionsCatalogJSON)

// catalogActions returns every action in catalog order (device, then local).
func catalogActions() []catalogAction {
	return append(append([]catalogAction{}, actionCatalog.Device...), actionCatalog.Local...)
}

// appMnemonicOps maps Alt+<letter> to an app-level action. Letters are chosen
// so that a chord does nothing unless the catalog maps it: Ctrl+A..Z (Android
// shortcuts) and unmapped Alt+letters are untouched.
var appMnemonicOps = func() map[byte]string {
	m := map[byte]string{}
	for _, a := range catalogActions() {
		if a.Mnemonic == "" {
			continue
		}
		c := []rune(a.Mnemonic)[0]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		m[byte(c)] = a.ID
	}
	return m
}()

// appKeyOps maps an app-level action to the Android keycode it sends.
var appKeyOps = func() map[string]uint32 {
	m := map[string]uint32{}
	for _, a := range catalogActions() {
		if a.Kind == kindKeycode {
			m[a.ID] = a.Keycode
		}
	}
	return m
}()

// appActionOps are the app-level actions that are not a keycode: scrcpy control
// messages (rotate/notif/settings/collapse/resetvideo/back) and shared frontend
// state (grab, quit).
var appActionOps = func() map[string]bool {
	m := map[string]bool{}
	for _, a := range catalogActions() {
		if a.Kind == kindControl || a.Kind == kindApp {
			m[a.ID] = true
		}
	}
	return m
}()

// appFKeyOps lists the F1..F12 actions in F-key order.
var appFKeyOps = func() []string {
	var withFKey []catalogAction
	for _, a := range catalogActions() {
		if a.FKey != 0 {
			withFKey = append(withFKey, a)
		}
	}
	sort.Slice(withFKey, func(i, j int) bool { return withFKey[i].FKey < withFKey[j].FKey })
	ops := make([]string, 0, len(withFKey))
	for _, a := range withFKey {
		ops = append(ops, a.ID)
	}
	return ops
}()

// menuItems is the TUI action menu, in catalog view order.
var menuItems = func() []menuItem {
	var out []menuItem
	for _, id := range actionCatalog.Views.TUIMenu {
		a := actionCatalog.byID[id]
		out = append(out, menuItem{Key: mnemonicByte(a.Mnemonic), Op: a.ID, Label: a.Label})
	}
	return out
}()

// barItems is the TUI action bar, in catalog view order.
var barItems = func() []barItem {
	var out []barItem
	for _, id := range actionCatalog.Views.TUIBar {
		a := actionCatalog.byID[id]
		out = append(out, barItem{menuItem{Key: mnemonicByte(a.Mnemonic), Op: a.ID, Label: a.Label}, a.Short})
	}
	return out
}()

// mnemonicByte is the chord letter of an action, 0 when it has none.
func mnemonicByte(mnemonic string) byte {
	if mnemonic == "" {
		return 0
	}
	r := []rune(mnemonic)[0]
	if r >= 'A' && r <= 'Z' {
		r += 'a' - 'A'
	}
	if r > 0xFF {
		return 0
	}
	return byte(r)
}

// controlSenders maps a catalog control name to the call that puts it on the
// wire. back_or_screen_on is the full press (down then up), which is what the
// catalog listing it twice describes.
var controlSenders = map[string]func(*controller) error{
	"back_or_screen_on":         func(c *controller) error { return c.back() },
	"expand_notification_panel": func(c *controller) error { return c.expandNotifications() },
	"expand_settings_panel":     func(c *controller) error { return c.expandSettings() },
	"collapse_panels":           func(c *controller) error { return c.collapsePanels() },
	"rotate_device":             func(c *controller) error { return c.rotate() },
	"reset_video":               func(c *controller) error { return c.resetVideo() },
}

// deviceActionFor resolves an op id to an action that has a wire effect, so the
// terminal, the browser dispatcher and the headless dispatcher all perform the
// same action from the same definition.
func deviceActionFor(op string) (catalogAction, bool) {
	a, ok := actionCatalog.byID[op]
	if !ok || (a.Kind != kindKeycode && a.Kind != kindControl) {
		return catalogAction{}, false
	}
	return a, true
}

// performDeviceAction puts one device action on the wire.
func performDeviceAction(c *controller, act catalogAction) error {
	if c == nil {
		return errNoControl
	}
	if act.Kind == kindKeycode {
		return c.injectKey(act.Keycode, 0)
	}
	sent := map[string]bool{}
	for _, name := range act.Control {
		if sent[name] {
			continue // a name listed twice is one full press, not two
		}
		sent[name] = true
		if err := controlSenders[name](c); err != nil {
			return err
		}
	}
	return nil
}

// actionTouchesDeviceVolume reports whether an action changes the device's own
// volume, so a frontend can refresh its volume readout after it.
func actionTouchesDeviceVolume(act catalogAction) bool {
	return act.Kind == kindKeycode && isVolumeKey(act.Keycode)
}
