package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"scterm/peer"
)

// TestWebPortErrorsSayWhatToDo: when the browser display cannot listen, the
// line names the address, and the port it is about, and says what to do: for a
// port in use, in the words of scterm doctor; for a port that needs privileges;
// and for an address that is not one of this computer's. Anything else, such
// as an impossible port or a name that does not resolve, is said as listening
// said it. The errors are real where this computer makes them, and built for
// every case as well.
func TestWebPortErrorsSayWhatToDo(t *testing.T) {
	const another = "choose another port with --web-port PORT (0 picks a free one)"
	notHere := "web: 192.0.2.1 is not an address of this computer: " +
		"--web-addr takes one of its addresses, 0.0.0.0 for all of them, or the default, 127.0.0.1"
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port
	inUse := fmt.Sprintf("web: port %d on 127.0.0.1 is in use: stop the program that uses it, or %s", port, another)

	type listening struct {
		addr string
		port int
		want string
	}
	listens := []listening{
		{"127.0.0.1", port, inUse},
		{"", port, inUse}, // --web-addr "" listens on 127.0.0.1
		{"127.0.0.1", 70000, "web: listen on 127.0.0.1:70000: listen tcp: address 70000: invalid port"},
	}
	// 192.0.2.1 (TEST-NET-1) is no address of this computer, and binding to it
	// fails at once, unless the kernel lets programs bind to any address.
	if procSys("net/ipv4/ip_nonlocal_bind") == 0 {
		listens = append(listens, listening{"192.0.2.1", 6969, notHere})
	} else {
		t.Log("this computer lets programs bind to any address: the 192.0.2.1 case is built only")
	}
	// A port below net.ipv4.ip_unprivileged_port_start needs privileges.
	if start := procSys("net/ipv4/ip_unprivileged_port_start"); os.Geteuid() != 0 && start > 1 {
		p := min(start, 1024) - 1
		listens = append(listens, listening{"127.0.0.1", p,
			fmt.Sprintf("web: port %d on 127.0.0.1 needs privileges that scterm does not have: %s", p, another)})
	} else {
		t.Log("no port needs privileges that this test lacks: that case is built only")
	}
	for _, tc := range listens {
		ln, err := listenWeb(tc.addr, tc.port)
		if err == nil {
			ln.Close()
			t.Errorf("listening on port %d on %q worked; want %q", tc.port, tc.addr, tc.want)
		} else if err.Error() != tc.want {
			t.Errorf("listening on port %d on %q: %q; want %q", tc.port, tc.addr, err, tc.want)
		}
	}

	listenErr := func(err error) error { return &net.OpError{Op: "listen", Net: "tcp", Err: err} }
	for _, tc := range []struct {
		addr string
		port int
		err  error
		want string
	}{
		{"127.0.0.1", 80, listenErr(os.NewSyscallError("bind", syscall.EACCES)),
			"web: port 80 on 127.0.0.1 needs privileges that scterm does not have: " + another},
		{"192.0.2.1", 6969, listenErr(os.NewSyscallError("bind", syscall.EADDRNOTAVAIL)), notHere},
		{"nosuch.invalid", 6969, listenErr(&net.DNSError{Err: "no such host", Name: "nosuch.invalid", IsNotFound: true}),
			"web: listen on nosuch.invalid:6969: listen tcp: lookup nosuch.invalid: no such host"},
	} {
		if got := webListenError(tc.addr, tc.port, tc.err).Error(); got != tc.want {
			t.Errorf("%v: %q; want %q", tc.err, got, tc.want)
		}
	}

	if l := doctorWebPort("127.0.0.1", port); inUse != "web: "+l.detail+": "+l.todo {
		t.Errorf("scterm doctor says %q and %q, the browser display %q; want the same words", l.detail, l.todo, inUse)
	}
}

// TestWebServesThePortMainHeld: once the session has started, the browser
// display serves the listener that main opened before any device or peer
// work: on a fixed port, on the port the kernel picked for --web-port 0, and
// on the --web-addr given, here 127.0.0.2 with the same port held on
// 127.0.0.1, where a listener on 127.0.0.1 would fail. A control-only session
// with the loopback fake peer target takes main past start-up with no device,
// no adb call and no browser.
func TestWebServesThePortMainHeld(t *testing.T) {
	target := newFakePeerTarget(t, controlWelcome)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldPort := held.Addr().(*net.TCPAddr).Port
	for _, tc := range []struct {
		name, addr string
		port       int
	}{
		{"a fixed port", "127.0.0.1", freeLoopbackPort(t)},
		{"port 0", "127.0.0.1", 0},
		{"--web-addr 127.0.0.2, with its port held on 127.0.0.1", "127.0.0.2", heldPort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--peer", "Pixel 8", "--web", "--web-addr", tc.addr, "--web-port", strconv.Itoa(tc.port),
				"--video=false", "--audio=false"}
			cmd, lines := startMainLines(t, args, peerSessionEnv(t, t.TempDir(), target))
			url, seen := webDisplayURL(t, lines)
			if tc.port != 0 {
				if want := "http://" + net.JoinHostPort(tc.addr, strconv.Itoa(tc.port)) + "/"; url != want {
					t.Errorf("prints %s; want %s", url, want)
				}
			} else if strings.HasSuffix(url, ":0/") {
				t.Errorf("prints %s, port 0", url)
			}
			if err := getPage(url); err != nil {
				t.Error(err)
			}
			endMain(t, cmd, lines, seen)
		})
	}
}

// TestWebListenerIsNotInheritedByADB: the adb that main runs while it holds
// the browser display's port does not get the listener, so an adb server that
// it starts cannot keep the port after scterm ends. The fake adb lists its
// open files; handed a listener on purpose, it shows a socket.
func TestWebListenerIsNotInheritedByADB(t *testing.T) {
	dir := t.TempDir()
	fds := filepath.Join(dir, "fds")
	bin := fdListingADB(t, dir, fds)
	given, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer given.Close()
	f, err := given.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := exec.Command(filepath.Join(bin, "adb"), "devices")
	c.ExtraFiles = []*os.File{f}
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("the fake adb: %v %s", err, out)
	}
	if got, _ := os.ReadFile(fds); !strings.Contains(string(got), "socket:") {
		t.Fatalf("handed a listener, the fake adb listed no socket:\n%s", got)
	}

	dir = t.TempDir()
	fds = filepath.Join(dir, "fds")
	port := strconv.Itoa(freeLoopbackPort(t))
	cmd, stderr := startMain(t, []string{"--web", "--web-port", port}, scratchEnv(t, dir, fdListingADB(t, dir, fds)))
	status := childStatus(cmd.Wait())
	got, err := os.ReadFile(fds)
	if err != nil {
		t.Fatalf("scterm --web --web-port %s never ran adb: status %d, stderr %q", port, status, stderr.String())
	}
	if strings.Contains(string(got), "socket:") {
		t.Errorf("the adb that main ran while it held port %s had a socket open:\n%s", port, got)
	}
}

// TestWebRequestDuringStartUpIsServed: a browser that connects while the
// session starts, as a page left open reconnects, waits on the port that main
// holds and gets the page once the display serves, where it used to be
// refused. The fake target holds its welcome back until the request has had
// no answer for a while.
func TestWebRequestDuringStartUpIsServed(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(open)
	target := newAnsweringPeerTarget(t, func(*peer.Message) peer.Message { <-gate; return controlWelcome })
	port := strconv.Itoa(freeLoopbackPort(t))
	cmd, lines := startMainLines(t, []string{"--peer", "Pixel 8", "--web", "--web-port", port, "--video=false", "--audio=false"},
		peerSessionEnv(t, t.TempDir(), target))
	var conn net.Conn
	for deadline := time.Now().Add(10 * time.Second); conn == nil; {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
		switch {
		case err == nil:
			conn = c
		case time.Now().After(deadline):
			t.Fatalf("main never listened on port %s: %v", port, err)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: 127.0.0.1:" + port + "\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
	var one [1]byte
	n, err := conn.Read(one[:])
	var timeout net.Error
	if n > 0 || !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("while the session started: read %d bytes, %v; want no answer yet", n, err)
	}
	open()
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("once the session started: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<canvas id="screen"`) {
		t.Errorf("the request made during start-up got status %d, want the page", resp.StatusCode)
	}
	endMain(t, cmd, lines, nil)
}

// startMainLines starts main as startMain does, with its stderr as a channel
// of lines, closed when main's stderr closes. Read the channel to its end
// before Wait.
func startMainLines(t *testing.T, args, env []string) (*exec.Cmd, <-chan string) {
	t.Helper()
	cmd := mainCommand(t, args, env)
	pipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(pipe)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	return cmd, lines
}

// peerSessionEnv is scratchEnv in dir, with only a fake adb on PATH, target
// paired as "Pixel 8" with control, and a PULSE_SERVER that names no server,
// with autospawn off, so the sound sink fails at once without reaching one.
func peerSessionEnv(t *testing.T, dir string, target *fakePeerTarget) []string {
	t.Helper()
	env := scratchEnv(t, dir, fakeADB(t, dir, filepath.Join(dir, "adb.log"), ""))
	conf := filepath.Join(dir, "client.conf")
	if err := os.WriteFile(conf, []byte("autospawn = no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := peer.OpenStore(filepath.Join(dir, "scterm")).Put(peer.Record{Fingerprint: target.fingerprint(),
		Name: "Pixel 8", Host: "127.0.0.1", Port: target.port(), Grants: []string{"control"}, PairedAtMs: 1}); err != nil {
		t.Fatal(err)
	}
	return append(env, "PULSE_SERVER=unix:"+filepath.Join(dir, "no-server", "native"), "PULSE_CLIENTCONFIG="+conf)
}

// webDisplayURL reads main's stderr until main prints where the browser
// display is, and returns that and the lines read.
func webDisplayURL(t *testing.T, lines <-chan string) (string, []string) {
	t.Helper()
	var seen []string
	deadline := time.After(25 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("main ended before it served; stderr:\n%s", strings.Join(seen, "\n"))
			}
			seen = append(seen, l)
			if url, found := strings.CutPrefix(l, "scterm: web display on "); found {
				return url, seen
			}
		case <-deadline:
			t.Fatalf("no web display line in 25 s; stderr:\n%s", strings.Join(seen, "\n"))
		}
	}
}

// getPage asks the browser display at url for its page.
func getPage(url string) error {
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<canvas id="screen"`) {
		return fmt.Errorf("GET %s: status %d, not the page", url, resp.StatusCode)
	}
	return nil
}

// endMain ends main with SIGTERM, as a session ends, and wants status 0.
func endMain(t *testing.T, cmd *exec.Cmd, lines <-chan string, seen []string) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for l := range lines {
		seen = append(seen, l)
	}
	if status := childStatus(cmd.Wait()); status != 0 {
		t.Errorf("after SIGTERM: status %d, want 0; stderr:\n%s", status, strings.Join(seen, "\n"))
	}
}

// freeLoopbackPort is a port on 127.0.0.1 that was free a moment ago.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// fdListingADB makes, in dir, a directory for PATH that holds only an adb,
// which lists its open files in fds and lists no device.
func fdListingADB(t *testing.T, dir, fds string) string {
	t.Helper()
	script := "#!/bin/sh\n/bin/ls -l /proc/$$/fd >> '" + fds + "' 2>&1\nprintf 'List of devices attached\\n\\n'\n"
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "adb"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

// procSys reads a number from /proc/sys, or returns -1.
func procSys(name string) int {
	b, err := os.ReadFile("/proc/sys/" + name)
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return -1
	}
	return n
}
