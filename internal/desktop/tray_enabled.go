//go:build tray

package desktop

import (
	"context"
	"fmt"
	"sync"

	"fyne.io/systray"
	"github.com/o25160526-pip/go-selfupdate-template/internal/updater"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// Run starts the native notification-area menu. systray owns the main thread,
// which is required by macOS AppKit.
func Run(ctx context.Context, engine *updater.Engine) error {
	var runErr error
	var once sync.Once
	systray.Run(func() {
		current, _ := version.Self()
		systray.SetTitle("Self Update")
		systray.SetTooltip(fmt.Sprintf("App %s", current))
		status := systray.AddMenuItem("Version: "+current.String(), "Current version")
		status.Disable()
		check := systray.AddMenuItem("Check for updates", "Check without installing")
		install := systray.AddMenuItem("Install latest", "Download, verify and install")
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "Close the tray app")
		go func() {
			for {
				select {
				case <-ctx.Done(): systray.Quit(); return
				case <-quit.ClickedCh: systray.Quit(); return
				case <-check.ClickedCh:
					result, err := engine.Update(ctx, updater.UpdateRequest{CheckOnly: true})
					if err != nil { status.SetTitle("Check failed: " + err.Error()) } else { status.SetTitle("Available: " + result.Installed.String()) }
				case <-install.ClickedCh:
					result, err := engine.Update(ctx, updater.UpdateRequest{})
					if err != nil { status.SetTitle("Update failed: " + err.Error()) } else { status.SetTitle("Installed: " + result.Installed.String()) }
				}
			}
		}()
	}, func() { once.Do(func() {}) })
	return runErr
}
