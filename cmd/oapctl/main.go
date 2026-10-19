// Command oapctl is the rewrite's agent CLI (working name until cutover). It is
// under construction; see docs/architecture/rewrite-plan.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "oapctl: not implemented yet; see docs/architecture/rewrite-plan.md")
	os.Exit(2)
}
