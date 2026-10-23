package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

// statusNum is a figure of a status message, 0 when it is missing.
func statusNum(st map[string]any, key string) float64 {
	v, _ := st[key].(float64)
	return v
}

// statusFigures sums up a status message's counters, for failures.
func statusFigures(st map[string]any) string {
	if st == nil {
		return "no status"
	}
	return fmt.Sprintf("sent %v, dropped %v, kbps %v, rawBytes %v", st["sent"], st["dropped"], st["kbps"], st["rawBytes"])
}

// statusUntil reads cl's messages up to a status that ok accepts, and returns
// it. It fails the test, with the last status read, when none comes within 5 s.
func statusUntil(t *testing.T, cl *testClient, what string, ok func(st map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last map[string]any
	for {
		if err := cl.conn.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		op, payload, err := cl.ws.ReadMessage()
		if err != nil {
			t.Fatalf("no status with %s within 5s (the last: %s): %v", what, statusFigures(last), err)
		}
		var st map[string]any
		if op != wsOpText || json.Unmarshal(payload, &st) != nil || st["op"] != "status" {
			continue
		}
		if last = st; ok(st) {
			return st
		}
	}
}

// TestWebStatusCountsDroppedFrames: frames offered faster than a viewer takes
// them replace one another in its mailbox, and its status counts each frame it
// missed that way, since it connected. A viewer that takes every frame misses
// none. The slow viewer's sender is held on the first of six frames, so the
// count is exact: the last frame waits in its mailbox, and the four between
// were each replaced unsent.
func TestWebStatusCountsDroppedFrames(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	slow := rawViewer(t, h.url())
	fast := rawViewer(t, h.url())
	waitClients(t, h.srv, 2)
	h.srv.clientsMu.Lock()
	sc := h.srv.clients[0]
	h.srv.clientsMu.Unlock()
	let := stall(t, &sc.cfgMu)

	cv := computeWebCanvas(16, 16, 16, 16)
	const frames = 6
	for i := range frames {
		h.srv.frame(&videoFrame{rgb: makeCanvas(cv.cw, cv.ch, byte(i)), w: 16, h: 16, cw: cv.cw, ch: cv.ch, fps: 60})
		fast.nextBinary(webMsgVideo, 5*time.Second)
		if i == 0 {
			eventually(t, "the slow sender holds the first frame", func() bool { return mailbox(sc) == nil })
		}
	}
	slow.send(webEvent{Op: "ping"}) // answered by its reader, while its sender is held
	st := statusUntil(t, slow, "4 frames dropped", func(st map[string]any) bool { return statusNum(st, "dropped") == frames-2 })
	if sent := statusNum(st, "sent"); sent != 0 {
		t.Errorf("the held viewer's status says %v frames sent, want 0", sent)
	}
	let()
	st = statusUntil(t, slow, "2 frames sent", func(st map[string]any) bool { return statusNum(st, "sent") == 2 })
	if dropped := statusNum(st, "dropped"); dropped != frames-2 {
		t.Errorf("once the held viewer has sent its 2 frames, its status says %v dropped, want %d", dropped, frames-2)
	}
	st = statusUntil(t, fast, "6 frames sent", func(st map[string]any) bool { return statusNum(st, "sent") == frames })
	if dropped := statusNum(st, "dropped"); dropped != 0 {
		t.Errorf("the viewer that took every frame has a status that says %v dropped, want 0", dropped)
	}
}

// TestWebStatusCountsFramesItCannotSend: a frame its sender takes but cannot
// send is one the viewer missed too: one too short for its canvas, to a JPEG
// or a raw viewer (the sender checks that before it tells them apart), one
// that no JPEG encoder opens for (libavcodec's takes at most 65500 pixels a
// side), and one whose encode fails, with an encoder planted in the server's
// pool: its JPEG does not fit, or it has no handle, which is an encode error
// the sender also counts in encErrors.
// The sender takes each before the next frame comes, so no newer frame
// replaces it in the mailbox: only the sender can count it.
func TestWebStatusCountsFramesItCannotSend(t *testing.T) {
	cv := computeWebCanvas(64, 64, 64, 64)
	// missOne hands a viewer f, and then a frame it can send.
	missOne := func(t *testing.T, h *webTestHarness, viewer func(*testing.T, string) *testClient, f *videoFrame) {
		t.Helper()
		cl := viewer(t, h.url())
		sc := onlyClient(t, h.srv)
		h.srv.frame(f)
		eventually(t, "its sender has taken the frame", func() bool { return mailbox(sc) == nil })
		h.srv.frame(&videoFrame{rgb: makeCanvas(cv.cw, cv.ch, 1), w: 64, h: 64, cw: cv.cw, ch: cv.ch, fps: 60})
		cl.nextBinary(webMsgVideo, 5*time.Second)
		st := statusUntil(t, cl, "1 frame sent", func(st map[string]any) bool { return statusNum(st, "sent") == 1 })
		if dropped := statusNum(st, "dropped"); dropped != 1 {
			t.Errorf("the status says %v frames dropped, want 1: the frame its sender could not send", dropped)
		}
	}
	t.Run("too short for its canvas", func(t *testing.T) {
		h := newWebTestHarness(t, config{web: true}, false)
		missOne(t, h, helloClient, &videoFrame{rgb: make([]byte, cv.cw*cv.ch*4-4), w: 64, h: 64, cw: cv.cw, ch: cv.ch, fps: 60})
	})
	t.Run("too short for its canvas, to a raw viewer", func(t *testing.T) {
		h := newWebTestHarness(t, config{web: true}, false)
		missOne(t, h, rawViewer, &videoFrame{rgb: make([]byte, cv.cw*cv.ch*4-4), w: 64, h: 64, cw: cv.cw, ch: cv.ch, fps: 60})
	})
	t.Run("no encoder opens for it", func(t *testing.T) {
		h := newWebTestHarness(t, config{web: true}, false)
		wide := computeWebCanvas(70000, 2, 70000, 2)
		if size := h.srv.encoderMaxSize(wide); size > 0 {
			t.Fatalf("an encoder opened for %dx%d", wide.cw, wide.ch)
		}
		missOne(t, h, helloClient, &videoFrame{rgb: make([]byte, wide.cw*wide.ch*4), w: wide.cw, h: wide.ch, cw: wide.cw, ch: wide.ch, fps: 60})
	})
	t.Run("its encode fails", func(t *testing.T) {
		h := newWebTestHarness(t, config{web: true}, false)
		small := computeWebCanvas(32, 32, 32, 32)
		hnd := jencOpen(small.cw, small.ch, h.srv.quality())
		if hnd == nil {
			t.Fatalf("no JPEG encoder opens for %dx%d", small.cw, small.ch)
		}
		// No JPEG fits its 1-byte buffer: encodeInto fails with errJencTooSmall.
		h.srv.encMu.Lock()
		h.srv.enc[uint64(uint32(small.cw))<<32|uint64(uint32(small.ch))] = &jpegEncoder{h: hnd, w: small.cw, hgt: small.ch, maxSize: 1}
		h.srv.encMu.Unlock()
		missOne(t, h, helloClient, &videoFrame{rgb: makeCanvas(small.cw, small.ch, 1), w: 32, h: 32, cw: small.cw, ch: small.ch, fps: 60})
	})
	t.Run("its encode fails, with no handle", func(t *testing.T) {
		h := newWebTestHarness(t, config{web: true}, false)
		small := computeWebCanvas(32, 32, 32, 32)
		// With no handle, encodeInto fails with "jpeg encoder unavailable".
		h.srv.encMu.Lock()
		h.srv.enc[uint64(uint32(small.cw))<<32|uint64(uint32(small.ch))] = &jpegEncoder{w: small.cw, hgt: small.ch, maxSize: 1}
		h.srv.encMu.Unlock()
		missOne(t, h, helloClient, &videoFrame{rgb: makeCanvas(small.cw, small.ch, 1), w: 32, h: 32, cw: small.cw, ch: small.ch, fps: 60})
		if n := onlyClient(t, h.srv).encErrors.Load(); n != 1 {
			t.Errorf("the sender counts %d encode errors, want 1: the frame whose encoder has no handle", n)
		}
	})
}

// TestWebStatusAccountsForEveryFrame: under load, each frame offered to a
// viewer is either sent to it or counted as dropped. Once a viewer's sender
// has caught up, its status says that sent and dropped add up to the frames
// offered, and the viewer got exactly the frames it says were sent. 600 frames
// come back to back, faster than a sender takes them, to a JPEG and a raw
// viewer. Run it with -race: the demux goroutine (here the test's), each
// sender and each reader touch the counters.
func TestWebStatusAccountsForEveryFrame(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	viewers := map[string]*testClient{"JPEG": helloClient(t, h.url()), "raw": rawViewer(t, h.url())}
	waitClients(t, h.srv, len(viewers))
	cv := computeWebCanvas(64, 64, 64, 64)
	const frames = 600
	for range frames {
		h.srv.frame(&videoFrame{rgb: make([]byte, cv.cw*cv.ch*4), w: 64, h: 64, cw: cv.cw, ch: cv.ch, fps: 60})
	}
	errs := make(chan error, len(viewers))
	for name, cl := range viewers {
		go func() {
			sent, dropped, err := accountFor(cl, frames)
			if err != nil {
				errs <- fmt.Errorf("the %s viewer: %v", name, err)
				return
			}
			t.Logf("the %s viewer: %d frames sent, %d dropped", name, sent, dropped)
			errs <- nil
		}()
	}
	for range viewers {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// accountFor reads a viewer's messages up to a status in which its sent and
// dropped frames add up to offered, and checks that it got as many frames as
// that status says were sent.
func accountFor(cl *testClient, offered int) (sent, dropped int, err error) {
	if err := cl.conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return 0, 0, err
	}
	got := 0
	for {
		op, payload, err := cl.ws.ReadMessage()
		if err != nil {
			return 0, 0, fmt.Errorf("after %d frames (last status: %d sent, %d dropped): %v", got, sent, dropped, err)
		}
		if op == wsOpBinary && len(payload) > 0 && payload[0] == webMsgVideo {
			got++
			continue
		}
		var st map[string]any
		if op != wsOpText || json.Unmarshal(payload, &st) != nil || st["op"] != "status" {
			continue
		}
		sent, dropped = int(statusNum(st, "sent")), int(statusNum(st, "dropped"))
		if sent+dropped < offered {
			continue
		}
		if sent+dropped != offered || got != sent {
			return sent, dropped, fmt.Errorf("a status says %d sent and %d dropped of %d frames offered, after the viewer got %d",
				sent, dropped, offered, got)
		}
		return sent, dropped, nil
	}
}

// TestWebStatusCountsRawBytes: a raw viewer's status counts the bytes of the
// raw frames it was sent since it connected, each a 16-byte header and the
// pixels, which is what the viewer got in its video messages; a JPEG viewer's
// counts none.
func TestWebStatusCountsRawBytes(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	raw := rawViewer(t, h.url())
	jpg := helloClient(t, h.url())
	cv := computeWebCanvas(64, 32, 64, 32)
	const frames = 3
	got := 0
	for i := range frames {
		h.srv.frame(&videoFrame{rgb: makeCanvas(cv.cw, cv.ch, byte(i)), w: 64, h: 32, cw: cv.cw, ch: cv.ch, fps: 60})
		msg := raw.nextBinary(webMsgVideo, 5*time.Second)
		if want := 16 + 64*32*4; len(msg) != want {
			t.Fatalf("a raw video message of %d bytes, want %d: a 16-byte header and 64x32 BGR0 pixels", len(msg), want)
		}
		got += len(msg)
		jpg.nextBinary(webMsgVideo, 5*time.Second)
	}
	st := statusUntil(t, raw, "3 frames sent", func(st map[string]any) bool { return statusNum(st, "sent") == frames })
	if n := statusNum(st, "rawBytes"); n != float64(got) {
		t.Errorf("the raw viewer's status counts %v raw bytes; it was sent %d", n, got)
	}
	st = statusUntil(t, jpg, "3 frames sent", func(st map[string]any) bool { return statusNum(st, "sent") == frames })
	if n := statusNum(st, "rawBytes"); n != 0 {
		t.Errorf("the JPEG viewer's status counts %v raw bytes, want 0", n)
	}
}

// TestWebStatusKbps: each periodic status carries the rate of what its viewer
// was sent since the status before, neither of them counted, in kilobits per
// second: video, sound and any other message, and 0 while nothing goes out.
// The viewer counts the bytes it gets between two statuses, which, but for the
// frames' few header bytes, is what the server counted: so the rate is 0
// exactly when the viewer got nothing, and otherwise gives what it got in
// about the time between the two. The viewer is sent frames, then sound alone,
// which only the status tick sends, just after its status, then nothing.
func TestWebStatusKbps(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cl := rawViewer(t, h.url()) // its hello names no caps, so it takes sound too
	cv := computeWebCanvas(64, 64, 64, 64)

	// feed calls f every 5 ms until the returned func stops it.
	feed := func(f func()) (stop func()) {
		quit, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-quit:
					return
				case <-time.After(5 * time.Millisecond):
					f()
				}
			}
		}()
		stop = sync.OnceFunc(func() { close(quit); <-done })
		t.Cleanup(stop)
		return stop
	}

	// A window is what came between two statuses, and the rate the later says.
	type window struct {
		kbps                float64
		video, sound, other int // message bytes
		took                time.Duration
	}
	var at time.Time
	next := func() window {
		t.Helper()
		var w window
		if err := cl.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		for {
			op, payload, err := cl.ws.ReadMessage()
			if err != nil {
				t.Fatalf("no status within 5s: %v", err)
			}
			var st map[string]any
			switch {
			case op == wsOpBinary && len(payload) > 0 && payload[0] == webMsgVideo:
				w.video += len(payload)
			case op == wsOpBinary && len(payload) > 0 && payload[0] == webMsgAudio:
				w.sound += len(payload)
			case op == wsOpText && json.Unmarshal(payload, &st) == nil && st["op"] == "status":
				now := time.Now()
				w.kbps, w.took, at = statusNum(st, "kbps"), now.Sub(at), now
				return w
			default:
				w.other += len(payload)
			}
		}
	}
	// until reads windows up to one that ok accepts, failing the test on a rate
	// that does not match what the viewer got.
	until := func(what string, ok func(window) bool) {
		t.Helper()
		for range 8 {
			w := next()
			got := w.video + w.sound + w.other
			switch {
			case got == 0 && w.kbps != 0:
				t.Fatalf("a status says %.1f kbps after the viewer got nothing", w.kbps)
			case got != 0 && w.kbps == 0:
				t.Fatalf("a status says 0 kbps after the viewer got %d bytes (%d video, %d sound, %d other)", got, w.video, w.sound, w.other)
			case got != 0:
				if implied := time.Duration(float64(got) * 8 / w.kbps * float64(time.Millisecond)); implied < w.took/2 || implied > w.took*2 {
					t.Fatalf("a status says %.1f kbps after the viewer got %d bytes in %v, which that rate takes %v to send",
						w.kbps, got, w.took, implied)
				}
			}
			if ok(w) {
				t.Logf("%s: %d bytes in %v, %.1f kbps", what, got, w.took.Round(time.Millisecond), w.kbps)
				return
			}
		}
		t.Fatalf("no status in 8 for %s", what)
	}

	next() // part of what came before this status went unread: it only starts the count
	stop := feed(func() {
		h.srv.frame(&videoFrame{rgb: make([]byte, cv.cw*cv.ch*4), w: 64, h: 64, cw: cv.cw, ch: cv.ch, fps: 60})
	})
	until("video", func(w window) bool { return w.video > 0 })
	stop()
	pcm := make([]byte, 960) // 5 ms of s16 stereo at 48 kHz
	stop = feed(func() { h.srv.pushPCM(pcm) })
	until("sound alone", func(w window) bool { return w.sound > 0 && w.video == 0 })
	stop()
	until("nothing", func(w window) bool { return w.video+w.sound+w.other == 0 })
}

// TestWebStatusAnswersAPingDuringATick: a viewer's reader answers a ping with
// a status, so it reads the rate that the viewer's sender stores at each tick.
// The test holds the viewer's socket while it pings, so that the reader reads
// the rate and waits to write it while a tick stores the next one: with -race,
// that is the pair the rate's atomic keeps apart. Once the socket is let go,
// both statuses go out: the tick's says 0, as nothing was written in its
// window, and the answer the rate before (or that 0, had the reader been late).
func TestWebStatusAnswersAPingDuringATick(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cl := rawViewer(t, h.url())
	sc := onlyClient(t, h.srv)
	cv := computeWebCanvas(64, 64, 64, 64)
	const frames = 3
	for range frames {
		h.srv.frame(&videoFrame{rgb: make([]byte, cv.cw*cv.ch*4), w: 64, h: 64, cw: cv.cw, ch: cv.ch, fps: 60})
	}
	// The first status that accounts for every frame rates a window in which
	// one went out, and nothing goes out after it.
	st := statusUntil(t, cl, "3 frames sent or dropped", func(st map[string]any) bool {
		return statusNum(st, "sent")+statusNum(st, "dropped") == frames
	})
	before := statusNum(st, "kbps")
	if before <= 0 {
		t.Fatalf("the status once the frames went out says %v kbps", before)
	}

	let := stall(t, &sc.ws.wmu)
	cl.send(webEvent{Op: "ping"})
	time.Sleep(sc.statusEvery * 3 / 2) // past the next tick
	let()
	var got []float64
	for range 2 {
		got = append(got, statusNum(statusUntil(t, cl, "a status", func(map[string]any) bool { return true }), "kbps"))
	}
	zeros, others := 0, 0
	for _, kbps := range got {
		switch kbps {
		case 0:
			zeros++
		case before:
		default:
			others++
		}
	}
	if zeros == 0 || others > 0 {
		t.Errorf("after the held ping, statuses with %v kbps, want one with 0 and the other with 0 or %v", got, before)
	}
}

// TestWebStatusPingRepeatsTheRate: a status that answers a ping repeats the
// rate the last tick stored, and neither ends that tick's window nor brings a
// new figure. The viewer's sender is held on a frame (cfgMu), so no tick can
// store a rate while its reader answers: the answer must say exactly the
// stored figure. The ping comes late in the window, so the next tick's rate,
// over the whole window, gives what the viewer got since the status before
// (the answer and the held frame) in about the time between the two; had the
// answer ended the window, that rate would cover the held frame alone, in a
// fifth of the time.
func TestWebStatusPingRepeatsTheRate(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cl := rawViewer(t, h.url())
	sc := onlyClient(t, h.srv)
	cv := computeWebCanvas(64, 64, 64, 64)
	frame := func() *videoFrame {
		return &videoFrame{rgb: make([]byte, cv.cw*cv.ch*4), w: 64, h: 64, cw: cv.cw, ch: cv.ch, fps: 60}
	}
	// read reads up to a status: it, its size, the bytes before it, and when it came.
	read := func() (st map[string]any, size, before int, at time.Time) {
		t.Helper()
		if err := cl.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		for {
			op, payload, err := cl.ws.ReadMessage()
			if err != nil {
				t.Fatalf("no status within 5s: %v", err)
			}
			if op == wsOpText && json.Unmarshal(payload, &st) == nil && st["op"] == "status" {
				return st, len(payload), before, time.Now()
			}
			before += len(payload)
		}
	}
	var stored float64
	var tN time.Time
	var let func()
	for try := 1; ; try++ {
		h.srv.frame(frame())
		statusUntil(t, cl, "a rate above 0", func(st map[string]any) bool { return statusNum(st, "kbps") > 0 })
		tN = time.Now()
		let = stall(t, &sc.cfgMu)
		h.srv.frame(frame())
		eventually(t, "the sender takes the frame", func() bool { return mailbox(sc) == nil })
		if stored = math.Float64frombits(sc.kbpsBits.Load()); stored > 0 {
			break
		}
		let() // a tick came before the sender was held: again
		if try == 3 {
			t.Fatal("3 tries, and a tick came before the sender was held each time")
		}
	}
	time.Sleep(time.Until(tN.Add(sc.statusEvery * 4 / 5)))
	cl.send(webEvent{Op: "ping"})
	st, answer, other, _ := read()
	answered := statusNum(st, "kbps")
	if answered != stored {
		t.Errorf("the ping's answer says %v kbps; the last tick stored %v", answered, stored)
	}
	let()
	st, _, video, at := read()
	got, took, kbps := answer+other+video, at.Sub(tN), statusNum(st, "kbps")
	if kbps <= 0 {
		t.Fatalf("the tick after the ping says %v kbps after the viewer got %d bytes", kbps, got)
	}
	implied := time.Duration(float64(got) * 8 / kbps * float64(time.Millisecond))
	t.Logf("answer %v kbps (stored %v); then %d bytes (answer %d, video %d) in %v at %.1f kbps, which takes %v",
		answered, stored, got, answer, video, took.Round(time.Millisecond), kbps, implied.Round(time.Millisecond))
	if implied < took/2 || implied > took*2 {
		t.Errorf("the tick after the ping says %.1f kbps after the viewer got %d bytes in %v, which that rate takes %v to send",
			kbps, got, took, implied)
	}
}

// TestKbpsOver: the status's rate is in kilobits, of 1000 bits, per second,
// fractions kept.
func TestKbpsOver(t *testing.T) {
	for _, c := range []struct {
		n    int64
		d    time.Duration
		want float64
	}{
		{1000, time.Second, 8},
		{62500, 500 * time.Millisecond, 1000},
		{100, time.Second, 0.8},
		{0, 500 * time.Millisecond, 0},
		{1000, 0, 0},
	} {
		if got := kbpsOver(c.n, c.d); got != c.want {
			t.Errorf("kbpsOver(%d, %v) = %v, want %v", c.n, c.d, got, c.want)
		}
	}
}

// TestWSCountsTheBytesItWrites: a socket counts the bytes of each frame it
// writes, its header included (2, 4 or 10 bytes for an unmasked frame, by
// payload length: RFC 6455, section 5.2), and nothing for a frame it fails to
// write.
func TestWSCountsTheBytesItWrites(t *testing.T) {
	client, server := wsTestPair(t)
	var want int64
	for _, c := range []struct{ n, header int }{{0, 2}, {125, 2}, {126, 4}, {65535, 4}, {65536, 10}} {
		if err := server.WriteBinary(make([]byte, c.n)); err != nil {
			t.Fatalf("write %d bytes: %v", c.n, err)
		}
		readServerFrame(t, client)
		want += int64(c.header + c.n)
		if got := server.wrote.Load(); got != want {
			t.Fatalf("after a %d-byte frame the socket counts %d bytes written, want %d", c.n, got, want)
		}
	}
	server.conn.Close()
	if err := server.WriteText(`{"op":"status"}`); err == nil {
		t.Fatal("a write to a closed socket succeeded")
	}
	if got := server.wrote.Load(); got != want {
		t.Errorf("a failed write took the count to %d, want %d", got, want)
	}
}
