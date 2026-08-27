// verify exercises the engine Session against a real device: opens a
// session (control role-verified), reads video packets, injects
// POWER/tap/UHID gamepad, and reports what the wire delivered.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"scterm/engine"
)

func main() {
	serial := flag.String("s", "", "serial")
	flag.Parse()
	if *serial == "" {
		fmt.Println("need -s serial")
		os.Exit(1)
	}
	fmt.Println("== engine.verify ==")
	s, err := engine.Open(*serial, engine.Options{Audio: true, CodecOptions: "i-frame-interval=2"})
	if err != nil {
		fmt.Println("open failed:", err)
		os.Exit(1)
	}
	defer s.Close()
	fmt.Printf("session scid=%s device=%q codec=%s\n", s.SCID, s.Video.Device, s.Video.Codec)

	// video reader
	go func() {
		last := time.Now()
		frames := 0
		for {
			pkt, err := s.Video.Next()
			if err != nil {
				fmt.Println("video read end:", err)
				return
			}
			if pkt.Config {
				fmt.Printf("  config packet: %d bytes (head %x)\n", len(pkt.Data), pkt.Data[:min(8, len(pkt.Data))])
				continue
			}
			frames++
			if time.Since(last) >= time.Second {
				fmt.Printf("  video: ~%d packets/s (last %d bytes, key=%v)\n", frames, len(pkt.Data), pkt.KeyFrame)
				frames = 0
				last = time.Now()
			}
		}
	}()

	// control: power off/on (screen animates -> encoder should emit)
	for i := 0; i < 2; i++ {
		s.Ctrl.Power()
		time.Sleep(900 * time.Millisecond)
	}

	// tap the center
	s.Ctrl.Tap(960, 540, 1920, 1080)
	time.Sleep(500 * time.Millisecond)

	// UHID gamepad: create, send a report (A+dpad right), destroy
	s.Ctrl.UhidCreate(0x2000, 0x054c, 0x09cc, "scterm gamepad", engine.GamepadDesc)
	time.Sleep(500 * time.Millisecond)
	s.Ctrl.UhidInput(0x2000, engine.GamepadReport(0x8000, 0x8000, 0x8000, 0x8000, 0x0001, 3))
	time.Sleep(400 * time.Millisecond)
	s.Ctrl.UhidInput(0x2000, engine.GamepadReport(0x8000, 0x8000, 0x8000, 0x8000, 0x0000, 0))
	time.Sleep(400 * time.Millisecond)
	s.Ctrl.UhidDestroy(0x2000)
	fmt.Println("gamepad create/report/destroy sent")

	// while still held: is the UHID device visible on the device?
	s.Ctrl.UhidCreate(0x2000, 0x054c, 0x09cc, "scterm gamepad", engine.GamepadDesc)
	fmt.Println("UHID_CREATE sent; holding 5s")
	time.Sleep(5 * time.Second)
	s.Ctrl.UhidDestroy(0x2000)
	time.Sleep(500 * time.Millisecond)
	fmt.Println("== done ==")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}