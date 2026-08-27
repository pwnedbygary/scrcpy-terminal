// probe — speaks the raw scrcpy 4.1 protocol to the device with NO scrcpy
// binary: pushes the server payload, opens the adb tunnel, does the
// handshake, reads the H264 video stream, injects touch + a UHID gamepad.
//
// Usage: go run . [-s SERIAL]
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

var SCID string
var PORT = 37000
var SERVER_PAYLOAD = "/usr/share/scrcpy/scrcpy-server"
var DEV_PATH = "/data/local/tmp/scrcpy-server"

// ---- wire helpers (all big-endian) ----
type W struct {
	b bytes.Buffer
}

func (w *W) u8(v uint8)   { w.b.WriteByte(v) }
func (w *W) u16(v uint16) { var b [2]byte; binary.BigEndian.PutUint16(b[:], v); w.b.Write(b[:]) }
func (w *W) u32(v uint32) { var b [4]byte; binary.BigEndian.PutUint32(b[:], v); w.b.Write(b[:]) }
func (w *W) u64(v uint64) { var b [8]byte; binary.BigEndian.PutUint64(b[:], v); w.b.Write(b[:]) }
func (w *W) str(s string) { w.b.WriteString(s) }
func (w *W) raw(p []byte) { w.b.Write(p) }

// ---- control messages (server-side framing from control/ControlMessageReader) ----
func msgInjectTouch(action uint8, x, y int, w, h int) []byte {
	var m W
	m.u8(2) // TYPE_INJECT_TOUCH_EVENT
	m.u8(action)
	m.u64(0) // pointerId
	m.u32(uint32(x))
	m.u32(uint32(y))
	m.u16(uint16(w))
	m.u16(uint16(h))
	m.u16(0xffff) // pressure = 1.0 fixed-point
	m.u32(0)      // actionButton
	m.u32(1)      // buttons (BTN_TOUCH)
	return m.b.Bytes()
}

func msgInjectKeycode(action uint8, keycode int) []byte {
	var m W
	m.u8(0) // TYPE_INJECT_KEYCODE
	m.u8(action)
	m.u32(uint32(keycode))
	m.u32(0) // repeat
	m.u32(0) // metaState
	return m.b.Bytes()
}

func msgBackOrScreenOn() []byte { return []byte{4} }

func msgSetClipboard(text string) []byte {
	var m W
	m.u8(9) // TYPE_SET_CLIPBOARD
	var lenBytes []byte
	if len(text) < 0x100 {
		lenBytes = []byte{byte(len(text))}
	} else {
		lenBytes = []byte{byte(len(text) >> 8), byte(len(text))}
	}
	m.u8(uint8(len(lenBytes)))
	m.raw(lenBytes)
	m.str(text)
	m.u8(0) // paste=false
	return m.b.Bytes()
}

var gamepadDesc = []byte{
	0x05, 0x01, 0x09, 0x05, 0xa1, 0x01, 0xa1, 0x00, 0x05, 0x01, 0x09, 0x30,
	0x09, 0x31, 0x09, 0x33, 0x09, 0x34, 0x15, 0x00, 0xff, 0x27, 0xff, 0xff,
	0x00, 0x00, 0x75, 0x10, 0x95, 0x04, 0x81, 0x02, 0x05, 0x01, 0x09, 0x32,
	0x09, 0x35, 0x15, 0x00, 0x26, 0xff, 0x7f, 0x75, 0x10, 0x95, 0x02, 0x81,
	0x02, 0x05, 0x09, 0x19, 0x01, 0x29, 0x10, 0x15, 0x00, 0x25, 0x01, 0x95,
	0x10, 0x75, 0x01, 0x81, 0x02, 0x05, 0x01, 0x09, 0x39, 0x15, 0x01, 0x25,
	0x08, 0x75, 0x04, 0x95, 0x01, 0x81, 0x42, 0xc0, 0xc0,
}

func msgUhidCreate(id uint16, vendor, product uint16, name string, desc []byte) []byte {
	var m W
	m.u8(12) // TYPE_UHID_CREATE
	m.u16(id)
	m.u16(vendor)
	m.u16(product)
	m.u8(uint8(len(name)))
	m.str(name)
	m.u16(uint16(len(desc)))
	m.raw(desc)
	return m.b.Bytes()
}

func msgUhidInput(id uint16, report []byte) []byte {
	var m W
	m.u8(13)
	m.u16(id)
	m.u16(uint16(len(report)))
	m.raw(report)
	return m.b.Bytes()
}

func msgUhidDestroy(id uint16) []byte {
	var m W
	m.u8(14)
	m.u16(id)
	return m.b.Bytes()
}

// gamepad report: 15 bytes (LX LY RX RY LT RT buttons16 hat)
func gamepadReport(lx, ly, rx, ry, buttons uint16, hat uint8) []byte {
	r := make([]byte, 15)
	binary.LittleEndian.PutUint16(r[0:], lx)
	binary.LittleEndian.PutUint16(r[2:], ly)
	binary.LittleEndian.PutUint16(r[4:], rx)
	binary.LittleEndian.PutUint16(r[6:], ry)
	binary.LittleEndian.PutUint16(r[8:], 0)
	binary.LittleEndian.PutUint16(r[10:], 0)
	binary.LittleEndian.PutUint16(r[12:], buttons)
	r[14] = hat
	return r
}

func adb(serial string, args ...string) ([]byte, error) {
	a := []string{}
	if serial != "" {
		a = append(a, "-s", serial)
	}
	a = append(a, args...)
	return exec.Command("adb", a...).CombinedOutput()
}

func randomScid() string {
	b := make([]byte, 4)
	rand.Read(b)
	b[0] &= 0x7f // the server parses scid with Integer.parseInt(,16): must fit int32
	return fmt.Sprintf("%08x", b)
}

func main() {
	serial := flag.String("s", "", "device serial")
	flag.Parse()
	if *serial == "" {
		out, _ := exec.Command("adb", "devices").Output()
		for _, l := range strings.Split(string(out), "\n")[1:] {
			f := strings.Fields(l)
			if len(f) >= 2 && f[1] == "device" {
				*serial = f[0]
				break
			}
		}
	}
	SCID = randomScid()
	fmt.Printf("== probe scrcpy protocol (serial=%s scid=%s)\n", *serial, SCID)

	// 1) push server payload
	fmt.Println(".. push server")
	if out, err := adb(*serial, "push", SERVER_PAYLOAD, DEV_PATH); err != nil {
		fmt.Println("push failed:", string(out), err)
		return
	}

	// 2) adb forward
	port := PORT + int(time.Now().UnixNano()%5000)
	// 0 enters) hygiene: kill stale servers, free the port forward
	adb(*serial, "shell", "pkill -f genymobile.scrcpy.Server")
	adb(*serial, "shell", "pkill -f scrcpy-server")
	time.Sleep(300 * time.Millisecond)
	adb(*serial, "forward", "--remove", fmt.Sprintf("tcp:%d", port))
	adb(*serial, "forward", "--no-rebind", fmt.Sprintf("tcp:%d", port),
		fmt.Sprintf("localabstract:scrcpy_%s", SCID))
	fmt.Printf(".. forward tcp:%d -> localabstract:scrcpy_%s\n", port, SCID)

	// 3) launch the server (audio disabled -> accept order: video, control)
	cmd := exec.Command("adb", appArgs(*serial,
		"shell",
		"CLASSPATH="+DEV_PATH,
		"app_process", "/", "com.genymobile.scrcpy.Server",
		"4.1", "scid="+SCID, "log_level=verbose", "tunnel_forward=true")...)
	sl, _ := os.Create("/tmp/probe_server.log")
	cmd.Stdout = sl
	cmd.Stderr = sl
	if err := cmd.Start(); err != nil {
		fmt.Println("launch failed:", err)
		return
	}

	// 4) connect video socket the way scrcpy does: dial + read the DUMMY
	// byte; the tunnel "connects" immediately even when the server hasn't
	// bound the abstract socket yet, so readiness = dummy byte arrival.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	var video net.Conn
	dummy := make([]byte, 1)
	connected := false
	for attempt := 0; attempt < 60 && !connected; attempt++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		c.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
		n, _ := c.Read(dummy)
		if n == 1 {
			fmt.Printf(".. dummy byte: %02x (attempt %d)\n", dummy[0], attempt)
			c.SetReadDeadline(time.Time{})
			video = c
			connected = true
			break
		}
		c.Close()
		c.SetReadDeadline(time.Time{})
		time.Sleep(400 * time.Millisecond)
	}
	if video == nil {
		fmt.Println("!! server never accepted (dummy never arrived)")
		cleanup(*serial, port)
		return
	}
	defer video.Close()

	// handshake + stream reading: ONE patient goroutine owns the video
	// socket (no shared deadlines — a timed-out read poisons the conn)
	videoState := new(atomic.Int64) // 0=waiting meta 1=meta done 2=streaming
	go func() {
		readN := func(n int) ([]byte, bool) {
			b := make([]byte, n)
			got := 0
			for got < n {
				video.SetReadDeadline(time.Now().Add(12 * time.Second))
				m, err := video.Read(b[got:])
				if m > 0 {
					got += m
				}
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue // still waiting for the encoder to emit
					}
					return nil, false
				}
			}
			video.SetReadDeadline(time.Time{})
			return b, true
		}
		name, ok := readN(64)
		if !ok {
			return
		}
		fmt.Printf(".. device meta (64B): %q\n", strings.TrimRight(string(name), "\x00"))
		videoState.Store(1)
		codec, ok := readN(4)
		if !ok {
			return
		}
		fmt.Printf(".. codec id: %s (0x%08x)\n",
			fmt.Sprintf("%c%c%c%c", codec[0], codec[1], codec[2], codec[3]),
			binary.BigEndian.Uint32(codec))
		videoState.Store(2)
		last := time.Now()
		waitSince := time.Now()
		frames := 0
		total := 0
		raw := make([]byte, 0, 1<<20)
		for {
			h, ok := readN(12)
			if !ok {
				return
			}
			if time.Since(waitSince) > 5*time.Second {
				fmt.Printf("   reader alive (still got a header after %s wait)\n",
					time.Since(waitSince).Round(time.Second))
				waitSince = time.Now()
			}
			ptsFlags := binary.BigEndian.Uint64(h)
			ln := binary.BigEndian.Uint32(h[8:])
			data, ok := readN(int(ln))
			if !ok {
				return
			}
			raw = append(raw, h...)
			raw = append(raw, data...)
			total++
			if ptsFlags&(1<<62) != 0 {
				fmt.Printf("   config packet: %d bytes | annexb head: %x\n", ln, data[:min(8, len(data))])
				continue
			}
			frames++
			if time.Since(last) >= time.Second {
				fmt.Printf("   video: ~%d packets/s (total %d, latest %d bytes)\n", frames, total, ln)
				os.WriteFile("/tmp/probe_video_raw.bin", raw, 0o644)
				frames = 0
				last = time.Now()
			}
		}
	}()

	// 5) audio socket (second accept) + control socket (third accept)
	time.Sleep(300 * time.Millisecond)
	aud, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Println("!! audio connect failed:", err)
	} else {
		defer aud.Close()
		fmt.Println(".. audio socket connected")
		// NOTE: no drain — audio reply probe below reads it directly
	}
	// bisect: which conn is CONTROL? SET_CLIPBOARD is unconditional and
	// device-visible — write to a conn, then read the clipboard back.
	focus := func() string {
		out, _ := adb(*serial, "shell",
			"dumpsys window | grep -E 'mCurrentFocus' | head -1")
		return strings.TrimSpace(string(out))
	}
	probe := func(name string, c net.Conn) {
		before := focus()
		c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		c.Write([]byte{5}) // EXPAND_NOTIFICATION_PANEL
		c.SetWriteDeadline(time.Time{})
		time.Sleep(900 * time.Millisecond)
		after := focus()
		isShade := strings.Contains(strings.ToLower(after), "notification")
		if isShade {
			fmt.Printf(".. [%s] IS CONTROL: shade opened -> %q\n", name, after)
		} else {
			fmt.Printf(".. [%s] no shade (was %q, now %q)\n", name, before, after)
		}
		// collapse for the next probe
		c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		c.Write([]byte{7}) // COLLAPSE_PANELS
		c.SetWriteDeadline(time.Time{})
		time.Sleep(700 * time.Millisecond)
		// verify collapsed before the next probe
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	probe("conn2", aud)
	ctrl, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Println("!! control connect failed:", err)
		cleanup(*serial, port)
		return
	}
	defer ctrl.Close()
	time.Sleep(300 * time.Millisecond)
	probe("conn3", ctrl)
	fmt.Println(".. conn2+conn3 connected & probed")

	// (no proactive drain here — probes below read ctrl directly)

	// now conn3 is confirmed CONTROL — step through messages ONE at a
	// time to find which one breaks the stream alignment
	fmt.Println(".. [control confirmed] message-by-message test")
	focusHas := func(sub string) bool {
		out, _ := adb(*serial, "shell",
			"dumpsys window | grep -E 'mCurrentFocus' | head -1")
		return strings.Contains(strings.ToLower(string(out)), sub)
	}
	step := func(name string, msg []byte, verify string, eat int) {
		fmt.Printf("   -> sending %s (%d bytes)\n", name, len(msg))
		ctrl.Write(msg)
		time.Sleep(time.Duration(eat) * time.Millisecond)
		if verify == "shade" && focusHas("notification") {
			fmt.Printf("      OK: shade/notification focus\n")
		}
		if verify == "launcher" && focusHas("launcher") {
			fmt.Printf("      OK: launcher focus (HOME worked)\n")
		}
	}
	step("EXPAND_PANEL", []byte{5}, "shade", 900)
	step("COLLAPSE", []byte{7}, "", 600)
	// POWER off/on twice — guaranteed screen animation for the encoder
	for i := 0; i < 2; i++ {
		ctrl.Write(msgInjectKeycode(0, 26))
		time.Sleep(60 * time.Millisecond)
		ctrl.Write(msgInjectKeycode(1, 26))
		time.Sleep(700 * time.Millisecond)
		awake, _ := adb(*serial, "shell",
			"dumpsys power | grep mWakefulness | head -1")
		fmt.Printf("   power cycle %d -> %s", i+1, string(awake))
		time.Sleep(700 * time.Millisecond) // screen-off -> screen-on animation
	}
	// UHID create ALONE (fresh session, right after the bisect) to prove
	// the format — if the stream stays aligned, the gamepad registers
	uhidMsg := msgUhidCreate(0x2000, 0x054c, 0x09cc, "scterm gamepad", gamepadDesc)
	fmt.Printf(".. UHID_CREATE msg: %d bytes\n", len(uhidMsg))
	ctrl.Write(uhidMsg)
	time.Sleep(1000 * time.Millisecond)
	outd, _ := adb(*serial, "shell",
		"dumpsys input | grep -i -A2 -E 'scterm|uhid' | head -8")
	fmt.Printf(".. uhid while held:\n%s\n", strings.TrimSpace(string(outd)))

	// 5a) control ECHO test: GET_CLIPBOARD — the server replies with a
	// DeviceMessage (CLIPBOARD) on the same socket if the channel works
	fmt.Println(".. GET_CLIPBOARD (expect a device msg reply)")
	ctrl.Write([]byte{8})
	ctrl.SetReadDeadline(time.Now().Add(3 * time.Second))
	reply := make([]byte, 32)
	rn, rerr := ctrl.Read(reply)
	if rn > 0 {
		fmt.Printf(".. control reply: % x\n", reply[:rn])
	} else if rerr != nil {
		fmt.Printf(".. control read: %v (no reply in 3s)\n", rerr)
	} else {
		fmt.Println(".. control read: <no data>")
	}
	ctrl.SetReadDeadline(time.Time{})

	// 5b) DECISIVE control test: SET_DISPLAY_POWER OFF — the display must go off
	fmt.Println(".. SET_DISPLAY_POWER OFF")
	ctrl.Write([]byte{10, 0})
	time.Sleep(700 * time.Millisecond)
	outp, _ := adb(*serial, "shell",
		"dumpsys power | grep -E 'mWakefulness' | head -1")
	fmt.Printf(".. display after power-off cmd: %s", strings.TrimSpace(string(outp)))
	if !strings.Contains(string(outp), "Asleep") &&
		!strings.Contains(string(outp), "mWakefulness=Asleep") {
		fmt.Printf("   (NOTE: still awake -> control cmd may not be applied)\n")
	}
	time.Sleep(200 * time.Millisecond)
	fmt.Println(".. SET_DISPLAY_POWER ON (restore)")
	ctrl.Write([]byte{10, 1})
	time.Sleep(500 * time.Millisecond)

	// 6) force the encoder to emit: open recents (screen animates)
	fmt.Println(".. injecting APP_SWITCH (recents) to force frames")
	ctrl.Write(msgInjectKeycode(0, 187))
	time.Sleep(60 * time.Millisecond)
	ctrl.Write(msgInjectKeycode(1, 187))
	time.Sleep(1 * time.Second)

	// 6b) inject a tap at 50%,50%
	fmt.Println(".. injecting tap (DOWN/UP at 50%%,50%%)")
	ctrl.Write(msgInjectTouch(0, 960, 540, 1920, 1080)) // ACTION_DOWN
	time.Sleep(80 * time.Millisecond)
	ctrl.Write(msgInjectTouch(1, 960, 540, 1920, 1080)) // ACTION_UP
	time.Sleep(300 * time.Millisecond)

	// 7) inject HOME via keycode
	fmt.Println(".. injecting KEYCODE_HOME")
	ctrl.Write(msgInjectKeycode(0, 3))
	time.Sleep(50 * time.Millisecond)
	ctrl.Write(msgInjectKeycode(1, 3))
	time.Sleep(500 * time.Millisecond)

	// 8) create a UHID gamepad + send a report (press A, dpad)
	fmt.Println(".. creating UHID gamepad (id 0x2000)")
	ctrl.Write(msgUhidCreate(0x2000, 0x054c, 0x09cc, "scterm gamepad", gamepadDesc))
	time.Sleep(300 * time.Millisecond)
	fmt.Println(".. sending gamepad report (A pressed + dpad right)")
	ctrl.Write(msgUhidInput(0x2000, gamepadReport(0x8000, 0x8000, 0x8000, 0x8000, 0x0001, 3)))
	time.Sleep(400 * time.Millisecond)
	ctrl.Write(msgUhidInput(0x2000, gamepadReport(0x8000, 0x8000, 0x8000, 0x8000, 0x0000, 0)))
	time.Sleep(400 * time.Millisecond)

	// hold the session open so side-checkers can observe the UHID
	// device + screen state while connected
	fmt.Println(".. holding session 8s (check device now)")

	time.Sleep(8 * time.Second)
	// verify the gamepad registered on the device
	out, _ := adb(*serial, "shell",
		"dumpsys input | grep -i -E 'uhid|gamepad|scterm' | head -5")
	if strings.TrimSpace(string(out)) == "" {
		out2, _ := adb(*serial, "shell", "dumpsys input | grep -iE 'Device' | head -10")
		fmt.Printf(".. (no uhid/gamepad match; input devices:)\n%s\n", strings.TrimSpace(string(out2)))
	} else {
		fmt.Printf(".. device input devices:\n%s\n", strings.TrimSpace(string(out)))
	}

	fmt.Println("== done (closing sockets, server exits) ==")
	cleanup(*serial, port)
}

func appArgs(serial string, tail ...string) []string {
	// args AFTER "adb" — call sites do exec.Command("adb", ...)
	a := []string{}
	if serial != "" {
		a = append(a, "-s", serial)
	}
	return append(a, tail...)
}

func cleanup(serial string, port int) {
	adb(serial, "forward", "--remove", fmt.Sprintf("tcp:%d", port))
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
