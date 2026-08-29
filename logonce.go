package main

import (
	"fmt"
	"sync"
)

var logOnceMu sync.Mutex
var logOnceSeen = map[string]bool{}

// logOnce prints s to stderr only the first time per unique s.
func logOnce(s string) {
	logOnceMu.Lock()
	defer logOnceMu.Unlock()
	if logOnceSeen[s] {
		return
	}
	logOnceSeen[s] = true
	fmt.Fprintf(stderrWriter(), "%s", s)
}
