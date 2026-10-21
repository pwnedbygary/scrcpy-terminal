package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/jpeg"
	"sync"
	"testing"
	"time"
)

// Every viewer is handed the same frame, so one frame buffer is shared until
// the last of them is done with it. The tests below hold a viewer's sender at a
// chosen point by holding a lock it needs (the encoder's, inside its encode;
// its own config lock, just before it touches the frame; or its socket's write
// lock, once it is done with the frame), or hold a viewer's frame lock to stop
// frame() before it reaches that viewer, and look at the frame pool meanwhile.

// sameBuffer reports whether a and b start at the same byte.
func sameBuffer(a, b []byte) bool {
	return cap(a) > 0 && cap(b) > 0 && &a[:1][0] == &b[:1][0]
}

// exactCopy copies b into a buffer whose capacity is its length: bytes.Clone
// may round the capacity up, and the pool hands out any buffer big enough.
func exactCopy(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// pooled counts the pool slots holding b.
func pooled(s *webServer, b []byte) int {
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	n := 0
	for _, p := range s.pool {
		if sameBuffer(p, b) {
			n++
		}
	}
	return n
}

// eventually fails the test unless cond comes true within 5 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
	}
}

// stall holds mu, which a sender needs, until the returned func lets go of it,
// or the test ends, so that a failed test still lets the server stop.
func stall(t *testing.T, mu *sync.Mutex) (let func()) {
	mu.Lock()
	var once sync.Once
	let = func() { once.Do(mu.Unlock) }
	t.Cleanup(let)
	return let
}

// rawViewer is a viewer that asked for raw BGR0 frames.
func rawViewer(t *testing.T, host string) *testClient {
	t.Helper()
	cl := dialWS(t, host)
	cl.send(webEvent{Op: "hello", W: 480, H: 1080, Format: "raw"})
	cl.nextJSON("hello", 3*time.Second)
	return cl
}

// TestWebSharedFrameGoesBackOnce: three viewers are handed the same frame, and
// its buffer goes back to the pool once they are all done with it. Before,
// each of them returned it, so one buffer sat in several pool slots, to be
// handed to several senders at once. In the first two cases the viewers are
// raw because a raw sender's output buffer is bigger than a frame buffer: no
// sender can take the frame's buffer back out, so the count is exact. The
// third case's viewers are JPEG ones; its comments say why its count is exact.
func TestWebSharedFrameGoesBackOnce(t *testing.T) {
	cv := computeWebCanvas(480, 1080, 480, 1080)
	threeViewers := func(t *testing.T) (*webTestHarness, []*testClient) {
		h := newWebTestHarness(t, config{web: true}, false)
		return h, []*testClient{rawViewer(t, h.url()), rawViewer(t, h.url()), rawViewer(t, h.url())}
	}
	t.Run("a frame they send", func(t *testing.T) {
		h, viewers := threeViewers(t)
		want := makeCanvas(cv.cw, cv.ch, 5)
		f := &videoFrame{rgb: exactCopy(want), w: 480, h: 1080, cw: cv.cw, ch: cv.ch, fps: 60}
		h.srv.frame(f)
		for i, cl := range viewers {
			if body := cl.nextBinary(webMsgVideo, 5*time.Second)[webHeaderLen:]; !bytes.Equal(body, want) {
				t.Fatalf("viewer %d was sent pixels that are not the frame's", i)
			}
		}
		if n := pooled(h.srv, f.rgb); n != 1 {
			t.Fatalf("the frame's buffer is in %d pool slots once its three viewers have sent it, want 1", n)
		}
	})
	t.Run("a frame too short for its canvas", func(t *testing.T) {
		h, viewers := threeViewers(t)
		waitClients(t, h.srv, len(viewers))
		h.srv.clientsMu.Lock()
		clients := h.srv.clients
		h.srv.clientsMu.Unlock()
		short := &videoFrame{rgb: make([]byte, cv.cw*cv.ch*4-4), w: 480, h: 1080, cw: cv.cw, ch: cv.ch, fps: 60}
		h.srv.frame(short)
		eventually(t, "every sender has refused the short frame", func() bool {
			for _, c := range clients {
				if c.encErrors.Load() != 1 {
					return false
				}
			}
			return true
		})
		// Each sender takes its frames in order: once it has sent the next
		// one, it is done with the short one.
		h.srv.frame(&videoFrame{rgb: makeCanvas(cv.cw, cv.ch, 5), w: 480, h: 1080, cw: cv.cw, ch: cv.ch, fps: 60})
		for _, cl := range viewers {
			cl.nextBinary(webMsgVideo, 5*time.Second)
		}
		if n := pooled(h.srv, short.rgb); n != 1 {
			t.Fatalf("the short frame's buffer is in %d pool slots once its three viewers have refused it, want 1", n)
		}
	})
	t.Run("a frame no encoder can be opened for", func(t *testing.T) {
		// libavcodec's JPEG encoder takes at most 65500 pixels a side, so no
		// encoder opens for this frame, and each of three JPEG senders drops
		// it unsent.
		h := newWebTestHarness(t, config{web: true}, false)
		viewers := []*testClient{helloClient(t, h.url()), helloClient(t, h.url()), helloClient(t, h.url())}
		waitClients(t, h.srv, len(viewers))
		wide := computeWebCanvas(70000, 2, 70000, 2)
		if size := h.srv.encoderMaxSize(wide); size > 0 {
			t.Fatalf("an encoder opened for %dx%d", wide.cw, wide.ch)
		}
		h.srv.clientsMu.Lock()
		clients := h.srv.clients
		h.srv.clientsMu.Unlock()
		f := &videoFrame{rgb: make([]byte, wide.cw*wide.ch*4), w: wide.cw, h: wide.ch, cw: wide.cw, ch: wide.ch, fps: 60}
		h.srv.frame(f)
		eventually(t, "every sender has taken the frame", func() bool {
			for _, c := range clients {
				if mailbox(c) != nil {
					return false
				}
			}
			return true
		})
		// Once a sender has sent the next frame, it is done with this one,
		// whose buffer is too small to be that frame's JPEG output.
		h.srv.frame(&videoFrame{rgb: makeCanvas(cv.cw, cv.ch, 5), w: 480, h: 1080, cw: cv.cw, ch: cv.ch, fps: 60})
		for _, cl := range viewers {
			cl.nextBinary(webMsgVideo, 5*time.Second)
		}
		if n := pooled(h.srv, f.rgb); n != 1 {
			t.Fatalf("the unencodable frame's buffer is in %d pool slots once its three viewers have dropped it, want 1", n)
		}
	})
}

// shareRig is a JPEG viewer and then a raw viewer at the default canvas, the
// raw viewer's server side, and the encoder the JPEG sender encodes with.
type shareRig struct {
	h         *webTestHarness
	cv        webCanvas
	jpeg, raw *testClient
	rc        *webClient
	enc       *jpegEncoder
	outSize   int // a JPEG sender's output buffer: header and encoder bound
}

func newShareRig(t *testing.T) *shareRig {
	t.Helper()
	h := newWebTestHarness(t, config{web: true}, false)
	r := &shareRig{h: h, cv: computeWebCanvas(480, 1080, 480, 1080)}
	r.jpeg = helloClient(t, h.url())
	r.raw = rawViewer(t, h.url())
	waitClients(t, h.srv, 2)
	h.srv.clientsMu.Lock()
	r.rc = h.srv.clients[1]
	h.srv.clientsMu.Unlock()
	size := h.srv.encoderMaxSize(r.cv)
	if size <= 0 {
		t.Fatalf("no JPEG encoder for %dx%d", r.cv.cw, r.cv.ch)
	}
	r.outSize = webHeaderLen + size
	key := uint64(uint32(r.cv.cw))<<32 | uint64(uint32(r.cv.ch))
	h.srv.encMu.Lock()
	r.enc = h.srv.enc[key]
	h.srv.encMu.Unlock()
	return r
}

func (r *shareRig) newFrame(seed byte) *videoFrame {
	return &videoFrame{rgb: makeCanvas(r.cv.cw, r.cv.ch, seed), w: 480, h: 1080, cw: r.cv.cw, ch: r.cv.ch, fps: 60}
}

// seedOutput empties the pool and leaves in it one buffer that only fits a
// JPEG sender's output. Once it is gone, the JPEG sender has its output buffer
// and cannot take another from the pool for this frame.
func (r *shareRig) seedOutput() []byte {
	m := make([]byte, r.outSize)
	r.h.srv.poolMu.Lock()
	r.h.srv.pool = [4][]byte{m}
	r.h.srv.poolMu.Unlock()
	return m
}

// outputPooled reports whether a JPEG sender's output buffer is in the pool.
func (r *shareRig) outputPooled() bool {
	r.h.srv.poolMu.Lock()
	defer r.h.srv.poolMu.Unlock()
	for _, p := range r.h.srv.pool {
		if cap(p) == r.outSize {
			return true
		}
	}
	return false
}

func mailbox(c *webClient) *videoFrame {
	c.frameMu.Lock()
	defer c.frameMu.Unlock()
	return c.frame
}

// holdInRawSender sends a first frame and returns once the raw sender sits on
// it, before touching it, and the JPEG sender has sent it and is idle again.
// The raw viewer's mailbox then keeps whatever frame comes next.
func (r *shareRig) holdInRawSender(t *testing.T) (let func()) {
	t.Helper()
	let = stall(t, &r.rc.cfgMu)
	r.h.srv.frame(r.newFrame(1))
	r.jpeg.nextBinary(webMsgVideo, 5*time.Second)
	eventually(t, "the raw sender holds the first frame", func() bool { return mailbox(r.rc) == nil })
	eventually(t, "the JPEG sender's output buffer is back in the pool", r.outputPooled)
	return let
}

// holdInEncode sends f and returns once the JPEG sender holds it inside its
// encode, its output buffer already taken.
func (r *shareRig) holdInEncode(t *testing.T, f *videoFrame) (let func()) {
	t.Helper()
	let = stall(t, &r.enc.mu)
	marker := r.seedOutput()
	r.h.srv.frame(f)
	eventually(t, "the JPEG sender has its output buffer", func() bool { return pooled(r.h.srv, marker) == 0 })
	return let
}

// checkHeld fails the test if f's buffer can be had from the pool while the
// JPEG sender still encodes from it.
func (r *shareRig) checkHeld(t *testing.T, f *videoFrame) {
	t.Helper()
	if n := pooled(r.h.srv, f.rgb); n > 0 {
		t.Errorf("the frame's buffer is in %d pool slots while the JPEG sender still encodes from it", n)
	}
	if b := r.h.srv.poolGet(len(f.rgb)); sameBuffer(b, f.rgb) {
		t.Error("the pool handed out the frame's buffer while the JPEG sender still encodes from it")
	}
}

// checkJPEG fails the test unless the JPEG viewer's next video is a whole
// JPEG of the canvas.
func (r *shareRig) checkJPEG(t *testing.T) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(r.jpeg.nextBinary(webMsgVideo, 5*time.Second)[webHeaderLen:]))
	if err != nil {
		t.Fatalf("the JPEG viewer's frame: %v", err)
	}
	if b := img.Bounds(); b.Dx() != r.cv.cw || b.Dy() != r.cv.ch {
		t.Fatalf("the JPEG viewer's frame is %dx%d, want %dx%d", b.Dx(), b.Dy(), r.cv.cw, r.cv.ch)
	}
}

// TestWebHeldFrameStaysOutOfThePool: a frame's buffer stays out of the pool,
// where any sender can be handed it for its output, for as long as anyone
// still holds the frame. Here the JPEG viewer's sender holds it inside its
// encode while the frame's other holder lets go of it in each of the ways a
// holder can. Before, every holder returned the buffer when it let go, so the
// next sender to ask the pool for an output buffer could write over pixels the
// encode was still reading.
func TestWebHeldFrameStaysOutOfThePool(t *testing.T) {
	t.Run("the raw sender is done with it", func(t *testing.T) {
		r := newShareRig(t)
		letRaw := stall(t, &r.rc.cfgMu)
		f := r.newFrame(2)
		letEnc := r.holdInEncode(t, f)
		letRaw()
		r.raw.nextBinary(webMsgVideo, 5*time.Second)
		r.checkHeld(t, f)
		letEnc()
		r.checkJPEG(t)
		if n := pooled(r.h.srv, f.rgb); n != 1 {
			t.Errorf("the frame's buffer is in %d pool slots once both viewers have sent it, want 1", n)
		}
	})
	t.Run("a newer frame replaces it in the raw mailbox", func(t *testing.T) {
		r := newShareRig(t)
		letRaw := r.holdInRawSender(t)
		f := r.newFrame(2)
		letEnc := r.holdInEncode(t, f)
		if mailbox(r.rc) != f {
			t.Fatal("the frame is not waiting in the raw viewer's mailbox")
		}
		r.h.srv.frame(r.newFrame(3))
		r.checkHeld(t, f)
		letEnc()
		letRaw()
	})
	t.Run("the raw viewer closes with it in its mailbox", func(t *testing.T) {
		r := newShareRig(t)
		letRaw := r.holdInRawSender(t)
		f := r.newFrame(2)
		letEnc := r.holdInEncode(t, f)
		if mailbox(r.rc) != f {
			t.Fatal("the frame is not waiting in the raw viewer's mailbox")
		}
		r.rc.close()
		r.checkHeld(t, f)
		letEnc()
		letRaw()
	})
	t.Run("the server is still handing it out", func(t *testing.T) {
		// The JPEG viewer is first on the list: it gets the frame, sends it
		// and lets go while the server waits to hand it to the raw viewer.
		r := newShareRig(t)
		letOffer := stall(t, &r.rc.frameMu)
		want := makeCanvas(r.cv.cw, r.cv.ch, 2)
		f := &videoFrame{rgb: bytes.Clone(want), w: 480, h: 1080, cw: r.cv.cw, ch: r.cv.ch, fps: 60}
		handed := make(chan struct{})
		go func() { r.h.srv.frame(f); close(handed) }()
		r.checkJPEG(t)
		if n := pooled(r.h.srv, f.rgb); n > 0 {
			t.Errorf("the frame's buffer is in %d pool slots while the server is still handing the frame out", n)
		}
		if b := r.h.srv.poolGet(len(f.rgb)); sameBuffer(b, f.rgb) {
			t.Error("the pool handed out the frame's buffer while the server is still handing the frame out")
		}
		letOffer()
		select {
		case <-handed:
		case <-time.After(5 * time.Second):
			t.Fatal("the server never finished handing the frame out")
		}
		if body := r.raw.nextBinary(webMsgVideo, 5*time.Second)[webHeaderLen:]; !bytes.Equal(body, want) {
			t.Error("the raw viewer was sent pixels that are not the frame's")
		}
		if n := pooled(r.h.srv, f.rgb); n != 1 {
			t.Errorf("the frame's buffer is in %d pool slots once both viewers have sent it, want 1", n)
		}
	})
}

// TestWebRawSenderCopiesBeforeLettingGo: the raw sender is the frame's last
// holder, so once it lets go the buffer is in the pool, and the next sender
// that takes it writes its header there. The test holds the raw sender's
// socket write, takes the buffer from the pool and writes to it, as such a
// sender would. A raw sender that let go before copying the frame races with
// that write, and the test relies on -race to report it: without -race the
// copy has come first in every run tried.
func TestWebRawSenderCopiesBeforeLettingGo(t *testing.T) {
	r := newShareRig(t)
	letRaw := stall(t, &r.rc.cfgMu)
	want := makeCanvas(r.cv.cw, r.cv.ch, 2)
	f := &videoFrame{rgb: exactCopy(want), w: 480, h: 1080, cw: r.cv.cw, ch: r.cv.ch, fps: 60}
	r.h.srv.frame(f)
	r.checkJPEG(t)
	letWrite := stall(t, &r.rc.ws.wmu)
	letRaw()
	eventually(t, "the frame's buffer is back in the pool", func() bool { return pooled(r.h.srv, f.rgb) == 1 })
	b := r.h.srv.poolGet(len(f.rgb))
	if !sameBuffer(b, f.rgb) {
		t.Fatal("the pool handed out another buffer than the frame's")
	}
	b[0] ^= 0xff
	letWrite()
	if body := r.raw.nextBinary(webMsgVideo, 5*time.Second)[webHeaderLen:]; !bytes.Equal(body, want) {
		t.Error("the raw viewer was sent a byte written after its sender let go of the frame")
	}
}

// TestWebFrameAllocatesNothing: handing a frame to every viewer allocates
// nothing and waits for no sender, here with each sender sitting on an earlier
// frame, so that every new frame replaces one in each mailbox and lets it go.
func TestWebFrameAllocatesNothing(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	for range 3 {
		rawViewer(t, h.url())
	}
	waitClients(t, h.srv, 3)
	h.srv.clientsMu.Lock()
	clients := h.srv.clients
	h.srv.clientsMu.Unlock()
	for _, c := range clients {
		stall(t, &c.cfgMu)
	}
	cv := computeWebCanvas(16, 16, 16, 16)
	frames := make([]*videoFrame, 102)
	for i := range frames {
		frames[i] = &videoFrame{rgb: make([]byte, cv.cw*cv.ch*4), w: 16, h: 16, cw: cv.cw, ch: cv.ch, fps: 60}
	}
	h.srv.frame(frames[0])
	eventually(t, "every sender sits on the first frame", func() bool {
		for _, c := range clients {
			if mailbox(c) != nil {
				return false
			}
		}
		return true
	})
	next := 1
	if n := testing.AllocsPerRun(100, func() { h.srv.frame(frames[next]); next++ }); n != 0 {
		t.Fatalf("handing out a frame allocated %v times", n)
	}
}

// TestWebSharedFramesUnderLoad: four viewers at the default canvas, three JPEG
// and one raw, are fed frames faster than they send them, so frames are shared,
// replaced in mailboxes and recycled all at once. Each viewer must get whole
// frames of its own, in order. Run it with -race: before, one buffer could
// reach two senders at once, or one sender as its output while another still
// read it as its frame, which the race detector reports in sendFrame. Without
// -race, the frames themselves show the damage it did.
func TestWebSharedFramesUnderLoad(t *testing.T) {
	h := newWebTestHarness(t, config{web: true}, false)
	cv := computeWebCanvas(480, 1080, 480, 1080)

	// Every frame has these pixels, so every JPEG must be this one: the
	// encoder gives the same bytes for the same pixels.
	pixels := makeCanvas(cv.cw, cv.ch, 9)
	out := make([]byte, h.srv.encoderMaxSize(cv))
	n, err := h.srv.encodeWith(cv, bytes.Clone(pixels), out, 0)
	if err != nil {
		t.Fatal(err)
	}
	picture := out[:n]
	if img, err := jpeg.Decode(bytes.NewReader(picture)); err != nil || img.Bounds().Dx() != cv.cw || img.Bounds().Dy() != cv.ch {
		t.Fatalf("the reference JPEG does not decode to the canvas: %v", err)
	}

	formats := []string{"auto", "auto", "auto", "raw"}
	var viewers []*testClient
	for _, format := range formats {
		cl := dialWS(t, h.url())
		cl.send(webEvent{Op: "hello", W: 480, H: 1080, Format: format})
		cl.nextJSON("hello", 3*time.Second)
		viewers = append(viewers, cl)
	}
	waitClients(t, h.srv, len(viewers))

	const frames = 200
	errs := make(chan error, len(viewers))
	for i, cl := range viewers {
		want := picture
		if formats[i] == "raw" {
			want = pixels
		}
		go func() {
			if err := readFrames(cl, cv, formats[i] == "raw", want, frames-1); err != nil {
				errs <- fmt.Errorf("viewer %d (%s): %v", i, formats[i], err)
				return
			}
			errs <- nil
		}()
	}
	for i := range frames {
		// The frame's number rides in the header, as its fps (x10 on the wire).
		h.srv.frame(&videoFrame{rgb: bytes.Clone(pixels), w: 480, h: 1080, cw: cv.cw, ch: cv.ch, fps: (float64(i) + 0.5) / 10})
		time.Sleep(time.Millisecond)
	}
	for range viewers {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// readFrames reads a viewer's video up to frame last and checks that each
// message is a whole frame of the viewer's own: its header counts the viewer's
// messages one by one and names a newer frame than the one before, and its
// payload is want, byte for byte.
func readFrames(cl *testClient, cv webCanvas, raw bool, want []byte, last int) error {
	if err := cl.conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	var seq uint32
	got := -1
	for got < last {
		op, msg, err := cl.ws.ReadMessage()
		if err != nil {
			return fmt.Errorf("after frame %d: %v", got, err)
		}
		if op != wsOpBinary || len(msg) == 0 || msg[0] != webMsgVideo {
			continue
		}
		if len(msg) < webHeaderLen {
			return fmt.Errorf("after frame %d: a %d-byte video message", got, len(msg))
		}
		seq++
		if n := binary.BigEndian.Uint32(msg[1:5]); n != seq {
			return fmt.Errorf("video message %d is numbered %d", seq, n)
		}
		n := int(binary.BigEndian.Uint16(msg[9:11]))
		if n <= got {
			return fmt.Errorf("frame %d came after frame %d", n, got)
		}
		got = n
		if w, h := int(binary.BigEndian.Uint16(msg[5:7])), int(binary.BigEndian.Uint16(msg[7:9])); w != cv.cw || h != cv.ch {
			return fmt.Errorf("frame %d: the header says %dx%d, want %dx%d", n, w, h, cv.cw, cv.ch)
		}
		format := byte(webFormatJPEG)
		if raw {
			format = webFormatRaw
		}
		if msg[11] != format {
			return fmt.Errorf("frame %d: format %d, want %d", n, msg[11], format)
		}
		if !bytes.Equal(msg[webHeaderLen:], want) {
			return fmt.Errorf("frame %d: a %d-byte payload that is not the frame's (%d bytes)", n, len(msg)-webHeaderLen, len(want))
		}
	}
	return nil
}
