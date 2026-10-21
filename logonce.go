package main

import (
	"sync"
)

var logOnceMu sync.Mutex
var logOnceSeen = map[string]bool{}

// logOnce reports s (see notice) only the first time per unique s.
func logOnce(s string) {
	logOnceMu.Lock()
	defer logOnceMu.Unlock()
	if logOnceSeen[s] {
		return
	}
	logOnceSeen[s] = true
	notice(s)
}
