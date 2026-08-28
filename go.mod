module scterm

go 1.27.0

require (
	golang.org/x/sys v0.47.0
	scterm/engine v0.0.0
)

require github.com/gorilla/websocket v1.5.3 // indirect

replace scterm/engine => ./engine
