package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"testing"
	"unicode/utf8"
)

// A paste longer than one message must arrive as several inject-text
// messages, none over the scrcpy limit and none splitting a UTF-8 sequence
// (Android peers refuse oversize messages; the server would inject U+FFFD for
// a split one).
func TestInjectTextSplitsAtUTF8Boundaries(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := newController(a)
	text := strings.Repeat("a", 298) + "\U0001F600" + strings.Repeat("é", 200) + "end"

	done := make(chan error, 1)
	go func() {
		done <- c.injectText(text)
		a.Close()
	}()
	data, err := io.ReadAll(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	var got strings.Builder
	messages := 0
	for len(data) > 0 {
		if data[0] != ctrlInjectText || len(data) < 5 {
			t.Fatalf("message %d: unexpected header % x", messages, data[:min(5, len(data))])
		}
		n := int(binary.BigEndian.Uint32(data[1:5]))
		if n > injectTextMaxBytes || len(data) < 5+n {
			t.Fatalf("message %d: length %d (have %d bytes)", messages, n, len(data)-5)
		}
		chunk := data[5 : 5+n]
		if !utf8.Valid(chunk) {
			t.Fatalf("message %d splits a UTF-8 sequence: % x", messages, chunk)
		}
		got.Write(chunk)
		data = data[5+n:]
		messages++
	}
	if got.String() != text {
		t.Fatalf("reassembled text differs")
	}
	if messages != 3 {
		t.Fatalf("got %d messages, want 3 (298 bytes, then the emoji opens the next)", messages)
	}
}

// TestSetClipboardWire: a set-clipboard is laid out as the shared scrcpy
// fixture set_clipboard_paste says (protocol/fixtures/scrcpy_wire.json), with
// sequence 0, so that the device sends no acknowledgement, and the paste flag
// as asked.
func TestSetClipboardWire(t *testing.T) {
	var want []byte
	var text string
	for _, c := range loadWireFixtures(t).Control {
		if c.Name != "set_clipboard_paste" {
			continue
		}
		b, err := hex.DecodeString(c.Hex)
		if err != nil {
			t.Fatal(err)
		}
		want, text = b, c.Fields["text"].(string)
	}
	if len(want) < 10 {
		t.Fatal("no set_clipboard_paste fixture")
	}
	copy(want[1:9], make([]byte, 8)) // the fixture's sequence, as 0

	cc := &capConn{}
	c := newController(cc)
	if err := c.setClipboard(text, true); err != nil {
		t.Fatal(err)
	}
	if len(cc.captured) != 1 || !bytes.Equal(cc.captured[0], want) {
		t.Errorf("set-clipboard pasting %q wrote % x, want % x", text, cc.captured, want)
	}

	cc.captured = nil
	want[9] = 0
	if err := c.setClipboard(text, false); err != nil {
		t.Fatal(err)
	}
	if len(cc.captured) != 1 || !bytes.Equal(cc.captured[0], want) {
		t.Errorf("set-clipboard not pasting wrote % x, want % x", cc.captured, want)
	}
}

// TestSetClipboardLimit: text up to scrcpy's limit for one set-clipboard
// (1 << 18 less the 14-byte header) goes in one message; longer text is
// refused and nothing is written, since an Android peer ends the session over
// an oversize message. Without a socket nothing panics.
func TestSetClipboardLimit(t *testing.T) {
	const most = 1<<18 - 14
	cc := &capConn{}
	c := newController(cc)
	if err := c.setClipboard(strings.Repeat("a", most), true); err != nil || len(cc.captured) != 1 || len(cc.captured[0]) != 14+most {
		t.Errorf("%d bytes of text: err %v, %d messages; want one message", most, err, len(cc.captured))
	}
	cc.captured = nil
	if err := c.setClipboard(strings.Repeat("a", most+1), true); err == nil || len(cc.captured) != 0 {
		t.Errorf("%d bytes of text: err %v, %d messages; want it refused and nothing written", most+1, err, len(cc.captured))
	}

	var none *controller
	for _, noSocket := range []*controller{none, {}} {
		if err := noSocket.setClipboard("hi", true); err != errNoControl {
			t.Errorf("without a socket set-clipboard returned %v, want errNoControl", err)
		}
	}
}
