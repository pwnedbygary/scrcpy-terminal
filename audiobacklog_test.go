package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// backlogPtsBase is the device's stamp, in microseconds, for what it sends at 0
// on the fake clock: the server's stamps are never 0.
const backlogPtsBase = 1_000_000

// unixPair is a connected pair of stream sockets. Like a TCP socket's, the
// bytes waiting in one can be counted (socketUnread), and what is written to
// the other is there to read as soon as the write returns.
func unixPair(t *testing.T) (a, b *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	conn := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "unixpair")
		defer f.Close()
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		return c.(*net.UnixConn)
	}
	a, b = conn(fds[0]), conn(fds[1])
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

// backlogLink runs the audio demuxer on a fake device and connection with one
// fake clock for both. The device sends a 20 ms packet every 20 ms from 0, and
// each arrives when the test says. The sink is a PCM tap that plays at the
// sound's rate: when waits is set, a write waits until the stream holds no
// more than 20 ms, as pa_simple_write does with scterm's 20 ms buffer; a
// muted sink's write, or the browser's, returns at once. The clock moves on
// there, and when the demuxer has read all that has arrived and waits for
// more. All of it runs on the demuxer's goroutine, so a run comes out the same
// every time and takes no real time.
type backlogLink struct {
	t       *testing.T
	dev     *net.UnixConn
	cli     *net.UnixConn
	s       *streamState
	now     time.Duration
	pkts    []linkPacket
	next    int // the first packet not yet delivered
	waits   bool
	playEnd time.Duration
	played  []linkPlayed
}

type linkPacket struct {
	arrive time.Duration
	wire   []byte
}

// linkPlayed is a packet the sink played: when the device sent it, and when it
// started to play.
type linkPlayed struct{ sent, start time.Duration }

// linkGap is sound left out, at a time on the fake clock.
type linkGap struct{ at, length time.Duration }

func (g linkGap) String() string { return fmt.Sprintf("%v at %v", g.length, g.at) }

// backlogConn is the demuxer's end of the link. A read that finds nothing
// waiting moves the clock on to the next arrival, or after the last packet
// gets the device's hang-up.
type backlogConn struct {
	*net.UnixConn
	l *backlogLink
}

func (c backlogConn) Read(p []byte) (int, error) {
	if socketUnread(rawConn(c.UnixConn)) == 0 {
		c.l.waitForDevice()
	}
	return c.UnixConn.Read(p)
}

func (l *backlogLink) waitForDevice() {
	if l.next == len(l.pkts) {
		l.dev.Close()
		return
	}
	l.now = max(l.now, l.pkts[l.next].arrive)
	l.deliver()
}

// deliver writes the packets that have arrived by now, in one write.
func (l *backlogLink) deliver() {
	var b []byte
	for ; l.next < len(l.pkts) && l.pkts[l.next].arrive <= l.now; l.next++ {
		b = append(b, l.pkts[l.next].wire...)
	}
	if len(b) == 0 {
		return
	}
	if _, err := l.dev.Write(b); err != nil {
		l.t.Fatalf("the device's write of %d bytes at %v: %v (a socket pair holds about 200 KB)", len(b), l.now, err)
	}
}

// play is the sink, as a PCM tap.
func (l *backlogLink) play(pcm []byte) {
	if l.waits {
		l.now = max(l.now, l.playEnd-20*time.Millisecond)
	}
	start := max(l.playEnd, l.now)
	l.playEnd = start + time.Duration(len(pcm)/4)*time.Second/48000
	sent := time.Duration(l.s.lastAudioPts.Load()-backlogPtsBase) * time.Microsecond
	l.played = append(l.played, linkPlayed{sent, start})
	l.deliver()
}

// runBacklog runs the demuxer with sink, on n packets of codec ("opus" or
// "raw") from a device whose packet sent at s arrives at arrive(s), and which
// hangs up after the last. The demuxer starts to read at start.
func runBacklog(t *testing.T, codec string, sink *audioSink, waits bool, start time.Duration, n int, arrive func(sent time.Duration) time.Duration) *backlogLink {
	t.Helper()
	l := newBacklogLink(t, codec, start, n, arrive)
	l.waits = waits
	l.run(sink, l.play)
	return l
}

// newBacklogLink is runBacklog's link, with what has arrived by start
// delivered, before the demuxer runs.
func newBacklogLink(t *testing.T, codec string, start time.Duration, n int, arrive func(sent time.Duration) time.Duration) *backlogLink {
	t.Helper()
	dev, cli := unixPair(t)
	dev.SetWriteDeadline(time.Now().Add(10 * time.Second)) // a full socket fails the test, not hangs it
	l := &backlogLink{t: t, dev: dev, cli: cli, now: start}
	l.pkts = append(l.pkts, linkPacket{0, []byte("opus")})
	l.pkts = append(l.pkts, linkPacket{0, wirePacket(1<<62, 0, []byte("OpusHead\x01\x02\x38\x01\x80\xbb\x00\x00\x00\x00\x00"))})
	payload := []byte{0xf8, 0xff, 0xfe} // 20 ms of silence
	if codec == "raw" {
		l.pkts = []linkPacket{{0, []byte("\x00raw")}}
		payload = make([]byte, 3840) // 20 ms of s16 stereo at 48 kHz
	}
	var last time.Duration
	for i := range n {
		sent := time.Duration(i) * 20 * time.Millisecond
		last = max(arrive(sent), last) // the connection keeps the order
		l.pkts = append(l.pkts, linkPacket{last, wirePacket(0, backlogPtsBase+sent.Microseconds(), payload)})
	}
	l.deliver()
	return l
}

// run runs the demuxer with sink, and with tap as the link's sink, until the
// device hangs up.
func (l *backlogLink) run(sink *audioSink, tap func([]byte)) {
	l.t.Helper()
	l.s = newStreamState(&session{deviceName: "Pixel 8", audio: backlogConn{l.cli, l}}, config{audio: true}, nil)
	l.s.addPCMTap(tap)
	if err := l.s.runAudio(sink); !errors.As(err, new(lostError)) {
		l.t.Fatalf("runAudio: %v, want the stream to end as the device hung up", err)
	}
}

// playingSink is a sink that plays here, unmuted: its writes reach no sound
// server, and the link's tap paces them.
func playingSink() *audioSink { return &audioSink{gain: 256, pct: 100} }

// steady: each packet arrives 5 ms after it is sent.
func steady(sent time.Duration) time.Duration { return sent + 5*time.Millisecond }

// stalled: the packets sent from at, for length, all arrive at its end, as both
// streams do over wireless adb after a stall; the others are steady.
func stalled(at, length time.Duration) func(time.Duration) time.Duration {
	return func(sent time.Duration) time.Duration {
		if sent >= at && sent < at+length {
			return steady(at + length)
		}
		return steady(sent)
	}
}

// refilledInOrder: as stalled, but the link then delivers what waited in order
// and at a steady rate, and has caught up spread after the stall.
func refilledInOrder(at, length, spread time.Duration) func(time.Duration) time.Duration {
	return func(sent time.Duration) time.Duration {
		if sent >= at && sent < at+length+spread {
			return steady(at + length + time.Duration(float64(spread)*float64(sent-at)/float64(length+spread)))
		}
		return steady(sent)
	}
}

// refilledStalledFirst: as stalled, but the packets sent during the stall
// arrive evenly over spread after it, and those sent meanwhile come in behind
// the last of them.
func refilledStalledFirst(at, length, spread time.Duration) func(time.Duration) time.Duration {
	return func(sent time.Duration) time.Duration {
		if sent >= at && sent < at+length {
			return steady(at + length + time.Duration(float64(spread)*float64(sent-at)/float64(length)))
		}
		return steady(sent)
	}
}

// inBursts: the packets arrive together at the moments given, each at the
// first moment at or after it would arrive steadily.
func inBursts(moments []time.Duration) func(time.Duration) time.Duration {
	return func(sent time.Duration) time.Duration {
		i := sort.Search(len(moments), func(i int) bool { return moments[i] >= steady(sent) })
		if i == len(moments) {
			return steady(sent)
		}
		return moments[i]
	}
}

// delayAt is how far behind the sound that plays at the time at is: when it
// started to play, less when the device sent it.
func (l *backlogLink) delayAt(at time.Duration) time.Duration {
	l.t.Helper()
	i := sort.Search(len(l.played), func(i int) bool { return l.played[i].start > at }) - 1
	if i < 0 {
		l.t.Fatalf("nothing played by %v", at)
	}
	return l.played[i].start - l.played[i].sent
}

// drops are the stretches of sound the sink never played, each at the time
// the sound after it started.
func (l *backlogLink) drops() []linkGap {
	var gaps []linkGap
	for i := 1; i < len(l.played); i++ {
		if d := l.played[i].sent - l.played[i-1].sent - 20*time.Millisecond; d > 0 {
			gaps = append(gaps, linkGap{l.played[i].start, d})
		}
	}
	return gaps
}

// silences are the times the sink ran dry, each at the time it did.
func (l *backlogLink) silences() []linkGap {
	var gaps []linkGap
	for i := 1; i < len(l.played); i++ {
		end := l.played[i-1].start + 20*time.Millisecond
		if d := l.played[i].start - end; d > 0 {
			gaps = append(gaps, linkGap{end, d})
		}
	}
	return gaps
}

// TestSoundCatchesUpAfterAStartDelay: the 585 ms of sound that waited when the
// demuxer read its first packet, as in three of the five runs on the phone,
// played late for the whole session before. Now, once a whole window of 2.5 s
// of sound has played with it queued, the oldest of it is dropped down to the
// margin of 150 ms: from then on the sound is about 185 ms behind (the margin,
// plus the packet the queue does not count, the transport's 5 ms and the
// sink's 20 ms), with nothing more dropped and no silence. The same with raw
// sound, whose packets are the samples, as with Opus, which is decoded.
func TestSoundCatchesUpAfterAStartDelay(t *testing.T) {
	for _, codec := range []string{"opus", "raw"} {
		l := runBacklog(t, codec, playingSink(), true, 585*time.Millisecond, 600, steady) // 12 s of sound
		if d := l.delayAt(3 * time.Second); d != 585*time.Millisecond {
			t.Errorf("%s: within the first window the sound is %v behind, want the start's 585ms", codec, d)
		}
		// The window ends at the packet sent at 2.5 s, which would play at
		// 3.085 s; from there on the sound is current.
		for _, at := range []time.Duration{3085 * time.Millisecond, 5 * time.Second, 12 * time.Second} {
			if d := l.delayAt(at); d < 150*time.Millisecond || d > 250*time.Millisecond {
				t.Errorf("%s: at %v the sound is %v behind, want the margin of 150ms and up to 100ms more", codec, at, d)
			}
		}
		if drops := l.drops(); len(drops) != 1 || drops[0].length < 300*time.Millisecond || drops[0].length > 450*time.Millisecond {
			t.Errorf("%s: dropped %v, want one drop of about 400ms", codec, drops)
		}
		if got, want := l.s.droppedSound(), l.played[len(l.played)-1].sent+20*time.Millisecond-time.Duration(len(l.played))*20*time.Millisecond; got != want {
			t.Errorf("%s: droppedSound says %v, and the sink missed %v", codec, got, want)
		}
		if s := l.silences(); len(s) != 0 {
			t.Errorf("%s: the sink ran dry: %v", codec, s)
		}
		t.Logf("%s: behind %v at 3 s, %v at 3.085 s, %v at 12 s; dropped %v", codec,
			l.delayAt(3*time.Second), l.delayAt(3085*time.Millisecond), l.delayAt(12*time.Second), l.drops())
	}
}

// TestSoundCatchesUpAfterAStall: both streams stop for 2.5 s, as they did over
// wireless adb with a video playing, and then come in a burst. The sink runs
// dry for the stall, which nothing here can help, and the sound resumes 2.5 s
// behind. Before, it stayed so. Now, once a whole window of 2.5 s of sound has
// played after the stall, the backlog is dropped down to the margin: within
// 3 s of the burst the sound is current again. A stall is no burst to keep a
// cushion for.
func TestSoundCatchesUpAfterAStall(t *testing.T) {
	const at, stall = 10130 * time.Millisecond, 2500 * time.Millisecond
	l := runBacklog(t, "opus", playingSink(), true, 0, 1500, stalled(at, stall)) // 30 s of sound
	burst := steady(at + stall)
	if d := l.delayAt(at - time.Second); d != 5*time.Millisecond {
		t.Errorf("before the stall the sound is %v behind, want the transport's 5ms", d)
	}
	if d := l.delayAt(burst + 500*time.Millisecond); d < stall-20*time.Millisecond {
		t.Errorf("just after the burst the sound is %v behind, want about the stall's %v", d, stall)
	}
	for _, at := range []time.Duration{burst + 3*time.Second, burst + 10*time.Second, 30 * time.Second} {
		if d := l.delayAt(at); d < 150*time.Millisecond || d > 250*time.Millisecond {
			t.Errorf("at %v the sound is %v behind, want the margin of 150ms and up to 100ms more", at, d)
		}
	}
	if drops := l.drops(); len(drops) != 1 || drops[0].length < 2200*time.Millisecond || drops[0].length > 2400*time.Millisecond {
		t.Errorf("dropped %v, want one drop of about 2.3s", drops)
	}
	if s := l.silences(); len(s) != 1 || s[0].length < stall-20*time.Millisecond || s[0].length > stall+20*time.Millisecond {
		t.Errorf("the sink ran dry %v, want once, for the stall", s)
	}
	t.Logf("behind: %v before, %v after the burst, %v 3 s after it; dropped %v; ran dry %v",
		l.delayAt(at-time.Second), l.delayAt(burst+500*time.Millisecond), l.delayAt(burst+3*time.Second), l.drops(), l.silences())
}

// TestAStallsBacklogGoesHoweverItsRefillComes: what waited out a stall need not
// come back at one moment: the link may take a second or two to catch up. Then
// the windows around the one that shows the stall each see part of its rise,
// which on its own looks like a burst's. None of it is kept as a cushion: for
// stalls of 1.5 to 6 s (those measured lasted 1 to 6 s), refills that take up
// to 2 s and come in two ways, and ten start times 50 ms apart across a
// segment, 10 s after the refill has come the sound is at most the margin
// plus 150 ms behind.
func TestAStallsBacklogGoesHoweverItsRefillComes(t *testing.T) {
	for _, way := range []struct {
		name   string
		arrive func(at, length, spread time.Duration) func(time.Duration) time.Duration
	}{
		{"caught up in order", refilledInOrder},
		{"the stalled sound first", refilledStalledFirst},
	} {
		var worst time.Duration
		for _, stall := range []time.Duration{1500 * time.Millisecond, 2500 * time.Millisecond, 4 * time.Second, 6 * time.Second} {
			for _, spread := range []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 1500 * time.Millisecond, 2 * time.Second} {
				var behind []string
				for off := time.Duration(0); off < 500*time.Millisecond; off += 50 * time.Millisecond {
					at := 6*time.Second + off
					l := runBacklog(t, "opus", playingSink(), true, 0, 1300, way.arrive(at, stall, spread)) // 26 s of sound
					d := l.delayAt(steady(at+stall+spread) + 10*time.Second)
					worst = max(worst, d)
					if d > 300*time.Millisecond {
						behind = append(behind, fmt.Sprintf("%v from %v", d, at))
					}
				}
				if len(behind) > 0 {
					t.Errorf("%s: a %v stall refilled over %v: 10 s after the refill the sound is still behind by %s; want at most 300ms",
						way.name, stall, spread, strings.Join(behind, ", "))
				}
			}
		}
		t.Logf("%s: at most %v behind 10 s after the refill", way.name, worst)
	}
}

// TestBurstsKeepTheirCushion: sound that comes in bursts about a second apart,
// as it did even at 2 Mbit/s, needs a cushion of about a second, or the sink
// runs dry before each burst. Here the demuxer starts with 2 s queued, and the
// bursts come every second for 5 s, then 0.9 and 1.1 s apart. Whatever is
// dropped of the start's backlog in the first windows, the queue keeps the
// cushion, with the margin for bursts a little further apart than the first
// window saw: the sink never runs dry, and after the first 5 s nothing is
// dropped. And it keeps no more: at 60 s the sound is at most the margin, the
// bursts' own swing of 1.1 s and 0.1 s more behind.
func TestBurstsKeepTheirCushion(t *testing.T) {
	var moments []time.Duration
	for k := 1; k <= 62; k++ {
		m := time.Duration(k) * time.Second
		if k > 5 && k%2 == 0 {
			m += 50 * time.Millisecond
		} else if k > 5 {
			m -= 50 * time.Millisecond
		}
		moments = append(moments, m)
	}
	l := runBacklog(t, "opus", playingSink(), true, 2*time.Second, 3000, inBursts(moments)) // 60 s of sound
	if s := l.silences(); len(s) != 0 {
		t.Errorf("the sink ran dry %d times: %v", len(s), s)
	}
	for _, d := range l.drops() {
		if d.at > 5*time.Second {
			t.Errorf("dropped %v at %v, after the first 5 s", d.length, d.at)
		}
	}
	if d := l.delayAt(60 * time.Second); d > 1350*time.Millisecond {
		t.Errorf("at 60 s the sound is %v behind, want at most 1.35s: the margin, the bursts' 1.1 s and 0.1 s", d)
	}
	if c := l.s.soundCushion(); c < time.Second || c > 1100*time.Millisecond {
		t.Errorf("the cushion kept is %v, want the bursts' swing, 1 to 1.1 s", c)
	}
	t.Logf("dropped %v; ran dry %v; behind %v at 4 s, %v at 60 s; cushion %v", l.drops(), l.silences(),
		l.delayAt(4*time.Second), l.delayAt(60*time.Second), l.s.soundCushion())
}

// TestABurstsCushionOutlastsTheWindow: sound that comes in small bursts, after
// a 0.3 s gap every second, apart from a larger gap and its burst every 20 s.
// The 1.5 s queued at the start goes after the first window, down to the
// margin and the small bursts' cushion, so the first large gap, 1 s, finds too
// little and the sink runs dry. From then on the queue keeps the cushion that
// burst needed, for 30 s of sound and so from one burst to the next: nothing
// more is dropped, and the next large gaps, 0.9 s, find the sound queued. The
// sound is never further behind than the margin, the largest swing of the
// bursts so far and 0.1 s more.
func TestABurstsCushionOutlastsTheWindow(t *testing.T) {
	arrive := func(sent time.Duration) time.Duration {
		for k, gap := range []time.Duration{time.Second, 900 * time.Millisecond, 900 * time.Millisecond} {
			end := time.Duration(k+1) * 20 * time.Second
			if sent >= end-gap && sent < end {
				return steady(end)
			}
		}
		if next := sent.Truncate(time.Second) + time.Second; sent >= next-300*time.Millisecond {
			return steady(next)
		}
		return steady(sent)
	}
	l := runBacklog(t, "opus", playingSink(), true, 1500*time.Millisecond, 3500, arrive) // 70 s of sound
	if d := l.delayAt(10 * time.Second); d > 550*time.Millisecond {
		t.Errorf("at 10 s the sound is %v behind, want at most 550ms: the margin, the small bursts' 0.3 s and 0.1 s", d)
	}
	if d := l.delayAt(70 * time.Second); d > 1250*time.Millisecond {
		t.Errorf("at 70 s the sound is %v behind, want at most 1.25s: the margin, the 1 s burst's swing and 0.1 s", d)
	}
	for _, d := range l.drops() {
		if d.at > 21*time.Second {
			t.Errorf("dropped %v at %v, after the first burst", d.length, d.at)
		}
	}
	for _, s := range l.silences() {
		if s.at > 21*time.Second {
			t.Errorf("the sink ran dry for %v at %v, after the first burst", s.length, s.at)
		}
	}
	t.Logf("dropped %v; ran dry %v; behind %v at 10 s, %v at 30 s, %v at 70 s", l.drops(), l.silences(),
		l.delayAt(10*time.Second), l.delayAt(30*time.Second), l.delayAt(70*time.Second))
}

// TestAWanderingDelayDropsNothingMore: the transport's delay goes from 45 to
// 5 ms and back every 5 s, as a Wi-Fi link's may. After the start's backlog
// has gone, the faster spells leave 40 ms more queued than the window's low
// point did before: the cushion for the 40 ms rise covers it, and so would
// the least drop of 100 ms. Nothing more is dropped, and the sink never runs
// dry.
func TestAWanderingDelayDropsNothingMore(t *testing.T) {
	l := runBacklog(t, "opus", playingSink(), true, 585*time.Millisecond, 2000, func(sent time.Duration) time.Duration {
		if sent/(5*time.Second)%2 == 0 {
			return steady(sent) + 40*time.Millisecond
		}
		return steady(sent)
	}) // 40 s of sound
	if drops := l.drops(); len(drops) != 1 || drops[0].at > 5*time.Second {
		t.Errorf("dropped %v, want the start's backlog only", drops)
	}
	if s := l.silences(); len(s) != 0 {
		t.Errorf("the sink ran dry: %v", s)
	}
	t.Logf("dropped %v; behind %v at 4 s, %v at 9 s, %v at 40 s", l.drops(),
		l.delayAt(4*time.Second), l.delayAt(9*time.Second), l.delayAt(40*time.Second))
}

// TestADriftIsDroppedInSteps: the device sends its sound 0.2% faster than the
// sink plays it (far more than two clocks drift apart), so 2 ms more is queued
// every second. After the start's backlog, the drift is dropped in steps of
// at least 100 ms, the least drop, rather than a packet at a time.
func TestADriftIsDroppedInSteps(t *testing.T) {
	l := runBacklog(t, "opus", playingSink(), true, 585*time.Millisecond, 6000, func(sent time.Duration) time.Duration {
		return steady(sent - sent/500)
	}) // 120 s of sound
	drops := l.drops()
	if len(drops) < 2 {
		t.Errorf("dropped %v, want the start's backlog and the drift", drops)
	}
	for _, d := range drops {
		if d.length < 100*time.Millisecond {
			t.Errorf("dropped %v at %v, less than the least drop of 100ms", d.length, d.at)
		}
	}
	if s := l.silences(); len(s) != 0 {
		t.Errorf("the sink ran dry: %v", s)
	}
	t.Logf("dropped %v", drops)
}

// TestSteadySoundIsNeverDropped: sound that arrives 5 to 45 ms after it is
// sent, for a minute, never has enough queued to drop any.
func TestSteadySoundIsNeverDropped(t *testing.T) {
	l := runBacklog(t, "opus", playingSink(), true, 0, 3000, func(sent time.Duration) time.Duration {
		return steady(sent) + time.Duration(sent/(20*time.Millisecond)*7%5)*10*time.Millisecond
	})
	if drops := l.drops(); len(drops) != 0 || len(l.played) != 3000 || l.s.droppedSound() != 0 {
		t.Errorf("played %d of 3000 packets, dropped %v (droppedSound %v)", len(l.played), drops, l.s.droppedSound())
	}
}

// TestSoundThatDoesNotWaitIsNeverDropped: a muted sink drops the sound at once,
// and the sink of a browser-only session plays none, so the demuxer reads the
// sound as it comes, and nothing queues. All of it goes to the PCM taps, which
// feed the browsers, as before: here after a start delay of 1 s and a stall of
// 4 s, which would leave a backlog behind a sink that waits.
func TestSoundThatDoesNotWaitIsNeverDropped(t *testing.T) {
	muted := playingSink()
	muted.setGain(0)
	for _, tc := range []struct {
		name string
		sink *audioSink
	}{
		{"muted", muted},
		{"a browser-only session's sink", browserOnlySink()},
	} {
		l := runBacklog(t, "opus", tc.sink, false, time.Second, 1000, stalled(8*time.Second, 4*time.Second)) // 20 s of sound
		if drops := l.drops(); len(drops) != 0 || len(l.played) != 1000 || l.s.droppedSound() != 0 {
			t.Errorf("%s: the taps got %d of 1000 packets, dropped %v (droppedSound %v)", tc.name, len(l.played), drops, l.s.droppedSound())
		}
	}
}

// TestAMuteStartsTheWindowOver: a muted sink drops the sound at once, so the
// demuxer reads it as it comes and what was queued goes. The window's low
// points from before the mute say nothing about the queue after it, so the
// window starts over at the unmute. Muted 2 s into a 585 ms start delay, for
// 3 s: nothing is dropped, and after the unmute the sound is current. Muted
// for 1 s just before the first window would end, with 1.5 s queued, while
// the link stalls for 0.8 s: the stall's burst comes as the sound is unmuted,
// and the next bursts come 0.6 s apart. The new window sees them and keeps
// their cushion: nothing is dropped, and after the unmute the sink never runs
// dry.
func TestAMuteStartsTheWindowOver(t *testing.T) {
	afterStall := func(sent time.Duration) time.Duration {
		const stall, end, every = 4 * time.Second, 4800 * time.Millisecond, 600 * time.Millisecond
		switch {
		case sent >= end:
			return steady(end + ((sent-end)/every+1)*every)
		case sent >= stall:
			return steady(end)
		}
		return steady(sent)
	}
	for _, tc := range []struct {
		name             string
		start            time.Duration
		arrive           func(time.Duration) time.Duration
		muteAt, unmuteAt time.Duration
		current          bool // after the unmute, the sound is as late as the transport
	}{
		{"muted 2 s into a 585 ms start delay, for 3 s", 585 * time.Millisecond, steady, 2 * time.Second, 5 * time.Second, true},
		{"muted with 1.5 s queued while the link stalls", 1500 * time.Millisecond, afterStall, 3800 * time.Millisecond, 4800 * time.Millisecond, false},
	} {
		sink := playingSink()
		l := newBacklogLink(t, "opus", tc.start, 1000, tc.arrive) // 20 s of sound
		l.waits = true
		muted, unmuted := false, false
		l.run(sink, func(pcm []byte) {
			if !muted && l.now >= tc.muteAt {
				sink.setGain(0)
				l.waits, muted = false, true
			}
			if muted && !unmuted && l.now >= tc.unmuteAt {
				sink.setGain(100)
				l.waits, unmuted = true, true
				l.playEnd = l.now
			}
			if muted && !unmuted {
				l.deliver() // nobody hears it
				return
			}
			l.play(pcm)
		})
		if d := l.s.droppedSound(); d != 0 {
			t.Errorf("%s: dropped %v", tc.name, d)
		}
		for _, s := range l.silences() {
			if s.at > tc.unmuteAt {
				t.Errorf("%s: the sink ran dry for %v at %v, after the unmute", tc.name, s.length, s.at)
			}
		}
		if d := l.delayAt(tc.unmuteAt + 100*time.Millisecond); tc.current && d != 5*time.Millisecond {
			t.Errorf("%s: just after the unmute the sound is %v behind, want the transport's 5ms", tc.name, d)
		}
		t.Logf("%s: droppedSound %v; behind %v just after the unmute, %v at 20 s", tc.name, l.s.droppedSound(),
			l.delayAt(tc.unmuteAt+100*time.Millisecond), l.delayAt(20*time.Second))
	}
}

// TestACushionLastsThirtySecondsOfSound: the cushion a burst earns lasts 30 s
// of the device's stamps, whether or not scterm measured the sound meanwhile.
// Here a 1 s gap and its burst earn about a second of cushion, then the sound
// is muted for 40 s. After the unmute, a 2.5 s stall's backlog goes down to
// the margin, not to the old burst's cushion.
func TestACushionLastsThirtySecondsOfSound(t *testing.T) {
	arrive := func(sent time.Duration) time.Duration {
		switch {
		case sent >= 4*time.Second && sent < 5*time.Second:
			return steady(5 * time.Second)
		case sent >= 52*time.Second && sent < 54500*time.Millisecond:
			return steady(54500 * time.Millisecond)
		}
		return steady(sent)
	}
	sink := playingSink()
	l := newBacklogLink(t, "opus", 0, 3500, arrive) // 70 s of sound
	l.waits = true
	var earned time.Duration
	muted, unmuted := false, false
	l.run(sink, func(pcm []byte) {
		if !muted && l.now >= 8*time.Second {
			earned = l.s.soundCushion()
			sink.setGain(0)
			l.waits, muted = false, true
		}
		if muted && !unmuted && l.now >= 48*time.Second {
			sink.setGain(100)
			l.waits, unmuted = true, true
			l.playEnd = l.now
		}
		if muted && !unmuted {
			l.deliver() // nobody hears it
			return
		}
		l.play(pcm)
	})
	if earned < 900*time.Millisecond {
		t.Errorf("the 1 s burst earned a cushion of %v, want about 1s", earned)
	}
	if d := l.delayAt(65 * time.Second); d > 300*time.Millisecond {
		t.Errorf("10 s after the stall the sound is %v behind, want at most 300ms: the margin and 150ms", d)
	}
	t.Logf("cushion %v before the mute, %v at the end; behind %v at 65 s; dropped %v", earned, l.s.soundCushion(),
		l.delayAt(65*time.Second), l.s.droppedSound())
}

// TestADropNeverTakesTheLastPacketQueued: a drop that runs out of queued sound
// before its end stops there, and the packet that has nothing behind it
// plays: a drop never waits for sound to skip. Here 1 s stays queued for the
// window and a drop begins, but the stamps of the packets behind run out
// after 100 ms.
func TestADropNeverTakesTheLastPacketQueued(t *testing.T) {
	var b audioBacklog
	pts := time.Second
	for ; !b.skip(pts, time.Second); pts += 20 * time.Millisecond {
		if pts > 10*time.Second {
			t.Fatal("1 s queued for 9 s was never dropped")
		}
	}
	for range 4 {
		pts += 20 * time.Millisecond
		if !b.skip(pts, 100*time.Millisecond) {
			t.Fatalf("the drop stopped at %v, with 100ms still queued", pts)
		}
	}
	pts += 20 * time.Millisecond
	if b.skip(pts, 0) {
		t.Error("the drop took the last packet queued")
	}
	if got := time.Duration(b.dropped.Load()); got != 100*time.Millisecond {
		t.Errorf("dropped %v, want the 5 packets skipped, 100ms", got)
	}
}

// tcpPair is a connected TCP pair on loopback, as adb's tunnel gives scterm:
// the device's end and scterm's.
func tcpPair(t *testing.T) (device, client net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	device, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client = <-accepted
	if client == nil {
		t.Fatal("no connection accepted")
	}
	t.Cleanup(func() { device.Close(); client.Close() })
	return device, client
}

// waitUnread waits until n bytes wait in c's socket: loopback TCP hands them
// over a moment after the write.
func waitUnread(t *testing.T, c net.Conn, n int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); socketUnread(rawConn(c)) != n; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d bytes wait in the socket, want %d", socketUnread(rawConn(c)), n)
		}
	}
}

// TestAudioQueuedCountsTheSocket: the sound queued behind a packet is measured
// by the stamps of the whole media packets after it, both those in the
// demuxer's buffer and those still in the socket, over TCP as adb's tunnel
// delivers them. A setup packet and a packet not yet whole do not count. On
// a connection whose socket cannot be read, such as a peer session's TLS,
// the buffer is all there is.
func TestAudioQueuedCountsTheSocket(t *testing.T) {
	device, client := tcpPair(t)
	payload := make([]byte, 320) // about an Opus packet at 128 kbit/s
	const wire = 12 + 320
	var sent []byte
	for i := range 10 {
		sent = append(sent, wirePacket(0, 1_000_000+int64(i)*20_000, payload)...)
	}
	device.Write(sent)
	waitUnread(t, client, 10*wire)
	r := bufio.NewReaderSize(client, 1<<20)
	if _, err := io.ReadFull(r, make([]byte, wire)); err != nil {
		t.Fatal(err)
	}
	rc := rawConn(client)
	if got := audioQueued(r, rc, 1_000_000); got != 180*time.Millisecond {
		t.Errorf("9 packets in the buffer: %v queued, want 180ms", got)
	}
	sent = wirePacket(1<<62, 0, []byte("OpusHead"))
	for i := range 5 {
		sent = append(sent, wirePacket(0, 1_200_000+int64(i)*20_000, payload)...)
	}
	sent = append(sent, wirePacket(0, 1_300_000, payload)[:100]...)
	device.Write(sent)
	waitUnread(t, client, len(sent))
	if got := audioQueued(r, rc, 1_000_000); got != 280*time.Millisecond {
		t.Errorf("9 packets in the buffer and 5 more in the socket: %v queued, want 280ms", got)
	}
	if r.Buffered() != 9*wire+len(sent) || socketUnread(rc) != 0 {
		t.Errorf("%d bytes in the buffer and %d in the socket, want all %d in the buffer", r.Buffered(), socketUnread(rc), 9*wire+len(sent))
	}

	pipe, peer := net.Pipe()
	t.Cleanup(func() { pipe.Close(); peer.Close() })
	if rawConn(pipe) != nil || rawConn(tls.Client(client, &tls.Config{})) != nil {
		t.Error("a pipe's or a TLS connection's socket can be read")
	}
	go peer.Write(append(wirePacket(0, 1_000_000, payload), wirePacket(0, 1_020_000, payload)...))
	r = bufio.NewReaderSize(pipe, 1<<20)
	if _, err := io.ReadFull(r, make([]byte, wire)); err != nil {
		t.Fatal(err)
	}
	if got := audioQueued(r, nil, 1_000_000); got != 20*time.Millisecond {
		t.Errorf("a pipe: %v queued, want the 20ms in the buffer", got)
	}
}
