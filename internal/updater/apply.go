package updater

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrNeedsElevation is returned when the install directory is not writable,
// which is the normal case under C:\Program Files or /usr/local/bin.
var ErrNeedsElevation = errors.New("the install directory is not writable: re-run elevated (Windows: as administrator, Unix: with sudo)")

// Applied describes a completed swap so it can be undone.
type Applied struct {
	Exe    string
	Backup string
}

// SelfExe resolves the running executable through symlinks, so updating a
// symlinked install replaces the real binary rather than the link.
func SelfExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}

// ApplyBinary replaces the running executable with newPath.
//
// The same rename dance runs on every platform on purpose:
//
//  1. copy the new binary next to the current one (same filesystem, so the
//     final rename is atomic)
//  2. rename the current executable aside as a backup
//  3. rename the new binary into place
//
// Step 2 is what makes this work on Windows, where a running .exe cannot be
// overwritten but can be renamed. It also gives rollback for free.
func ApplyBinary(newPath string) (Applied, error) {
	exe, err := SelfExe()
	if err != nil {
		return Applied{}, err
	}
	dir := filepath.Dir(exe)
	base := filepath.Base(exe)

	if err := checkWritable(dir); err != nil {
		return Applied{}, err
	}

	staged := filepath.Join(dir, "."+base+".new")
	if err := copyFile(newPath, staged, 0o755); err != nil {
		return Applied{}, fmt.Errorf("stage new binary: %w", err)
	}
	defer os.Remove(staged) // no-op once the rename below succeeds

	backup := filepath.Join(dir, "."+base+".old")
	os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		return Applied{}, fmt.Errorf("move the current binary aside: %w", err)
	}
	if err := os.Rename(staged, exe); err != nil {
		// Put the old binary back before returning. Never leave the user
		// without a working executable.
		if rerr := os.Rename(backup, exe); rerr != nil {
			return Applied{}, fmt.Errorf("installing the new binary failed (%v) AND restoring the old one failed (%v): the previous binary is at %s", err, rerr, backup)
		}
		return Applied{}, fmt.Errorf("install new binary: %w", err)
	}
	return Applied{Exe: exe, Backup: backup}, nil
}

// Rollback restores the previous binary.
func Rollback(a Applied) error {
	if a.Backup == "" {
		return fmt.Errorf("no backup was recorded")
	}
	if _, err := os.Stat(a.Backup); err != nil {
		return fmt.Errorf("backup %s is missing: %w", a.Backup, err)
	}
	broken := a.Exe + ".failed"
	os.Remove(broken)
	_ = os.Rename(a.Exe, broken)
	if err := os.Rename(a.Backup, a.Exe); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	os.Remove(broken)
	return nil
}

// BackupPath returns where the previous binary is kept, if it is still there.
func BackupPath() (string, bool) {
	exe, err := SelfExe()
	if err != nil {
		return "", false
	}
	p := filepath.Join(filepath.Dir(exe), "."+filepath.Base(exe)+".old")
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// CleanupLeftovers removes debris from a previous update. On Windows the old
// file can still be locked immediately after the swap, so failures are ignored
// and simply retried on the next run.
func CleanupLeftovers() {
	exe, err := SelfExe()
	if err != nil {
		return
	}
	dir, base := filepath.Dir(exe), filepath.Base(exe)
	for _, suffix := range []string{".new", ".failed"} {
		os.Remove(filepath.Join(dir, "."+base+suffix))
	}
	os.Remove(exe + ".failed")
}

// VerifyBinary runs a candidate binary and reads back the version it reports.
//
// This is the check that lets CI refuse to publish a release that cannot even
// start, and it is why a bad update can be rolled back before a user notices.
func VerifyBinary(path string) (string, error) {
	out, err := exec.Command(path, "version", "--short").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("the new binary failed to run: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".writetest")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("%w (%s)", ErrNeedsElevation, dir)
		}
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Windows ignores the exec bit; everywhere else it is mandatory.
	if runtime.GOOS != "windows" {
		return os.Chmod(dst, mode)
	}
	return nil
}
