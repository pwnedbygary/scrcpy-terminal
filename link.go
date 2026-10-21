package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os/exec"
	"strings"
	"time"

	"scterm/peer"
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
	serverOut   *tailBuffer   // the server's last output, for errors
	serverDone  chan struct{} // closed once the server process has exited
	listener    net.Listener
	forwardMode bool
	forwardPort uint16

	// peer is set in peer mode (no adb): the connection to an scterm target.
	peer *peer.Session
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
	if s.forwardMode {
		// Unless told otherwise the server connects out, through adb reverse.
		serverParams = append(serverParams, "tunnel_forward=true")
	}
	s.serverOut = &tailBuffer{max: 4096}
	cmd, err := s.adb.startServer(serverParams, s.serverOut)
	if err != nil {
		s.closeTunnel()
		return fmt.Errorf("start server: %w", err)
	}
	s.serverCmd = cmd
	done := make(chan struct{})
	s.serverDone = done
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

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
		// Accept blocks until the device connects, so it needs its own
		// deadline, and a server that exits (it refuses a bad option before
		// connecting) must end the wait too.
		ln := s.listener
		if tl, ok := ln.(*net.TCPListener); ok {
			_ = tl.SetDeadline(time.Now().Add(connectTimeout))
		}
		accepted := make(chan struct{})
		defer close(accepted)
		serverDone := s.serverDone
		go func() {
			select {
			case <-serverDone:
				ln.Close()
			case <-accepted:
			}
		}()
		conns := []*net.Conn{&s.video, &s.audio, &s.control}
		enabled := []bool{video, audio, control}
		for i := range conns {
			if !enabled[i] {
				continue
			}
			conn, err := ln.Accept()
			if err != nil {
				if s.serverExited() {
					return s.serverStoppedError()
				}
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					return fmt.Errorf("the device did not connect its %s socket within %s%s",
						socketName(i), connectTimeout, s.serverOutputNote())
				}
				return fmt.Errorf("accept %s: %w", socketName(i), err)
			}
			*conns[i] = conn
		}
	} else {
		// Forward: the server listens on the device abstract socket and
		// accepts in order, like the reverse sockets; we dial through adb
		// forward. All of it shares one deadline, as the accepts do.
		deadline := time.Now().Add(connectTimeout)
		conns := []*net.Conn{&s.video, &s.audio, &s.control}
		enabled := []bool{video, audio, control}
		first := true
		for i := range conns {
			if !enabled[i] {
				continue
			}
			conn, err := s.dialForward(i, first, deadline)
			if err != nil {
				return err
			}
			*conns[i] = conn
			first = false
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
	_ = first.SetReadDeadline(time.Now().Add(connectTimeout))
	if _, err := io.ReadFull(first, meta[:]); err != nil {
		if s.serverExited() {
			return s.serverStoppedError()
		}
		return fmt.Errorf("device meta: %w%s", err, s.serverOutputNote())
	}
	_ = first.SetReadDeadline(time.Time{})
	s.deviceName = cstring(meta[:])
	return nil
}

// dialForward dials socket i through adb forward, retrying until deadline.
// adb accepts a dial even before the server listens, then hangs up, so the
// first socket only counts once the dummy byte the server sends on it arrives.
// A server that exits ends the wait at once, even mid-dial or mid-read.
func (s *session) dialForward(i int, first bool, deadline time.Time) (net.Conn, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", s.forwardPort)
	exited, stopWatching := s.watchServer()
	defer stopWatching()
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	for {
		conn, err := dialer.DialContext(exited, "tcp", addr)
		dialed := err == nil
		if dialed && first {
			if err = readDummyByte(exited, conn, deadline); err != nil {
				conn.Close()
			}
		}
		if err == nil {
			return conn, nil
		}
		if s.serverExited() {
			return nil, s.serverStoppedError()
		}
		if time.Until(deadline) < 50*time.Millisecond {
			if !dialed {
				return nil, fmt.Errorf("dial %s: %w%s", addr, err, s.serverOutputNote())
			}
			return nil, fmt.Errorf("the device did not connect its %s socket within %s%s",
				socketName(i), connectTimeout, s.serverOutputNote())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// watchServer returns a context that ends when the server exits, and a
// function that ends it anyway and returns once nothing watches any more.
func (s *session) watchServer() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := s.serverDone
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		cancel()
		<-watching
	}
}

// readDummyByte waits until deadline for the byte the server sends first on
// its first socket. ctx ending closes conn, which ends the wait at once; it
// then fails even if the byte came, since conn is closed.
func readDummyByte(ctx context.Context, conn net.Conn, deadline time.Time) error {
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		conn.Close()
		close(closed)
	})
	var b [1]byte
	_ = conn.SetReadDeadline(deadline)
	_, err := io.ReadFull(conn, b[:])
	if !stop() {
		<-closed
		return net.ErrClosed
	}
	if err == nil {
		_ = conn.SetReadDeadline(time.Time{})
	}
	return err
}

// connectTimeout bounds each wait for the device side to connect or speak.
var connectTimeout = 15 * time.Second

// serverExited reports whether the device-side server process has exited.
func (s *session) serverExited() bool {
	if s.serverDone == nil {
		return false
	}
	select {
	case <-s.serverDone:
		return true
	default:
		return false
	}
}

// serverStoppedError explains a server that exited before it connected.
func (s *session) serverStoppedError() error {
	return fmt.Errorf("the scrcpy server on the device stopped before connecting%s", s.serverOutputNote())
}

// serverOutputNote is the server's last lines, indented, for an error message.
func (s *session) serverOutputNote() string {
	if s.serverOut == nil {
		return ""
	}
	lines := s.serverOut.lastLines(6)
	if len(lines) == 0 {
		return ""
	}
	return "; it said:\n  " + strings.Join(lines, "\n  ")
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
	if s.peer != nil {
		s.peer.Close(false)
		return
	}
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
		<-s.serverDone // reaped by the goroutine start runs Wait on
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
	if cfg.audioSource != "" {
		p = append(p, fmt.Sprintf("audio_source=%s", cfg.audioSource))
	}
	if cfg.audioCodec != "" {
		p = append(p, fmt.Sprintf("audio_codec=%s", cfg.audioCodec))
	}
	return p
}

func fmtFPS(f float64) string {
	if f == float64(int(f)) {
		return fmt.Sprintf("%d", int(f))
	}
	return fmt.Sprintf("%.3f", f)
}
