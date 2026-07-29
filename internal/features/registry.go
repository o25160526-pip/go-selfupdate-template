// Package features provides extension points for template applications.
package features

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// MenuItem is shared by CLI and tray frontends.
type MenuItem struct {
	Title  string
	Action func(context.Context) error
}

// Feature adds optional behavior without changing the updater or main package.
type Feature interface {
	ID() string
	Start(context.Context) error
	MenuItems() []MenuItem
}

var registry = struct {
	sync.RWMutex
	items map[string]Feature
}{items: make(map[string]Feature)}

// Register adds a feature. Duplicate or empty IDs are programmer errors.
func Register(f Feature) {
	if f == nil || f.ID() == "" {
		panic("features: nil feature or empty ID")
	}
	registry.Lock()
	defer registry.Unlock()
	if _, exists := registry.items[f.ID()]; exists {
		panic(fmt.Sprintf("features: duplicate ID %q", f.ID()))
	}
	registry.items[f.ID()] = f
}

// All returns a stable snapshot sorted by ID.
func All() []Feature {
	registry.RLock()
	defer registry.RUnlock()
	ids := make([]string, 0, len(registry.items))
	for id := range registry.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Feature, 0, len(ids))
	for _, id := range ids {
		out = append(out, registry.items[id])
	}
	return out
}

// StartAll starts registered features until the first failure.
func StartAll(ctx context.Context) error {
	for _, f := range All() {
		if err := f.Start(ctx); err != nil {
			return fmt.Errorf("start feature %s: %w", f.ID(), err)
		}
	}
	return nil
}

// MenuItems flattens all feature-contributed menu items.
func MenuItems() []MenuItem {
	var out []MenuItem
	for _, f := range All() {
		out = append(out, f.MenuItems()...)
	}
	return out
}
