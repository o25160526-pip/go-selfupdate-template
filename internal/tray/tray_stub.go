//go:build !tray

package tray

import (
	"context"
	"fmt"
	"os"
)

// stub is used for headless builds and on hosts without a tray. It reports
// Supported() == false and logs status changes instead of drawing anything.
type stub struct{ app string }

// New returns the tray controller for this build.
func New(app string) Controller { return &stub{app: app} }

func (s *stub) Supported() bool { return false }

func (s *stub) SetItems([]Item) {}

func (s *stub) Notify(title, body string) {
	fmt.Fprintf(os.Stderr, "%s: %s: %s\n", s.app, title, body)
}

func (s *stub) SetStatus(st Status, tooltip string) {
	fmt.Fprintf(os.Stderr, "%s: status %s: %s\n", s.app, st, tooltip)
}

func (s *stub) Run(ctx context.Context) error {
	return fmt.Errorf("this build has no system tray: rebuild with `-tags tray`, or run `%s menu`", s.app)
}
