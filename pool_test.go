package main

import "testing"

// TestPoolPingPong verifies canvas pool reuse after returnPooled (A2): a slot
// returned by the renderer is handed back out with identical backing memory.
func TestPoolPingPong(t *testing.T) {
	s := &streamState{}
	n := 80 * 78 * 4

	a := s.takePooled(n)
	b := s.takePooled(n)
	if len(a) != n || len(b) != n {
		t.Fatalf("take sizes %d/%d, want %d", len(a), len(b), n)
	}
	s.returnPooled(a)
	c := s.takePooled(n)
	if &c[0] != &a[0] {
		t.Fatal("returned slot was not reused (pointer identity lost)")
	}
}

// TestPoolReturnDropsWhenFull: returning a third buffer while both pool slots
// already hold returned buffers must be a silent drop, never a panic.
func TestPoolReturnDropsWhenFull(t *testing.T) {
	s := &streamState{}
	n := 1024
	_ = s.takePooled(n) // fresh alloc (pool starts empty)
	_ = s.takePooled(n)

	q := make([]byte, n)
	r := make([]byte, n)
	extra := make([]byte, n)
	s.returnPooled(q)
	s.returnPooled(r) // pool now full: [q, r]

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("returnPooled panicked when full: %v", rec)
			}
		}()
		s.returnPooled(extra) // must drop silently
	}()

	for _, b := range s.canvasPool {
		if &b[0] == &extra[0] {
			t.Fatal("dropped buffer was stored in a pool slot")
		}
	}
	if s.canvasPool[0] == nil || s.canvasPool[1] == nil {
		t.Fatal("expected both slots occupied after two returns")
	}
}

// TestResetPoolStaleCanvas verifies resetPool (A1): after a geometry change the
// pooled canvases are discarded, so the next takePooled returns a fresh zeroed
// buffer (letterbox stays black instead of showing stale pixels).
func TestResetPoolStaleCanvas(t *testing.T) {
	s := &streamState{}
	n := 80 * 78 * 4

	a := s.takePooled(n)
	for i := range a {
		a[i] = 0xAB // simulate stale canvas bytes from the previous geometry
	}
	b := s.takePooled(n)
	s.returnPooled(b)

	s.resetPool()

	c := s.takePooled(n)
	if &c[0] == &a[0] {
		t.Fatal("resetPool did not discard pooled slots")
	}
	for i, v := range c {
		if v != 0 {
			t.Fatalf("stale byte at offset %d: %#x", i, v)
		}
	}
}

// TestMarkDirty verifies the C3 helper: markDirty sets the dirty flag and a new
// tui starts clean.
func TestMarkDirty(t *testing.T) {
	tu := &tui{}
	if tu.dirty {
		t.Fatal("fresh tui should not be dirty")
	}
	tu.markDirty()
	if !tu.dirty {
		t.Fatal("markDirty did not set dirty")
	}
}
