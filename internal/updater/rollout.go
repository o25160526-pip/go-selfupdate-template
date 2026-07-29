package updater

import (
	"hash/fnv"
	"os"
	"strings"
)

// MachineID returns a stable per-machine identifier.
//
// Stability is the entire point: a staged rollout must place the same machine
// in the same bucket on every run, otherwise "10%" just means every client
// updates eventually anyway.
func MachineID() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
	}
	// Windows and macOS: hostname plus the resolved executable path is stable
	// enough for bucketing and needs no extra dependency.
	host, _ := os.Hostname()
	exe, _ := SelfExe()
	if host == "" && exe == "" {
		return "unknown"
	}
	return host + "|" + exe
}

// RolloutBucket maps an identity to a stable bucket in [0,100).
func RolloutBucket(id, salt string) int {
	h := fnv.New64a()
	h.Write([]byte(id))
	h.Write([]byte{'|'})
	h.Write([]byte(salt))
	return int(h.Sum64() % 100)
}

// InRollout reports whether this machine is included at a given percentage.
// The salt is the target version, so every release reshuffles the buckets and
// the same unlucky machines are not always last.
func InRollout(percent int, salt string) bool {
	if percent >= 100 {
		return true
	}
	if percent <= 0 {
		return false
	}
	return RolloutBucket(MachineID(), salt) < percent
}
