package main

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// The tests here run update, or a part of it, in a child process: the test
// binary again, with SCTERM_TEST_UPDATE set. Their signals go to the child,
// never to the test process.

// inUpdateChild does what a test started this process for, when it is one
// of these children, and exits. SCTERM_TEST_UPDATE holds what to do, the
// release binary to do it to and the URL of the release list, one to a line.
// "update" updates the binary as scterm 1.2.3 without asking, and "ask"
// asks first, as on a terminal; an error ends either as it ends scterm
// update. "copy" copies the binary with copyBeside, with files limited to
// 4 kB, and prints the error. "terminal" prints whether stderr is a terminal.
func inUpdateChild() {
	v, ok := os.LookupEnv("SCTERM_TEST_UPDATE")
	if !ok {
		return
	}
	what, rest, _ := strings.Cut(v, "\n")
	exe, latest, _ := strings.Cut(rest, "\n")
	switch what {
	case "terminal":
		fmt.Println(stderrIsTerminal())
	case "copy":
		var limit syscall.Rlimit
		syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit)
		syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 4096, Max: limit.Max})
		_, err := copyBeside(exe)
		syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit)
		fmt.Println(err)
	default:
		latestRelease, version = latest, "1.2.3"
		interactive = func() bool { return what == "ask" }
		if err := updateRelease(exe, updateOptions{yes: what == "update"}); err != nil {
			fatal(err)
		}
	}
	os.Exit(0)
}

// updateChild is a child process for test, which does what inUpdateChild
// says, with nothing from the test's environment but PATH.
func updateChild(test, what, exe, latest string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^"+test+"$")
	cmd.Env = []string{"SCTERM_TEST_UPDATE=" + what + "\n" + exe + "\n" + latest, "PATH=" + os.Getenv("PATH")}
	if dir, ok := os.LookupEnv("GOCOVERDIR"); ok {
		// Under go test -cover the child warns on stderr without it.
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// startChild starts cmd in a process group of its own, which the test's end
// kills, with SIGINT and SIGHUP at their defaults, as a shell starts a
// command, also when this process ignores them, as a background job does: a
// signal caught at the fork is reset in the child, where an ignored one
// would stay ignored. They are caught whether ignored or not, as once Notify
// has caught one, signal.Ignored no longer says so after Stop.
func startChild(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	caught := make(chan os.Signal, 1)
	signal.Notify(caught, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(caught)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	group := cmd.Process.Pid
	t.Cleanup(func() { syscall.Kill(-group, syscall.SIGKILL) })
}

// waitChild waits at most d for cmd to end, and returns how it ended.
func waitChild(t *testing.T, cmd *exec.Cmd, d time.Duration) syscall.WaitStatus {
	t.Helper()
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		t.Fatalf("the child was still running after %v", d)
	}
	return cmd.ProcessState.Sys().(syscall.WaitStatus)
}

// endedBy reports whether a process that ended with ws was ended by sig,
// which a shell reports as the status 128 plus sig's number.
func endedBy(ws syscall.WaitStatus, sig syscall.Signal) bool {
	return ws.Signaled() && ws.Signal() == sig
}

// TestUpdateKeepsThePreviousBinaryWithStdoutClosed: with nothing reading its
// stdout any more, as in scterm update -y | head -1, update dies by SIGPIPE
// at the first line it prints after the download, which comes after both
// renames, so the previous binary is kept as .old all the same.
func TestUpdateKeepsThePreviousBinaryWithStdoutClosed(t *testing.T) {
	inUpdateChild()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { read.Close() })
	bin := fakeBinary("1.3.0")
	srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: bin, download: func(w http.ResponseWriter, r *http.Request) {
		read.Close() // nothing reads update's stdout from here on
		w.Write(bin)
	}})
	exe := installed(t, "1.2.3")
	os.WriteFile(exe+".old", fakeBinary("1.1.0"), 0o755)
	cmd := updateChild("TestUpdateKeepsThePreviousBinaryWithStdoutClosed", "update", exe, srv.URL+"/latest")
	cmd.Stdout = write
	startChild(t, cmd)
	write.Close()
	if ws := waitChild(t, cmd, 30*time.Second); !endedBy(ws, syscall.SIGPIPE) {
		t.Errorf("the update ended with %v, want SIGPIPE at the line after the download", cmd.ProcessState)
	}
	want := map[string]string{"scterm": string(bin), "scterm.old": string(fakeBinary("1.2.3"))}
	if got := files(t, filepath.Dir(exe)); !maps.Equal(got, want) {
		t.Errorf("left %v, want %v", got, want)
	}
}

// TestUpdateInterruptedDuringTheDownload: SIGINT, SIGTERM or SIGHUP during
// the download removes what has arrived, leaves the binary and the one kept
// from before as they were, says nothing more, and ends update as the signal
// would have.
func TestUpdateInterruptedDuringTheDownload(t *testing.T) {
	inUpdateChild()
	bin := fakeBinary("1.3.0")
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		arrived := make(chan struct{})
		srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: bin, download: func(w http.ResponseWriter, r *http.Request) {
			w.Write(bin[:10])
			w.(http.Flusher).Flush()
			close(arrived)
			select { // the rest never comes, so only the interruption ends the download in time
			case <-r.Context().Done():
			case <-time.After(10 * time.Minute): // far longer than the test waits after the signal
			}
		}})
		exe := installed(t, "1.2.3")
		os.WriteFile(exe+".old", fakeBinary("1.1.0"), 0o755)
		before := files(t, filepath.Dir(exe))
		cmd := updateChild("TestUpdateInterruptedDuringTheDownload", "update", exe, srv.URL+"/latest")
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		startChild(t, cmd)
		select {
		case <-arrived:
		case <-time.After(30 * time.Second):
			t.Fatalf("%v: the download did not start", sig)
		}
		cmd.Process.Signal(sig)
		if ws := waitChild(t, cmd, 30*time.Second); !endedBy(ws, sig) {
			t.Errorf("%v: the update ended with %v", sig, cmd.ProcessState)
		}
		if after := files(t, filepath.Dir(exe)); !maps.Equal(after, before) {
			t.Errorf("%v: the files went from %v to %v", sig, slices.Sorted(maps.Keys(before)), slices.Sorted(maps.Keys(after)))
		}
		if !strings.HasSuffix(stdout.String(), "Downloading scterm 1.3.0…\n") || stderr.String() != "" {
			t.Errorf("%v: printed %q, and %q on stderr", sig, stdout.String(), stderr.String())
		}
	}
}

// TestUpdateInterruptedWhileTheNewBinaryRuns: a signal while the new binary
// runs its version check stops it and removes it, from beside the binary or
// from TMPDIR where it waited for sudo, and update ends by the signal.
func TestUpdateInterruptedWhileTheNewBinaryRuns(t *testing.T) {
	inUpdateChild()
	// The new binary signals update, which runs it, and waits to be stopped.
	bin := []byte("#!/bin/sh\nkill -TERM $PPID\nexec sleep 20\n")
	for _, writable := range []bool{true, false} {
		if !writable && os.Geteuid() == 0 {
			t.Log("skipped the read-only case: root may write anywhere")
			continue
		}
		srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: bin})
		exe := installed(t, "1.2.3")
		dir, tmp := filepath.Dir(exe), t.TempDir()
		os.WriteFile(exe+".old", fakeBinary("1.1.0"), 0o755)
		before := files(t, dir)
		if !writable {
			os.Chmod(dir, 0o555)
			t.Cleanup(func() { os.Chmod(dir, 0o755) })
		}
		cmd := updateChild("TestUpdateInterruptedWhileTheNewBinaryRuns", "update", exe, srv.URL+"/latest")
		cmd.Env = append(cmd.Env, "TMPDIR="+tmp)
		startChild(t, cmd)
		if ws := waitChild(t, cmd, 10*time.Second); !endedBy(ws, syscall.SIGTERM) {
			t.Errorf("writable %v: the update ended with %v", writable, cmd.ProcessState)
		}
		if after := files(t, dir); !maps.Equal(after, before) {
			t.Errorf("writable %v: the files went from %v to %v", writable, slices.Sorted(maps.Keys(before)), slices.Sorted(maps.Keys(after)))
		}
		if left := files(t, tmp); len(left) != 0 {
			t.Errorf("writable %v: left %v in TMPDIR", writable, slices.Sorted(maps.Keys(left)))
		}
	}
}

// TestCtrlCAtTheUpdateQuestion: Ctrl+C at the question ends update at once,
// as it always has, with nothing downloaded.
func TestCtrlCAtTheUpdateQuestion(t *testing.T) {
	inUpdateChild()
	srv, downloads := serveRelease(t, fakeRelease{version: "1.3.0", binary: fakeBinary("1.3.0")})
	exe := installed(t, "1.2.3")
	before := files(t, filepath.Dir(exe))
	stdin, typing, err := os.Pipe() // the answer never comes
	if err != nil {
		t.Fatal(err)
	}
	stderr, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { typing.Close(); stderr.Close() })
	cmd := updateChild("TestCtrlCAtTheUpdateQuestion", "ask", exe, srv.URL+"/latest")
	cmd.Stdin, cmd.Stderr = stdin, w
	startChild(t, cmd)
	stdin.Close()
	w.Close()
	asked := make(chan string, 1)
	go func() {
		var got []byte
		buf := make([]byte, 256)
		for !bytes.HasSuffix(got, []byte("[Y/n] ")) {
			n, err := stderr.Read(buf)
			got = append(got, buf[:n]...)
			if err != nil {
				break
			}
		}
		asked <- string(got)
	}()
	select {
	case q := <-asked:
		if q != "Update scterm 1.2.3 to 1.3.0? [Y/n] " {
			t.Fatalf("stderr %q, want the question", q)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no question")
	}
	cmd.Process.Signal(syscall.SIGINT)
	if ws := waitChild(t, cmd, 10*time.Second); !endedBy(ws, syscall.SIGINT) {
		t.Errorf("the update ended with %v", cmd.ProcessState)
	}
	if n := downloads.Load(); n != 0 {
		t.Errorf("downloaded %d times", n)
	}
	if after := files(t, filepath.Dir(exe)); !maps.Equal(after, before) {
		t.Errorf("the files went from %v to %v", before, after)
	}
}

// TestUpdateUnderNohupIgnoresAHangUp: SIGHUP, which nohup starts update
// ignoring, is still ignored during the download, and the update goes on.
func TestUpdateUnderNohupIgnoresAHangUp(t *testing.T) {
	inUpdateChild()
	bin := fakeBinary("1.3.0")
	arrived, rest := make(chan struct{}), make(chan struct{})
	srv, _ := serveRelease(t, fakeRelease{version: "1.3.0", binary: bin, download: func(w http.ResponseWriter, r *http.Request) {
		w.Write(bin[:10])
		w.(http.Flusher).Flush()
		close(arrived)
		<-rest
		w.Write(bin[10:])
	}})
	exe := installed(t, "1.2.3")
	cmd := updateChild("TestUpdateUnderNohupIgnoresAHangUp", "update", exe, srv.URL+"/latest")
	cmd.Path, cmd.Args = "/bin/sh", append([]string{"sh", "-c", `trap "" HUP; exec "$0" "$@"`}, cmd.Args...)
	startChild(t, cmd)
	select {
	case <-arrived:
	case <-time.After(30 * time.Second):
		t.Fatal("the download did not start")
	}
	cmd.Process.Signal(syscall.SIGHUP)
	close(rest)
	if ws := waitChild(t, cmd, 30*time.Second); ws.Signaled() || ws.ExitStatus() != 0 {
		t.Errorf("the update ended with %v", cmd.ProcessState)
	}
	want := map[string]string{"scterm": string(bin), "scterm.old": string(fakeBinary("1.2.3"))}
	if got := files(t, filepath.Dir(exe)); !maps.Equal(got, want) {
		t.Errorf("left %v, want %v", got, want)
	}
}

// TestCopyBesideRemovesAPartialCopy: a copy that fails part way, here at a
// file size limit, as a full disk would stop it, leaves nothing behind.
func TestCopyBesideRemovesAPartialCopy(t *testing.T) {
	inUpdateChild()
	exe := filepath.Join(t.TempDir(), "scterm")
	os.WriteFile(exe, bytes.Repeat([]byte("scterm "), 10000), 0o755) // 70 kB
	cmd := updateChild("TestCopyBesideRemovesAPartialCopy", "copy", exe, "")
	var out strings.Builder
	cmd.Stdout = &out
	startChild(t, cmd)
	if ws := waitChild(t, cmd, 30*time.Second); ws.Signaled() || ws.ExitStatus() != 0 {
		t.Fatalf("the child ended with %v", cmd.ProcessState)
	}
	if !strings.Contains(out.String(), "file too large") {
		t.Errorf("copyBeside said %q, want it to have stopped at the limit", out.String())
	}
	if got := files(t, filepath.Dir(exe)); len(got) != 1 {
		t.Errorf("left %v", slices.Sorted(maps.Keys(got)))
	}
}

// TestStderrIsTerminal: progress is shown on stderr only when it is a
// terminal, not a file or a pipe.
func TestStderrIsTerminal(t *testing.T) {
	inUpdateChild()
	file, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stderrs := map[string]io.Writer{"a file": file, "a pipe": new(strings.Builder)}
	want := map[string]string{"a file": "false\n", "a pipe": "false\n"}
	if term, err := openTerminal(t); err != nil {
		t.Logf("no terminal to try: %v", err)
	} else {
		stderrs["a terminal"], want["a terminal"] = term, "true\n"
	}
	for name, stderr := range stderrs {
		cmd := updateChild("TestStderrIsTerminal", "terminal", "", "")
		var out strings.Builder
		cmd.Stdout, cmd.Stderr = &out, stderr
		startChild(t, cmd)
		waitChild(t, cmd, 30*time.Second)
		if out.String() != want[name] {
			t.Errorf("stderr is %s: %q, want %q", name, out.String(), want[name])
		}
	}
}

// openTerminal opens a pseudo-terminal for the length of the test, and
// returns its terminal end.
func openTerminal(t *testing.T) (*os.File, error) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { ptmx.Close() })
	var unlock, n uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		return nil, errno
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ptmx.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); errno != 0 {
		return nil, errno
	}
	pts, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { pts.Close() })
	return pts, nil
}
