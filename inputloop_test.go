package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// The input loop itself, for what depends on when input comes: it runs in a
// child process (this test binary again), reading a pipe as it reads the
// terminal, and the parent writes to the pipe with pauses in between.
// ---------------------------------------------------------------------------

// inputLoopChild is the child's side: the real input loop on stdin, with each
// event it sends printed on stdout, in hex, with the milliseconds since the
// one before (or since the start), after a line saying it is ready. Notices
// go to stderr, as they do before the terminal UI starts.
func inputLoopChild() {
	a := newTestApp(newController(&capConn{}))
	a.events = make(chan inputEvent, 4096)
	done := make(chan struct{})
	go func() {
		a.inputLoop()
		close(done)
	}()
	fmt.Println("ready")
	last := time.Now()
	show := func(ev inputEvent) {
		now := time.Now()
		fmt.Printf("event %x %d\n", ev.buf, now.Sub(last).Milliseconds())
		last = now
	}
	for {
		select {
		case ev := <-a.events:
			show(ev)
		case <-done:
			for len(a.events) > 0 {
				show(<-a.events)
			}
			os.Exit(0)
		}
	}
}

// TestInputLoop: a lone Esc waits 25 ms for what follows it and is then the
// Esc key, and an Esc with the start of a paste's start marker after it waits
// as long, then decodes as before; an Esc and a paste in one write are the Esc
// key and one paste; a paste whose end marker never comes gets a notice after
// about 2 s that Ctrl+C cancels it, Ctrl+C does, what comes with the Ctrl+C is
// dropped, and once input has stopped for a moment keys act again. A pause of
// 400 ms comes before the event marked as coming after one.
func TestInputLoop(t *testing.T) {
	if os.Getenv("SCTERM_TEST_INPUT_LOOP") != "" {
		inputLoopChild()
	}
	type write struct {
		bytes string
		then  time.Duration // the pause after it
	}
	const pause = 400 * time.Millisecond
	for _, tc := range []struct {
		name    string
		writes  []write
		events  []string
		late    int      // the event that comes after the pause; -1 for none
		notices []string // on stderr, in this order
	}{
		{"lone Esc", []write{{"\x1b", pause}, {"x", 0}}, []string{"\x1b", "x"}, 1, nil},
		{"Esc and the start of a start marker, then nothing", []write{{"\x1b\x1b[2", pause}, {"x", 0}},
			[]string{"\x1b\x1b", "[", "2", "x"}, 3, nil},
		{"Esc and a paste in one write", []write{{"\x1b\x1b[200~hi\x1b[201~", pause}, {"x", 0}},
			[]string{"\x1b", "\x1b[200~hi\x1b[201~", "x"}, 2, nil},
		{"a paste whose end marker never comes", []write{{"\x1b[200~abc", 2600 * time.Millisecond}, {"\x03q\r", 700 * time.Millisecond}, {"x", 0}},
			[]string{"x"}, -1, []string{"scterm: Ctrl+C cancels the unfinished paste\n", "scterm: paste cancelled by Ctrl+C\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(os.Args[0], "-test.run=^TestInputLoop$")
			cmd.Env = append(os.Environ(), "SCTERM_TEST_INPUT_LOOP=1")
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			lines := make(chan string, 64)
			go func() {
				sc := bufio.NewScanner(stdout)
				for sc.Scan() {
					lines <- sc.Text()
				}
				close(lines)
			}()
			select {
			case l := <-lines:
				if l != "ready" {
					t.Fatalf("the child said %q first", l)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the child did not start")
			}
			for _, w := range tc.writes {
				if _, err := io.WriteString(stdin, w.bytes); err != nil {
					t.Fatal(err)
				}
				time.Sleep(w.then)
			}
			stdin.Close()
			var out []string
			for l := range lines {
				out = append(out, l)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("child: %v (stderr %q)", err, stderr.String())
			}
			var events []string
			var gaps []int
			for _, line := range out {
				f := strings.Fields(line)
				if len(f) != 3 || f[0] != "event" {
					continue
				}
				b, _ := hex.DecodeString(f[1])
				ms, _ := strconv.Atoi(f[2])
				events, gaps = append(events, string(b)), append(gaps, ms)
			}
			if !slices.Equal(events, tc.events) {
				t.Errorf("events %q, want %q", events, tc.events)
			} else if tc.late >= 0 && gaps[tc.late] < int(pause.Milliseconds())/2 {
				t.Errorf("event %q came %d ms after the one before it; want it after the %v pause (gaps %v)",
					tc.events[tc.late], gaps[tc.late], pause, gaps)
			}
			rest := stderr.String()
			for _, n := range tc.notices {
				i := strings.Index(rest, n)
				if i < 0 {
					t.Errorf("no %q on stderr after what comes before it: %q", n, stderr.String())
					break
				}
				rest = rest[i+len(n):]
			}
			if tc.notices == nil && stderr.Len() != 0 {
				t.Errorf("stderr %q", stderr.String())
			}
		})
	}
}
