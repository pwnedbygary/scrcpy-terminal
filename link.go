package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"os/exec"
	"time"
)

const (
	sctSocketPrefix  = "scrcpy_"
	deviceServerPath = "/data/local/tmp/scrcpy-server.jar"
	portFirst        = 27183
	portLast         = 27282
)

type session struct {
	adb      *adbRunner
	scid     uint32
	sockName string

	video   net.Conn
	audio   net.Conn
	control net.Conn

	deviceName string

	serverCmd   *exec.Cmd
	listener    net.Listener
	forwardMode bool
	forwardPort uint16
}

func newSession(serial string) (*session, error) {
	s := &session{adb: newADB(serial)}
	n, err := rand.Int(rand.Reader, big.NewInt(1<<31))
	if err != nil {
		return nil, err
	}
	s.scid = uint32(n.Int64())
	s.sockName = fmt.Sprintf(sctSocketPrefix+"%08x", s.scid)
	return s, nil
}

// start links the device: push server, open tunnel, start server, connect.
func (s *session) start(video, audio, control bool, params []string) error {
	// push server
	if err := s.adb.pushServer(serverJarData()); err != nil {
		return fmt.Errorf("push server: %w", err)
	}

	// Open tunnel. Prefer reverse (client listens), fall back to forward.
	if err := s.openTunnelReverse(); err == nil {
		s.forwardMode = false
	} else {
		fmt.Fprintf(stderrWriter(), "adb reverse unavailable (%v), using adb forward\n", err)
		if err := s.openTunnelForward(); err != nil {
			return fmt.Errorf("open tunnel: %w", err)
		}
		s.forwardMode = true
	}

	// Launch the server process on the device.
	serverParams := append([]string{
		fmt.Sprintf("scid=%08x", s.scid),
		"log_level=info",
	}, params...)
	cmd, err := s.adb.startServer(serverParams)
	if err != nil {
		s.closeTunnel()
		return fmt.Errorf("start server: %w", err)
	}
	s.serverCmd = cmd

	// Connect the sockets.
	if err := s.connect(video, audio, control); err != nil {
		s.stop()
		return err
	}
	// Tunnel no longer needed.
	s.closeTunnel()
	return nil
}

func (s *session) openTunnelReverse() error {
	for port := portFirst; port <= portLast; port++ {
		// Bind the listener first so the device connection has a target.
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		if err := s.adb.reverse(s.sockName, uint16(port)); err != nil {
			ln.Close()
			return err // adb reverse failing is fatal for reverse mode
		}
		s.listener = ln
		return nil
	}
	return fmt.Errorf("no free port in %d:%d", portFirst, portLast)
}

func (s *session) openTunnelForward() error {
	for port := portFirst; port <= portLast; port++ {
		if err := s.adb.forward(uint16(port), s.sockName); err == nil {
			s.forwardPort = uint16(port)
			return nil
		}
	}
	return fmt.Errorf("no free port forward range")
}

// connect accepts (reverse) or dials (forward) the sockets in order:
// video, audio, control.
func (s *session) connect(video, audio, control bool) error {
	// In reverse mode the device connects to us: accept in order.
	// In forward mode we connect to the device socket via the adb tunnel.
	if !s.forwardMode {
		deadline := time.Now().Add(15 * time.Second)
		conns := []*net.Conn{&s.video, &s.audio, &s.control}
		enabled := []bool{video, audio, control}
		for i := range conns {
			if !enabled[i] {
				continue
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for %s socket", socketName(i))
			}
			conn, err := s.listener.Accept()
			if err != nil {
				return fmt.Errorf("accept %s: %w", socketName(i), err)
			}
			*conns[i] = conn
		}
	} else {
		// Forward: server listens on the device abstract socket; we dial
		// through adb forward. The first socket gets a dummy byte from the
		// device which we must consume before reading meta.
		addr := fmt.Sprintf("127.0.0.1:%d", s.forwardPort)
		first := true
		for _, enabled := range []bool{video, audio, control} {
			if !enabled {
				continue
			}
			var conn net.Conn
			var err error
			for attempt := 0; attempt < 200; attempt++ {
				conn, err = net.DialTimeout("tcp", addr, 250*time.Millisecond)
				if err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if err != nil {
				return fmt.Errorf("dial %s: %w", addr, err)
			}
			if first {
				var b [1]byte
				if _, err := io.ReadFull(conn, b[:]); err != nil {
					conn.Close()
					return fmt.Errorf("dummy byte: %w", err)
				}
				first = false
			}
			if s.video == nil {
				s.video = conn
			} else if s.audio == nil {
				s.audio = conn
			} else {
				s.control = conn
			}
		}
	}

	// Set TCP_NODELAY on control.
	if s.control != nil {
		if tc, ok := s.control.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
	}

	// Device meta: 64-byte device name on the first socket.
	first := s.video
	if first == nil {
		first = s.audio
	}
	if first == nil {
		first = s.control
	}
	var meta [64]byte
	if _, err := io.ReadFull(first, meta[:]); err != nil {
		return fmt.Errorf("device meta: %w", err)
	}
	s.deviceName = cstring(meta[:])
	return nil
}

func socketName(i int) string {
	switch i {
	case 0:
		return "video"
	case 1:
		return "audio"
	default:
		return "control"
	}
}

func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func (s *session) closeTunnel() {
	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
	}
	if s.forwardMode {
		if s.forwardPort != 0 {
			s.adb.forwardRemove(s.forwardPort)
			s.forwardPort = 0
		}
	} else if s.sockName != "" {
		s.adb.reverseRemove(s.sockName)
	}
}

func (s *session) stop() {
	s.closeTunnel()
	for _, c := range []net.Conn{s.video, s.audio, s.control} {
		if c != nil {
			c.Close()
		}
	}
	// The server exits when sockets close; give it a moment, then kill.
	if s.serverCmd != nil && s.serverCmd.Process != nil {
		time.Sleep(200 * time.Millisecond)
		s.serverCmd.Process.Kill()
		s.serverCmd.Wait()
		s.serverCmd = nil
	}
}

// serverParams builds the key=value params for the server launch.
func serverParams(video, audio, control bool, cfg config) []string {
	p := []string{}
	if !cfg.video {
		p = append(p, "video=false")
	}
	if !cfg.audio {
		p = append(p, "audio=false")
	}
	if !cfg.control {
		p = append(p, "control=false")
	}
	if cfg.videoBitRate != 0 {
		p = append(p, fmt.Sprintf("video_bit_rate=%d", cfg.videoBitRate))
	}
	if cfg.audioBitRate != 0 {
		p = append(p, fmt.Sprintf("audio_bit_rate=%d", cfg.audioBitRate))
	}
	if cfg.maxSize != 0 {
		p = append(p, fmt.Sprintf("max_size=%d", cfg.maxSize))
	}
	if cfg.maxFps != 0 {
		p = append(p, fmt.Sprintf("max_fps=%s", fmtFPS(cfg.maxFps)))
	}
	if cfg.audioDup {
		p = append(p, "audio_dup=true")
	}
	return p
}

func fmtFPS(f float64) string {
	if f == float64(int(f)) {
		return fmt.Sprintf("%d", int(f))
	}
	return fmt.Sprintf("%.3f", f)
}

var _ = binary.BigEndian // placeholder to keep imports tidy if refactored
