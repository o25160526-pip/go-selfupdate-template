package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Applied describes an installed binary and its rollback companion.
type Applied struct { Exe string; Backup string }

func SelfExe() (string, error) { exe, err := os.Executable(); if err != nil { return "", err }; return filepath.EvalSymlinks(exe) }
func BackupPath() (string, bool) { exe, err := SelfExe(); if err != nil { return "", false }; path := exe + ".old"; _, err = os.Stat(path); return path, err == nil }

func ApplyBinary(downloaded string) (Applied, error) { exe, err := SelfExe(); if err != nil { return Applied{}, err }; backup := exe + ".old"; if err := Apply(downloaded, exe); err != nil { return Applied{}, err }; return Applied{Exe: exe, Backup: backup}, nil }
func Rollback(a Applied) error { if a.Backup == "" { return fmt.Errorf("no backup available") }; if _, err := os.Stat(a.Backup); err != nil { return err }; return os.Rename(a.Backup, a.Exe) }

func VerifyBinary(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel()
	arg := "version"; cmd := exec.CommandContext(ctx, path, arg, "--json"); if runtime.GOOS == "windows" { cmd = exec.CommandContext(ctx, path, arg, "--json") }; out, err := cmd.Output(); if err != nil { return "", err }
	var info struct { Version string `json:"version"` }; if err := json.Unmarshal(out, &info); err != nil { return "", err }; if strings.TrimSpace(info.Version) == "" { return "", fmt.Errorf("binary returned no version") }; return info.Version, nil
}

func CleanupLeftovers() { exe, err := SelfExe(); if err != nil { return }; for _, path := range []string{exe+".new", exe+".tmp"} { _ = os.Remove(path) } }
