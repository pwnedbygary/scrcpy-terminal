package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestDumpAudioWritesThePacketsAsSent: --dump-audio writes each audio packet
// from the device after its 4-byte size, as the device sent it, before the
// local volume scales the samples for the speakers. With --audio-codec raw
// the packets are the PCM itself; an encoded stream's config packet is not
// audio and is left out.
func TestDumpAudioWritesThePacketsAsSent(t *testing.T) {
	for _, tc := range []struct {
		codec   string
		config  []byte // a config packet ahead of the audio, or nil
		packets [][]byte
	}{
		{"raw", nil, [][]byte{{0x10, 0x00, 0x20, 0x00, 0x30, 0x00, 0x40, 0x00}, {0x00, 0x40, 0x00, 0x30}}},
		// OpusHead, then a 20 ms Opus frame of silence.
		{"opus", []byte("OpusHead\x01\x02\x38\x01\x80\xbb\x00\x00\x00\x00\x00"), [][]byte{{0xf8, 0xff, 0xfe}}},
	} {
		path := filepath.Join(t.TempDir(), "audio.dump")
		id := []byte(tc.codec)
		if len(id) < 4 {
			id = append([]byte{0}, id...) // "raw" is sent as 00 'r' 'a' 'w'
		}
		sent := [][]byte{id}
		if tc.config != nil {
			header := binary.BigEndian.AppendUint64(nil, 1<<62) // the config flag
			sent = append(sent, binary.BigEndian.AppendUint32(header, uint32(len(tc.config))), tc.config)
		}
		var want []byte
		for i, p := range tc.packets {
			header := binary.BigEndian.AppendUint64(nil, uint64(i)*20000) // pts, in microseconds
			sent = append(sent, binary.BigEndian.AppendUint32(header, uint32(len(p))), p)
			want = append(binary.BigEndian.AppendUint32(want, uint32(len(p))), p...)
		}
		cfg := config{audio: true, audioCodec: tc.codec, audioDump: path}
		s := newStreamState(&session{deviceName: "Pixel 8", audio: deviceStream(t, sent...)}, cfg, nil)
		err := s.runAudio(&audioSink{gain: 128, pct: 50}) // half volume scales the samples in place
		closeAudioDump()
		if !errors.As(err, new(lostError)) {
			t.Errorf("%s: runAudio: %v, want the stream to end as the device hung up", tc.codec, err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
			t.Errorf("%s: the dump holds % x, want % x", tc.codec, got, want)
		}
	}
}
