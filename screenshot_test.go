package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Alt+S in the terminal saves the device's picture at the stream's resolution
// as a PNG. The video goroutine, which owns the decoder, scales the frame for
// the request, and the PNG is written on a goroutine of its own. These tests
// run the real decoder on a real H.264 stream.
// ---------------------------------------------------------------------------

// shotFixture is three 1280x2800 frames, 100 ms apart: dark blue (#204060)
// with a red, a green and a blue quarter, whose bottom right quarter turns
// white from the second frame on. FFmpeg 9.0.2 with x264 0.165.r3222 made it,
// and makes the same bytes again, with:
//
//	ffmpeg -f lavfi -i "color=c=0x204060:s=1280x2800:r=10:d=0.3,drawbox=x=0:y=0:w=iw/2:h=ih/2:c=red:t=fill,drawbox=x=iw/2:y=0:w=iw/2:h=ih/2:c=lime:t=fill,drawbox=x=0:y=ih/2:w=iw/2:h=ih/2:c=blue:t=fill,drawbox=x=iw/2:y=ih/2:w=iw/2:h=ih/2:c=white:t=fill:enable=gte(t\,0.1)" \
//	  -c:v libx264 -profile:v high -preset veryslow -tune zerolatency -threads 1 -bf 0 -g 100 \
//	  -pix_fmt yuv420p -bitexact -bsf:v filter_units=remove_types=6 -f h264 testdata/shot-1280x2800.h264
const shotFixture = "testdata/shot-1280x2800.h264"

// Read before any test changes directory.
var shotFixtureData, shotFixtureErr = os.ReadFile(shotFixture)

// shotTime names the screenshots of these tests: scterm-20261021-045500.png.
var shotTime = time.Date(2026, 10, 21, 4, 55, 0, 0, time.Local)

const shotName = "scterm-20261021-045500.png"

// shotRig is a terminal UI app whose video goroutine runs on the client end
// of a pipe; device is the server's end. It has decoded the first frame.
type shotRig struct {
	t       *testing.T
	a       *app
	device  net.Conn
	session []byte   // the session packet it was started with
	frames  [][]byte // the fixture's frames, as media packets
	ended   chan error
}

// noDeadline is a video socket whose read deadline does nothing, so a
// screenshot request cannot end a read early and waits for the next frame.
type noDeadline struct{ net.Conn }

func (noDeadline) SetReadDeadline(time.Time) error { return nil }

// newShotRig starts runVideo on a terminal of cols x rows (the canvas is cols
// x 2*(rows-1)), sends the stream's head with a session packet of w x h, the
// config packet and the first frame, and waits until it is decoded.
func newShotRig(t *testing.T, cols, rows, w, h int, deadlines bool) *shotRig {
	t.Helper()
	video := func(c net.Conn) net.Conn { return c }
	if !deadlines {
		video = func(c net.Conn) net.Conn { return noDeadline{c} }
	}
	return newShotRigOn(t, cols, rows, w, h, video)
}

// newShotRigOn is newShotRig with the video socket that video makes of the
// client end of the pipe.
func newShotRigOn(t *testing.T, cols, rows, w, h int, video func(net.Conn) net.Conn) *shotRig {
	t.Helper()
	withTermSize(t, cols, rows)
	config, frames := fixturePackets(t)
	client, device := net.Pipe()
	a := streamApp(&session{deviceName: "Pixel 8", video: video(client)})
	a.tui = &tui{cols: cols, rows: rows - 1, running: true}
	a.frames = make(chan *videoFrame, 1)
	a.stream.setSink(a)
	r := &shotRig{t: t, a: a, device: device, frames: frames, ended: make(chan error, 1)}
	go func() { r.ended <- a.stream.runVideo() }()
	t.Cleanup(func() {
		device.Close()
		client.Close()
		select {
		case <-r.ended:
		case <-time.After(5 * time.Second):
			t.Error("runVideo did not end after the device hung up")
		}
	})
	r.session = []byte{0x80, 0, 0, 0}
	r.session = binary.BigEndian.AppendUint32(r.session, uint32(w))
	r.session = binary.BigEndian.AppendUint32(r.session, uint32(h))
	if err := r.send([]byte("h264"), r.session, config, frames[0]); err != nil {
		t.Fatal(err)
	}
	r.waitFrames(1)
	return r
}

// settle sends the session packet again, which changes nothing. runVideo
// takes it off the pipe only when it reads again, so once settle returns,
// runVideo is done with what was sent before, and has answered the
// screenshots that it could.
func (r *shotRig) settle() error { return r.send(r.session) }

// send writes packets as the device does, each read in full by runVideo.
func (r *shotRig) send(packets ...[]byte) error {
	for _, p := range packets {
		r.device.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := r.device.Write(p); err != nil {
			return err
		}
	}
	return nil
}

// waitFrames waits until runVideo has decoded and scaled n frames.
func (r *shotRig) waitFrames(n int64) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.a.stream.frameCount.Load() < n {
		if time.Now().After(deadline) {
			r.t.Fatalf("runVideo decoded %d frames, want %d", r.a.stream.frameCount.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// fixturePackets cuts the fixture into the packets the server would send:
// the SPS and PPS as one config packet, then one media packet per frame, with
// the 12-byte header of docs/ARCHITECTURE.md (bit 62 config, bit 61 keyframe,
// then the timestamp in microseconds, and the payload size).
func fixturePackets(t *testing.T) (config []byte, frames [][]byte) {
	t.Helper()
	data := shotFixtureData
	if shotFixtureErr != nil {
		t.Fatal(shotFixtureErr)
	}
	var starts []int // where each start code begins, 3 or 4 bytes
	for i := 0; i+3 <= len(data); i++ {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			s := i
			if s > 0 && data[s-1] == 0 {
				s--
			}
			starts = append(starts, s)
			i += 2
		}
	}
	var params []byte
	for k, s := range starts {
		e := len(data)
		if k+1 < len(starts) {
			e = starts[k+1]
		}
		nal := data[s:e]
		switch nal[bytes.Index(nal, []byte{0, 0, 1})+3] & 0x1f {
		case 7, 8: // SPS, PPS
			params = append(params, nal...)
		case 5: // IDR slice
			frames = append(frames, wirePacket(1<<61, int64(len(frames))*100000, nal))
		case 1: // other slice
			frames = append(frames, wirePacket(0, int64(len(frames))*100000, nal))
		}
	}
	if len(params) == 0 || len(frames) != 3 {
		t.Fatalf("%s: %d bytes of parameter sets and %d frames, want some and 3", shotFixture, len(params), len(frames))
	}
	return wirePacket(1<<62, 0, params), frames
}

func wirePacket(flags uint64, ptsUs int64, payload []byte) []byte {
	b := binary.BigEndian.AppendUint64(nil, flags|uint64(ptsUs))
	b = binary.BigEndian.AppendUint32(b, uint32(len(payload)))
	return append(b, payload...)
}

// withShotWait sets shotWait for one test.
func withShotWait(t *testing.T, d time.Duration) {
	old := shotWait
	shotWait = d
	t.Cleanup(func() { shotWait = old })
}

// waitShot waits for a screenshot to be saved or dropped.
func waitShot(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the screenshot was neither saved nor dropped within 5 s")
	}
}

// stderrOf runs fn with os.Stderr on a pipe and returns what was written to
// it. Unlike captureStderr, it puts os.Stderr back when fn fails the test.
func stderrOf(t *testing.T, fn func()) (out string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); r.Close(); got <- string(b) }()
	old := setStderr(w)
	defer func() {
		setStderr(old)
		w.Close()
		out = <-got
	}()
	fn()
	return
}

// readShot decodes the PNG at name.
func readShot(t *testing.T, name string) image.Image {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return img
}

// shotFiles lists the screenshots in the current directory, in either format.
func shotFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("scterm-*")
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// checkQuarters checks the colour in the middle of each quarter of a
// 1280x2800 picture of the fixture. H.264 and the 4:2:0 round trip shift a
// colour by a few levels; a red and blue swap moves it by more than 200.
func checkQuarters(t *testing.T, img image.Image, bottomRight [3]int) {
	t.Helper()
	if b := img.Bounds(); b.Dx() != 1280 || b.Dy() != 2800 {
		t.Fatalf("the screenshot is %dx%d, want the stream's 1280x2800", b.Dx(), b.Dy())
	}
	for _, q := range []struct {
		name string
		x, y int
		want [3]int
	}{
		{"top left", 320, 700, [3]int{255, 0, 0}},
		{"top right", 960, 700, [3]int{0, 255, 0}},
		{"bottom left", 320, 2100, [3]int{0, 0, 255}},
		{"bottom right", 960, 2100, bottomRight},
	} {
		r, g, b, _ := img.At(q.x, q.y).RGBA()
		got := [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
		for i := range got {
			if d := got[i] - q.want[i]; d < -12 || d > 12 {
				t.Errorf("%s quarter is %v, want about %v", q.name, got, q.want)
				break
			}
		}
	}
}

var (
	darkBlue = [3]int{0x20, 0x40, 0x60}
	white    = [3]int{255, 255, 255}
)

// TestShotOfAStillScreen: a still screen sends no frames, yet Alt+S saves the
// frame the decoder holds, at once and at the stream's resolution: the
// request ends the read that waits for the device.
func TestShotOfAStillScreen(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour) // only the video goroutine can answer
	r := newShotRig(t, 80, 24, 1280, 2800, true)
	out := stderrOf(t, func() { waitShot(t, r.a.takeShot(shotTime)) })
	if want := "scterm: saved " + shotName + " (1280x2800)\n"; out != want {
		t.Errorf("notice %q, want %q", out, want)
	}
	checkQuarters(t, readShot(t, shotName), darkBlue) // the first frame
	if files := shotFiles(t); len(files) != 1 {
		t.Errorf("files %v, want only %s", files, shotName)
	}
	// The video goroutine reads on after it answered.
	if err := r.send(r.frames[1]); err != nil {
		t.Fatal(err)
	}
	r.waitFrames(2)
}

// TestShotWaitsForTheNextFrame: where a read cannot be ended early, the next
// frame answers, at the stream's resolution. The PNG is encoded and written
// off the video goroutine: while the saving goroutine waits for the UI lock,
// the decoder reads the frame after, and no file is there yet.
func TestShotWaitsForTheNextFrame(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour)
	r := newShotRig(t, 80, 24, 1280, 2800, false)
	out := stderrOf(t, func() {
		done := r.a.takeShot(shotTime)
		func() {
			r.a.tui.mu.Lock()
			defer r.a.tui.mu.Unlock()
			if err := r.send(r.frames[1]); err != nil {
				t.Errorf("sending the second frame: %v", err)
				return
			}
			if err := r.send(r.frames[2]); err != nil {
				t.Errorf("the video goroutine stopped reading while the screenshot waited to be saved: %v", err)
				return
			}
			if files := shotFiles(t); len(files) != 0 {
				t.Errorf("the screenshot was written on the video goroutine: %v", files)
			}
		}()
		waitShot(t, done)
	})
	if want := "scterm: saved " + shotName + " (1280x2800)\n"; out != want {
		t.Errorf("notice %q, want %q", out, want)
	}
	checkQuarters(t, readShot(t, shotName), white) // the second frame
}

// TestShotFallsBackToTheCanvas: when no frame comes within shotWait, the
// terminal's picture is saved instead: the canvas the run loop drew, without
// its letterbox bars, colour for colour, and the notice says so. The 40x100
// canvas fits the 1280x2800 frame at 1/32: 40x86 (87.5, made even), 7 rows
// below the top.
func TestShotFallsBackToTheCanvas(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, 50*time.Millisecond)
	r := newShotRig(t, 40, 51, 1280, 2800, false)
	var f *videoFrame
	select {
	case f = <-r.a.frames:
	case <-time.After(5 * time.Second):
		t.Fatal("runVideo delivered no frame")
	}
	// What the run loop records when it draws the frame (loop, tui.draw).
	r.a.frameW, r.a.frameH = f.w, f.h
	r.a.tui.lastRGB = slices.Clone(f.rgb)
	r.a.tui.lastW, r.a.tui.lastH = f.cw, f.ch
	if f.cw != 40 || f.ch != 100 {
		t.Fatalf("test setup: the canvas is %dx%d, want 40x100", f.cw, f.ch)
	}

	out := stderrOf(t, func() { waitShot(t, r.a.takeShot(shotTime)) })
	if want := "scterm: saved " + shotName + " (40x86) at the terminal's size, because no frame came from the device in time\n"; out != want {
		t.Errorf("notice %q, want %q", out, want)
	}
	img := readShot(t, shotName)
	if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 86 {
		t.Fatalf("the screenshot is %dx%d, want the 40x86 picture", b.Dx(), b.Dy())
	}
	for y := 0; y < 86; y++ {
		for x := 0; x < 40; x++ {
			p := f.rgb[((y+7)*40+x)*4:] // B, G, R, unused
			r, g, b, _ := img.At(x, y).RGBA()
			if byte(r>>8) != p[2] || byte(g>>8) != p[1] || byte(b>>8) != p[0] {
				t.Fatalf("pixel (%d,%d) is %d,%d,%d, want the canvas's %d,%d,%d",
					x, y, r>>8, g>>8, b>>8, p[2], p[1], p[0])
			}
		}
	}
	for _, row := range []int{0, 85} { // no bar row came along
		if r, g, b, _ := img.At(20, row).RGBA(); r == 0 && g == 0 && b == 0 {
			t.Errorf("row %d of the screenshot is a black bar", row)
		}
	}
}

// TestShotWithTheVideoOff: with --video=false there is no frame to save, and
// the notice says why rather than that a frame is yet to come.
func TestShotWithTheVideoOff(t *testing.T) {
	t.Chdir(t.TempDir())
	a := streamApp(&session{deviceName: "Pixel 8"})
	a.tui = &tui{cols: 80, rows: 23, running: true}
	if err := a.stream.runVideo(); err != nil {
		t.Fatal(err)
	}
	out := stderrOf(t, func() { waitShot(t, a.takeShot(shotTime)) })
	if want := "scterm: screenshot: the video is off\n"; out != want {
		t.Errorf("notice %q, want %q", out, want)
	}
	if files := shotFiles(t); len(files) != 0 {
		t.Errorf("wrote %v with the video off", files)
	}
}

// TestShotDroppedWhenTheSessionEnds: a screenshot still waiting when the
// video stream ends is dropped, and nothing is left waiting: the saving
// goroutine ends, and so does the video goroutine.
func TestShotDroppedWhenTheSessionEnds(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour) // only the end of the stream can end the wait
	r := newShotRig(t, 80, 24, 1280, 2800, false)
	before := runtime.NumGoroutine()
	var done <-chan struct{}
	out := stderrOf(t, func() {
		done = r.a.takeShot(shotTime)
		r.device.Close() // the device hangs up
		waitShot(t, done)
	})
	if out != "" {
		t.Errorf("notice %q, want none", out)
	}
	select {
	case err := <-r.ended:
		r.ended <- err // for the cleanup
	case <-time.After(5 * time.Second):
		t.Fatal("runVideo did not end")
	}
	// The saving goroutine and the video goroutine are gone.
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before-1 {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines, want at most %d", runtime.NumGoroutine(), before-1)
		}
		time.Sleep(time.Millisecond)
	}
	if files := shotFiles(t); len(files) != 0 {
		t.Errorf("wrote %v after the session ended", files)
	}
}

// TestShotDroppedAfterTheTerminalIsGivenBack: a frame that comes after the
// terminal UI ended (a quit) saves nothing.
func TestShotDroppedAfterTheTerminalIsGivenBack(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour)
	r := newShotRig(t, 80, 24, 1280, 2800, false)
	out := stderrOf(t, func() {
		done := r.a.takeShot(shotTime)
		r.a.tui.mu.Lock()
		r.a.tui.running = false // as shellClose leaves it
		r.a.tui.mu.Unlock()
		if err := r.send(r.frames[1]); err != nil {
			t.Error(err)
		}
		waitShot(t, done)
	})
	if out != "" {
		t.Errorf("notice %q, want none", out)
	}
	if files := shotFiles(t); len(files) != 0 {
		t.Errorf("wrote %v after the terminal was given back", files)
	}
}

// TestShotsInTheSameSecond: two screenshots in the same second are two files,
// the second named with -2. The second is taken once the first is saved.
func TestShotsInTheSameSecond(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour)
	r := newShotRig(t, 80, 24, 1280, 2800, true)
	out := stderrOf(t, func() {
		waitShot(t, r.a.takeShot(shotTime))
		waitShot(t, r.a.takeShot(shotTime))
	})
	const other = "scterm-20261021-045500-2.png"
	if files := shotFiles(t); !slices.Equal(files, []string{other, shotName}) {
		t.Fatalf("files %v, want %s and %s", files, shotName, other)
	}
	for _, name := range []string{shotName, other} {
		checkQuarters(t, readShot(t, name), darkBlue)
		if line := "scterm: saved " + name + " (1280x2800)\n"; !strings.Contains(out, line) {
			t.Errorf("notices %q do not say %q", out, line)
		}
	}
}

// TestShotPressesJoinTheShotInFlight: one screenshot is taken at a time.
// Presses while one is in flight, from its press until its file is written
// (Alt+S held down repeats), take none of their own: they write no file and
// post no notice, and they are done when it is. A press after it is done
// takes a new one.
func TestShotPressesJoinTheShotInFlight(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour)
	r := newShotRig(t, 80, 24, 1280, 2800, false) // only the next frame answers
	var presses []<-chan struct{}
	out := stderrOf(t, func() {
		for range 3 { // the shot, and two presses while it waits for its frame
			presses = append(presses, r.a.takeShot(shotTime))
		}
		// Two more once the frame has answered it (settle), while it waits to
		// be saved: saving needs the UI lock, which the test holds. A press
		// that took a shot of its own would wait for that lock too, so they
		// are made on a goroutine of their own.
		r.a.tui.mu.Lock()
		err := r.send(r.frames[1])
		if err == nil {
			err = r.settle()
		}
		late := make(chan (<-chan struct{}), 2)
		go func() {
			for range 2 {
				late <- r.a.takeShot(shotTime)
			}
		}()
		timeout := time.After(5 * time.Second)
	waitLate:
		for range 2 {
			select {
			case d := <-late:
				presses = append(presses, d)
			case <-timeout:
				t.Error("a press while the shot was being saved waited for the UI lock: it took a shot of its own")
				break waitLate
			}
		}
		for i, d := range presses {
			select {
			case <-d:
				t.Errorf("press %d was done before the shot in flight was saved", i+1)
			default:
			}
		}
		r.a.tui.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range presses {
			waitShot(t, d)
		}
	})
	if want := "scterm: saved " + shotName + " (1280x2800)\n"; out != want {
		t.Errorf("notices %q, want only %q", out, want)
	}
	if files := shotFiles(t); !slices.Equal(files, []string{shotName}) {
		t.Fatalf("files %v, want only %s", files, shotName)
	}
	checkQuarters(t, readShot(t, shotName), white) // the second frame

	const next = "scterm-20261021-045500-2.png"
	out = stderrOf(t, func() {
		done := r.a.takeShot(shotTime)
		if err := r.send(r.frames[2]); err != nil {
			t.Error(err)
		}
		waitShot(t, done)
	})
	if want := "scterm: saved " + next + " (1280x2800)\n"; out != want {
		t.Errorf("notice %q after the shot was done, want %q", out, want)
	}
	if files := shotFiles(t); !slices.Equal(files, []string{next, shotName}) {
		t.Errorf("files %v, want %s and %s", files, shotName, next)
	}
}

// TestShotIsTheFramesOwnSize: the screenshot has the size of the frame that
// was scaled, not the size the last session packet announced (here 2800x1280,
// as a rotation would, while the frames are still 1280x2800).
func TestShotIsTheFramesOwnSize(t *testing.T) {
	t.Chdir(t.TempDir())
	withShotWait(t, time.Hour)
	r := newShotRig(t, 80, 24, 2800, 1280, true)
	out := stderrOf(t, func() { waitShot(t, r.a.takeShot(shotTime)) })
	if want := "scterm: saved " + shotName + " (1280x2800)\n"; out != want {
		t.Errorf("notice %q, want %q", out, want)
	}
	checkQuarters(t, readShot(t, shotName), darkBlue)
}
