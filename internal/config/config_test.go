package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDurationJSON(t *testing.T) {
	var c struct{ D Duration }
	if err := json.Unmarshal([]byte(`{"D":"15m"}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.D.Duration() != 15*time.Minute {
		t.Fatalf("got %s", c.D)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"D":"15m0s"}` {
		t.Fatalf("marshal: %s", b)
	}
}

func TestEnvBeatsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"channel":"beta"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_CONFIG", path)
	t.Setenv("APP_CHANNEL", "stable")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Channel != "stable" {
		t.Fatalf("env should win, got %q", c.Channel)
	}
}

func TestValidateRejectsUnknownChannel(t *testing.T) {
	c := Default()
	c.Channel = "nightly"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRequiresAnEnabledSource(t *testing.T) {
	c := Default()
	for i := range c.Sources {
		c.Sources[i].Disabled = true
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error when every source is disabled")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Setenv("APP_CONFIG", path)

	c := Default()
	c.Channel = ChannelBeta
	c.SnoozeUntil = time.Now().Add(24 * time.Hour).Truncate(time.Second)
	c.path = path
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Channel != ChannelBeta || !got.Snoozed() {
		t.Fatalf("round trip lost data: %+v", got)
	}
}
