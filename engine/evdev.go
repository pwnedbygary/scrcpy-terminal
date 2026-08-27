// evdev gamepad reader for the TUI: reads host /dev/input/event* devices
// and translates joystick/button events into GamepadState snapshots.
package engine

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Linux input event (struct input_event): 16 bytes little-endian
// (sec u64, usec u64, type u16, code u16, value i32).
const (
	evKey  = 0x01
	evAbs  = 0x03
	evSync = 0x00

	// EV_KEY gamepad buttons (linux/input-event-codes.h)
	btnSouth = 0x130 // BTN_SOUTH (A)
	btnEast  = 0x131 // BTN_EAST (B)
	btnNorth = 0x133 // BTN_NORTH (Y)
	btnWest  = 0x134 // BTN_WEST (X)
	btnTL    = 0x136 // BTN_TL (LB)
	btnTR    = 0x137 // BTN_TR (RB)
	btnTL2   = 0x138
	btnTR2   = 0x139
	btnSelect = 0x13a
	btnStart  = 0x13b
	btnMode   = 0x13c
	btnThumbL = 0x13d
	btnThumbR = 0x13e

	// EV_ABS axes
	absX  = 0x00
	absY  = 0x01
	absZ  = 0x02
	absRX = 0x03
	absRY = 0x04
	absRZ = 0x05
	absHat0X = 0x10
	absHat0Y = 0x11
)

// GamepadReader tracks one evdev gamepad and emits states.
type GamepadReader struct {
	path string
	absMax map[int]int32 // axis -> max value for normalization
	state GamepadState
	ls, rs bool // left/right stick "pressed" state (buttons 11/12 are also num 8/9 hats on some pads)
}

func OpenGamepadReader() (*GamepadReader, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		name := readEvName(p)
		if !strings.Contains(strings.ToLower(name), "controller") &&
			!strings.Contains(strings.ToLower(name), "gamepad") &&
			!strings.Contains(strings.ToLower(name), "joystick") {
			continue
		}
		r := &GamepadReader{path: p, absMax: map[int]int32{}}
		_ = r
		return r, nil
	}
	return nil, fmt.Errorf("no gamepad device found in /dev/input")
}

func readEvName(path string) string {
	// EVIOCGNAME = _IOC_READ('E', 0x06, size) steps: read via sysfs instead
	b, err := os.ReadFile(filepath.Join("/sys/class/input", filepath.Base(path), "device/name"))
	if err == nil {
		return strings.TrimSpace(string(b))
	}
	// fallback: EVIOCGNAME(256) via unix ioctl is engine-free; sysfs covers most
	return path
}

// ReadLoop blocks, translating events until the device closes.
// cb receives the newest GamepadState whenever a sync event lands.
func (r *GamepadReader) ReadLoop(cb func(GamepadState)) error {
	f, err := os.Open(r.path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 24) // one input_event (16 bytes) + pad for alignment
	for {
		n, err := f.Read(buf[:16])
		if err != nil {
			return err
		}
		if n < 16 {
			continue
		}
		typ := binary.LittleEndian.Uint16(buf[8:])
		code := binary.LittleEndian.Uint16(buf[10:])
		val := int32(binary.LittleEndian.Uint32(buf[12:]))
		switch typ {
		case evKey:
			switch code {
			case btnSouth:
				r.state.A = val != 0
			case btnEast:
				r.state.B = val != 0
			case btnWest:
				r.state.X = val != 0
			case btnNorth:
				r.state.Y = val != 0
			case btnTL:
				r.state.LB = val != 0
			case btnTR:
				r.state.RB = val != 0
			case btnSelect:
				r.state.Back = val != 0
			case btnStart:
				r.state.Start = val != 0
			case btnMode:
				r.state.Guide = val != 0
			case btnThumbL:
				r.state.LStick = val != 0
			case btnThumbR:
				r.state.RStick = val != 0
			}
		case evAbs:
			axis(code, val, r)
		case evSync:
			cb(r.state)
		}
	}
}

func axis(code uint16, val int32, r *GamepadReader) {
	norm := func(v int32) float64 {
		mx := r.absMax[int(code)]
		if mx <= 0 {
			mx = 32767
		}
		return float64(v) / float64(mx)
	}
	switch code {
	case absX:
		r.state.LX = norm(val)
	case absY:
		r.state.LY = norm(val)
	case absRX:
		r.state.RX = norm(val)
	case absRY:
		r.state.RY = norm(val)
	case absZ:
		r.state.L2 = norm(val)
	case absRZ:
		r.state.R2 = norm(val)
	case absHat0X:
		if val < 0 {
			r.state.Hat = 7
		} else if val > 0 {
			r.state.Hat = 3
		} else if r.state.Hat == 7 || r.state.Hat == 3 {
			r.state.Hat = 0
		}
	case absHat0Y:
		if val < 0 {
			if r.state.Hat == 3 {
				r.state.Hat = 2
			} else if r.state.Hat == 7 {
				r.state.Hat = 8
			} else {
				r.state.Hat = 1
			}
		} else if val > 0 {
			if r.state.Hat == 3 {
				r.state.Hat = 4
			} else if r.state.Hat == 7 {
				r.state.Hat = 6
			} else {
				r.state.Hat = 5
			}
		}
	}
}

var _ = time.Now