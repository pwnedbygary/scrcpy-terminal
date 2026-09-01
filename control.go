package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

// dbgControl: when non-empty, log control messages to stderr.
var dbgControl = os.Getenv("SCT_DEBUG_CONTROL")

// controller serializes and sends control messages to the device.
type controller struct {
	conn net.Conn
	mu   sync.Mutex
}

var errNoControl = errors.New("control disabled")

func newController(conn net.Conn) *controller {
	return &controller{conn: conn}
}

func (c *controller) send(mtype byte, payload []byte) error {
	if c == nil || c.conn == nil {
		return errNoControl
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	wire := serializeControlMsg(payload, mtype)
	_, err := c.conn.Write(wire)
	return err
}

func (c *controller) sendRaw(wire []byte) error {
	if c == nil || c.conn == nil {
		return errNoControl
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.conn.Write(wire)
	return err
}

// ---------------------------------------------------------------------------
// high-level input helpers
// ---------------------------------------------------------------------------

// injectKey sends a keyevent down/up pair.
func (c *controller) injectKey(keycode uint32, metastate uint32) error {
	if dbgControl != "" {
		fmt.Fprintf(os.Stderr, "scterm: key %d meta=0x%x\n", keycode, metastate)
	}
	if err := c.send(ctrlInjectKeycode, keycodeMsg(keyActionDown, keycode, 0, metastate)); err != nil {
		return err
	}
	return c.send(ctrlInjectKeycode, keycodeMsg(keyActionUp, keycode, 0, metastate))
}

// injectText sends a text payload (utf-8).
func (c *controller) injectText(text string) error {
	// msg layout: [pad(1), len(4)?] -- we write pad(1) then the writer adds len
	payload := make([]byte, 1+4+len(text))
	copy(payload[1+4:], text)
	// serializeControlMsg for injectText re-derives len from msg[5:]
	return c.send(ctrlInjectText, payload)
}

// backOrScreenOn sends the back key action.
func (c *controller) backOrScreenOn(down bool) error {
	action := uint8(1)
	if down {
		action = 0
	}
	return c.send(ctrlBackOrScreenOn, []byte{action})
}

func (c *controller) back() error {
	if err := c.backOrScreenOn(true); err != nil {
		return err
	}
	return c.backOrScreenOn(false)
}

func (c *controller) touch(down bool, pos position) error {
	if dbgControl != "" {
		fmt.Fprintf(os.Stderr, "scterm: touch down=%v at %d,%d (%dx%d)\n", down, pos.x, pos.y, pos.screenW, pos.screenH)
	}
	action := byte(motionActionUp)
	pressure := uint16(0)
	if down {
		action = motionActionDown
		pressure = floatToU16fp(1.0)
	}
	return c.send(ctrlInjectTouch, touchMsg(action, pointerIDMouse, pos, pressure, 0, 0))
}

func (c *controller) touchMove(pos position) error {
	if dbgControl != "" {
		fmt.Fprintf(os.Stderr, "scterm: touch move at %d,%d\n", pos.x, pos.y)
	}
	return c.send(ctrlInjectTouch,
		touchMsg(motionActionMove, pointerIDMouse, pos, floatToU16fp(1.0), 0, 0))
}

func (c *controller) touchRelease(pos position) error {
	return c.send(ctrlInjectTouch,
		touchMsg(motionActionUp, pointerIDMouse, pos, 0, 0, 0))
}

// scroll sends a vertical scroll (wheel delta units, positive = up).
func (c *controller) scroll(pos position, vscroll float32, buttons uint32) error {
	if dbgControl != "" {
		fmt.Fprintf(os.Stderr, "scterm: scroll %f at %d,%d\n", vscroll, pos.x, pos.y)
	}
	return c.send(ctrlInjectScroll, scrollMsg(pos, 0, vscroll, buttons))
}

func (c *controller) expandNotifications() error {
	return c.send(ctrlExpandNotif, nil)
}

func (c *controller) expandSettings() error {
	return c.send(ctrlExpandSettings, nil)
}

func (c *controller) collapsePanels() error {
	return c.send(ctrlCollapsePanels, nil)
}

func (c *controller) rotate() error {
	return c.send(ctrlRotateDevice, nil)
}

func (c *controller) resetVideo() error {
	return c.send(ctrlResetVideo, nil)
}

// ---------------------------------------------------------------------------
// device messages (device -> client) on the control socket
// ---------------------------------------------------------------------------

// deviceMsgReader drains and parses messages from the device.
func deviceMsgReader(conn net.Conn) {
	buf := make([]byte, 1024)
	var acc []byte
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(stderrWriter(), "scterm: control recv: %v\n", err)
			}
			return
		}
		acc = append(acc, buf[:n]...)
		for {
			consumed, ok := parseDeviceMessage(acc)
			if !ok {
				break
			}
			acc = acc[consumed:]
		}
		if len(acc) > 1<<19 {
			acc = nil // protocol desync protection
		}
	}
}

// parseDeviceMessage returns (bytesConsumed, completed).
func parseDeviceMessage(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	switch b[0] {
	case 0: // clipboard: type + u32 length + utf8
		if len(b) < 5 {
			return 0, false
		}
		l := int(binary.BigEndian.Uint32(b[1:5]))
		if l > 1<<18 {
			return 1 + 4 + 0, true // corrupt, skip
		}
		if len(b) < 5+l {
			return 0, false
		}
		text := string(b[5 : 5+l])
		fmt.Fprintf(stderrWriter(), "scterm: clipboard: %q\n", text)
		return 5 + l, true
	case 1: // ack clipboard: 8-byte sequence
		if len(b) < 9 {
			return 0, false
		}
		return 9, true
	default:
		fmt.Fprintf(stderrWriter(), "scterm: unknown device msg %d\n", b[0])
		return 1, true
	}
}
