// Package features is the extension point of this template.
//
// A new capability is a package that registers itself in init(). It then gets a
// CLI verb, a menu entry and a tray entry for free, and cmd/app/main.go never
// changes. That single rule is what makes this repo a template instead of one
// app that happens to update itself.
package features

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// MenuItem is an action a feature contributes to the tray and the menu.
type MenuItem struct {
	Label   string
	Tooltip string
	Run     func(ctx context.Context) error
}

// Feature is the contract every add-on implements.
type Feature interface {
	// ID is the CLI verb, so keep it a single lowercase word.
	ID() string
	Summary() string
	// Run handles `app <id> [args...]`.
	Run(ctx context.Context, args []string) error
	// MenuItems may return nil for features with no interactive surface.
	MenuItems() []MenuItem
}

var (
	mu       sync.RWMutex
	registry = map[string]Feature{}
)

// Register adds a feature. A duplicate ID panics at startup instead of
// silently shadowing, because that bug is invisible at runtime.
func Register(f Feature) {
	mu.Lock()
	defer mu.Unlock()
	id := f.ID()
	if id == "" {
		panic("features: a feature registered with an empty ID")
	}
	if _, exists := registry[id]; exists {
		panic(fmt.Sprintf("features: duplicate feature ID %q", id))
	}
	registry[id] = f
}

func Lookup(id string) (Feature, bool) {
	mu.RLock()
	defer mu.RUnlock()
	f, ok := registry[id]
	return f, ok
}

// All returns every feature, sorted by ID for stable help output.
func All() []Feature {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Feature, 0, len(registry))
	for _, f := range registry {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// MenuItems collects the contributions of every registered feature.
func MenuItems() []MenuItem {
	var out []MenuItem
	for _, f := range All() {
		out = append(out, f.MenuItems()...)
	}
	return out
}
