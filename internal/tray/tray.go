// Package tray is the system tray surface.
//
// The interface is deliberately separate from any implementation so the default
// build stays dependency free. A real tray needs CGO plus
// libayatana-appindicator3-dev on Linux, which would otherwise destroy plain
// cross-compilation for all six targets.
//
//	default build (no tray): go build ./cmd/app
//	with a tray:             go build -tags tray ./cmd/app
package tray

import "context"

// Status drives the icon and the tooltip.
type Status int

const (
	StatusOK Status = iota
	StatusUpdateAvailable
	StatusWorking
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusUpdateAvailable:
		return "update available"
	case StatusWorking:
		return "working"
	case StatusError:
		return "error"
	default:
		return "up to date"
	}
}

// Item is one entry in the tray menu.
type Item struct {
	Label     string
	Tooltip   string
	Disabled  bool
	Separator bool
	OnClick   func()
}

// Controller is what the app talks to. Every method is safe to call even when
// the tray is unsupported, so callers never branch on the platform.
type Controller interface {
	Supported() bool
	SetItems(items []Item)
	SetStatus(s Status, tooltip string)
	Notify(title, body string)
	// Run blocks until ctx is cancelled or the user quits.
	Run(ctx context.Context) error
}
