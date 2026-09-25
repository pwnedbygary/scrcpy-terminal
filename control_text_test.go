package main

import (
	"encoding/binary"
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
