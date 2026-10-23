package main

import (
	"testing"
	"time"
)

// TestExpireKeepsLeftInStep: the decoder counts what expire leaves over as
// left over, as it does after a read, so that the next read's own bytes are
// told from it (see feed): also once an Esc that waited, alone or with what
// could become a paste's start marker after it, is decoded after its 25 ms.
func TestExpireKeepsLeftInStep(t *testing.T) {
	for _, r := range []string{"\x1b", "\x1b\x1b", "\x1b\x1b[", "\x1b\x1b[2", "\x1b\x1b[20", "\x1b\x1b[200"} {
		c := newClockedInput(t)
		c.read(r)
		c.after(25 * time.Millisecond)
		if c.d.left != len(c.left) {
			t.Errorf("%q, 25 ms later: % x left over, but the decoder counts %d bytes", r, c.left, c.d.left)
		}
	}
}
