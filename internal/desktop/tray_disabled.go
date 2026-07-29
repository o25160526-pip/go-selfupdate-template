//go:build !tray

package desktop

import (
	"context"
	"fmt"

	"github.com/o25160526-pip/go-selfupdate-template/internal/updater"
)

// Run explains how to opt into the desktop dependency on headless builds.
func Run(context.Context, *updater.Engine) error {
	return fmt.Errorf("system tray is not included in this headless build; rebuild with -tags tray")
}
