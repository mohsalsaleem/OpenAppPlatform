package main

import "fmt"

// Set at release (make release): the version, and the oapd image this
// oapctl installs, pinned by digest.
var (
	Version   = "dev"
	OapdImage = ""
)

// versionCmd: version (also --version).
func (a *app) versionCmd() int {
	fmt.Fprintf(a.out, "oapctl %s\n", Version)
	if OapdImage != "" {
		fmt.Fprintf(a.out, "installs oapd %s\n", OapdImage)
	} else {
		fmt.Fprintln(a.out, "development build: host install needs --image")
	}
	return 0
}
