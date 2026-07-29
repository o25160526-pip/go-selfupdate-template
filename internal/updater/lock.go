package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLocked means another update is already running.
var ErrLocked = errors.New("another update is already in progress")

// Lock is a single-writer lock around the update process.
//
// Required because the tray, a scheduled auto-check and a manual `app update`
// can all fire at once, and two processes swapping the same binary is how you
// end up with no working binary at all.
type Lock struct{ path string }

type lockInfo struct {
	PID       int       `json:"pid"`
	Host      string    `json:"host"`
	StartedAt time.Time `json:"started_at"`
}

// AcquireLock takes the lock, reclaiming it when the holder is older than
// stale. Staleness is time based rather than PID based: liveness checks by PID
// are unreliable on Windows and PIDs get reused.
func AcquireLock(path string, stale time.Duration) (*Lock, error) {
	if stale <= 0 {
		stale = 15 * time.Minute
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			host, _ := os.Hostname()
			b, _ := json.Marshal(lockInfo{PID: os.Getpid(), Host: host, StartedAt: time.Now()})
			f.Write(b)
			f.Close()
			return &Lock{path: path}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		st, serr := os.Stat(path)
		if serr != nil {
			continue // vanished between calls, try again
		}
		if time.Since(st.ModTime()) < stale {
			return nil, fmt.Errorf("%w (held since %s)", ErrLocked, st.ModTime().Format(time.RFC3339))
		}
		os.Remove(path) // stale, reclaim it
	}
	return nil, ErrLocked
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	return os.Remove(l.path)
}
