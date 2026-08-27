module scterm

go 1.27.0

require (
	golang.org/x/sys v0.47.0
	scterm/engine v0.0.0
)

replace scterm/engine => ./engine
