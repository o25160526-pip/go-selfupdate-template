package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// State is small persistent memory between runs.
type State struct {
	LastCheck   time.Time       `json:"last_check,omitempty"`
	LastUpdate  time.Time       `json:"last_update,omitempty"`
	LastVersion version.Version `json:"last_version,omitempty"`
	// FailedVersion stops an automatic update from retrying a build that has
	// already been rolled back once. Without it, a broken release turns into an
	// update-crash-update loop on every client.
	FailedVersion version.Version `json:"failed_version,omitempty"`
	// SkipVersion is what "remind me later about this one" writes.
	SkipVersion version.Version `json:"skip_version,omitempty"`

	dir string `json:"-"`
}

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

func LoadState(dir string) *State {
	s := &State{dir: dir}
	b, err := os.ReadFile(statePath(dir))
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, s)
	s.dir = dir
	return s
}

func (s *State) Save() error {
	if s == nil || s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := statePath(s.dir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
