package features

import (
	"context"
	"testing"
)

type stub struct{ id string }

func (s stub) ID() string                          { return s.id }
func (s stub) Summary() string                     { return "stub" }
func (s stub) Run(context.Context, []string) error { return nil }
func (s stub) MenuItems() []MenuItem               { return []MenuItem{{Label: s.id}} }

func reset() {
	mu.Lock()
	registry = map[string]Feature{}
	mu.Unlock()
}

func TestRegisterAndLookup(t *testing.T) {
	reset()
	Register(stub{"beta"})
	Register(stub{"alpha"})

	if _, ok := Lookup("alpha"); !ok {
		t.Fatal("alpha not found")
	}
	all := All()
	if len(all) != 2 || all[0].ID() != "alpha" {
		t.Fatalf("expected sorted order, got %v", all)
	}
	if len(MenuItems()) != 2 {
		t.Fatal("menu items were not collected")
	}
}

func TestDuplicateIDPanics(t *testing.T) {
	reset()
	defer func() {
		if recover() == nil {
			t.Fatal("a duplicate ID must panic at startup, not shadow silently")
		}
	}()
	Register(stub{"dup"})
	Register(stub{"dup"})
}
