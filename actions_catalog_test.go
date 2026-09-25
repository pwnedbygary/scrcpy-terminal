package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

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
