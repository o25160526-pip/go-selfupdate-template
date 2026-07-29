package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Lock is a portable create-exclusive lock. Stale locks are reclaimed.
type Lock struct { path string }

func AcquireLock(dir string, staleAfter time.Duration) (*Lock, error) {
	if staleAfter <= 0 { staleAfter = 30 * time.Minute }
	if err := os.MkdirAll(dir, 0o700); err != nil { return nil, err }
	path := filepath.Join(dir, "update.lock")
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			_ = file.Close()
			return &Lock{path: path}, nil
		}
		if !os.IsExist(err) { return nil, err }
		info, statErr := os.Stat(path)
		if statErr == nil && time.Since(info.ModTime()) > staleAfter {
			_ = os.Remove(path)
			continue
		}
		body, _ := os.ReadFile(path)
		return nil, fmt.Errorf("update already running (lock %s, pid %s)", path, firstLine(string(body)))
	}
	return nil, fmt.Errorf("could not acquire update lock")
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" { return nil }
	return os.Remove(l.path)
}

func firstLine(s string) string {
	for i, r := range s { if r == '\n' { return s[:i] } }
	if _, err := strconv.Atoi(s); err == nil { return s }
	return "unknown"
}
