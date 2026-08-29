package main

import (
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

func closeAudioDump() {
	dumpMu.Lock()
	defer dumpMu.Unlock()
	if dumpF != nil {
		dumpF.Close()
		dumpF = nil
	}
}
