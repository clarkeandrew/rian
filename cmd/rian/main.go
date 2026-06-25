// Command rian is a tiny, Flyway-compatible database migration runner.
//
// This is an early-development entrypoint; migration commands (migrate, info,
// validate, baseline, repair) are not wired up yet.
package main

import (
	"fmt"
	"os"
)

// version is overridden at build time via -ldflags by the release pipeline.
var version = "0.0.0-dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println("rian", version)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "rian: commands not yet implemented (early development)")
	fmt.Fprintln(os.Stderr, "usage: rian version")
	os.Exit(2)
}
