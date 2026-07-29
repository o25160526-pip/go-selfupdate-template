package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Apply atomically replaces target with downloaded and restores the old binary
// if any step fails. The caller should verify downloaded before calling Apply.
func Apply(downloaded, target string) error {
	if target == "" {
		self, err := os.Executable()
		if err != nil { return err }
		target, err = filepath.EvalSymlinks(self)
		if err != nil { return err }
	}
	info, err := os.Stat(target)
	if err != nil { return fmt.Errorf("stat current binary: %w", err) }
	staged := target + ".new"
	backup := target + ".old"
	_ = os.Remove(staged)
	_ = os.Remove(backup)
	if err := copyFile(downloaded, staged, info.Mode()); err != nil { return fmt.Errorf("stage update: %w", err) }
	if runtime.GOOS == "windows" {
		// Windows cannot replace a running executable. Rename the current file
		// first; open executable handles remain valid while the name changes.
		if err := os.Rename(target, backup); err != nil { _ = os.Remove(staged); return fmt.Errorf("backup current binary: %w", err) }
		if err := os.Rename(staged, target); err != nil {
			_ = os.Rename(backup, target)
			return fmt.Errorf("install update: %w", err)
		}
		_ = os.Remove(backup)
		return nil
	}
	if err := os.Rename(target, backup); err != nil { _ = os.Remove(staged); return fmt.Errorf("backup current binary: %w", err) }
	if err := os.Rename(staged, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("install update: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil { return err }
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil { return err }
	ok := false
	defer func() { _ = out.Close(); if !ok { _ = os.Remove(dst) } }()
	if _, err := io.Copy(out, in); err != nil { return err }
	if err := out.Sync(); err != nil { return err }
	if err := out.Close(); err != nil { return err }
	ok = true
	return nil
}
