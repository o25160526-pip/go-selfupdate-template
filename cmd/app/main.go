// Command app is the template entrypoint.
//
// It stays this small on purpose. New capabilities are feature packages that
// register themselves, so this file should never need editing.
package main

import (
	"os"

	"github.com/o25160526-pip/go-selfupdate-template/internal/cli"

	// Blank import runs every feature's init(). Managed by `make new-feature`.
	_ "github.com/o25160526-pip/go-selfupdate-template/internal/features/all"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
