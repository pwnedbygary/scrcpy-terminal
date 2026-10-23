package main

import (
	"encoding/binary"
	"os"
	"sync"
)

var dumpMu sync.Mutex
var dumpF *os.File

// audioDumpFile lazily opens the raw-packet dump file (audio_dump config).
func audioDumpFile(path string) *os.File {
	if path == "" {
		return nil
	}
	dumpMu.Lock()
	defer dumpMu.Unlock()
	if dumpF == nil {
		f, err := os.Create(path)
		if err != nil {
			return nil
		}
		dumpF = f
	}
	return dumpF
}

// dumpAudioPacket writes one packet from the device to the dump file at path,
// after its 4-byte big-endian size, for offline analysis.
func dumpAudioPacket(path string, payload []byte) {
	f := audioDumpFile(path)
	if f == nil {
		return
	}
	var sz [4]byte
	binary.BigEndian.PutUint32(sz[:], uint32(len(payload)))
	f.Write(sz[:])
	f.Write(payload)
}

func closeAudioDump() {
	dumpMu.Lock()
	defer dumpMu.Unlock()
	if dumpF != nil {
		dumpF.Close()
		dumpF = nil
	}
}
