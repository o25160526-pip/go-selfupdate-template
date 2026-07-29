// Package all blank-imports every feature so their init() runs.
//
// `make new-feature NAME=x` appends to this file. Nothing else needs editing,
// which is the point: main.go stays untouched as the app grows.
package all

import (
	_ "github.com/o25160526-pip/go-selfupdate-template/internal/features/diagnostics"
)
