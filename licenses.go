package main

import (
	_ "embed"
	"fmt"
)

// scterm's license, that of the scrcpy server it bundles and that of the Go
// code built into it. scterm update downloads only the binary, so the binary
// carries them itself.
var (
	//go:embed LICENSE
	sctermLicense string
	//go:embed third_party/LICENSE.scrcpy
	scrcpyLicense string
	//go:embed third_party/LICENSE.golang
	goLicense string
)

// runLicensesCommand runs `scterm licenses`, and reports whether args named it.
func runLicensesCommand(args []string) bool {
	if len(args) == 0 || args[0] != "licenses" {
		return false
	}
	if len(args) > 1 {
		if isHelpArg(args[1]) {
			fmt.Print(licensesHelp)
			return true
		}
		usageError("usage: scterm licenses (it takes no arguments)")
	}
	fmt.Printf("scterm is released under the MIT License:\n\n%s\n", sctermLicense)
	fmt.Printf("The scrcpy server that scterm bundles is licensed under the Apache License 2.0:\n\n%s", scrcpyLicense)
	fmt.Print(goLicense)
	return true
}

const licensesHelp = `usage: scterm licenses

Prints the licenses of scterm (the MIT License), of the scrcpy server
it bundles (the Apache License 2.0) and of the Go code built into it
(the BSD 3-Clause License).
`
