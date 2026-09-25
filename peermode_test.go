package main

import (
	"reflect"
	"testing"
)

func TestAudioCodecPreferencePutsTheChosenCodecFirst(t *testing.T) {
	cases := map[string][]string{
		"":     {"opus", "aac", "flac", "raw"},
		"opus": {"opus", "aac", "flac", "raw"},
		"flac": {"flac", "opus", "aac", "raw"},
		"raw":  {"raw", "opus", "aac", "flac"},
		"mp3":  {"opus", "aac", "flac", "raw"},
	}
	for chosen, want := range cases {
		if got := audioCodecPreference(chosen); !reflect.DeepEqual(got, want) {
			t.Errorf("audioCodecPreference(%q) = %v, want %v", chosen, got, want)
		}
	}
	// The shared default list must never be reordered by a previous call.
	audioCodecPreference("raw")
	if got := audioCodecPreference(""); got[0] != "opus" {
		t.Errorf("default order changed: %v", got)
	}
}

func TestDescribeGrants(t *testing.T) {
	if got := describeGrants([]string{"view", "control", "future"}); got != "view, control" {
		t.Errorf("got %q", got)
	}
	if got := describeGrants(nil); got != "do nothing" {
		t.Errorf("got %q", got)
	}
}
