// Package config loads runtime configuration.
//
// Precedence, highest wins:
//
//	CLI flag > env (APP_*) > config file > manifest policy > built-in default
//
// One deliberate exception: ForceUpdate and Blocked from the signed manifest
// always beat local config. If a client could opt out of those, the kill switch
// would be decorative.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AppName is overridable at build time so a template consumer only has to
// rename it in one place.
var AppName = "app"

// Duration is a time.Duration that serialises as "15m" instead of nanoseconds.
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }
func (d Duration) String() string          { return time.Duration(d).String() }

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		p, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(p)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("duration must be a string like \"15m\"")
	}
	*d = Duration(time.Duration(n) * time.Second)
	return nil
}

// SourceType values.
const (
	SourceGitHub = "github"
	SourceAzure  = "azure"
)

// Azure modes. Universal Packages has real versioning but downloads need the
// artifacttool CLI; blob mode is plain HTTP and works everywhere, so it is the
// default until the Azure side is decided.
const (
	AzureModeBlob = "blob"
	AzureModeFeed = "feed"
)

type SourceConfig struct {
	Type     string `json:"type"`
	Disabled bool   `json:"disabled,omitempty"`

	// github
	Owner string `json:"owner,omitempty"`
	Repo  string `json:"repo,omitempty"`

	// azure
	Mode         string `json:"mode,omitempty"`
	IndexURL     string `json:"index_url,omitempty"` // blob mode: URL of index.json
	Organization string `json:"organization,omitempty"`
	Project      string `json:"project,omitempty"`
	Feed         string `json:"feed,omitempty"`
	Package      string `json:"package,omitempty"`
}

func (s SourceConfig) Enabled() bool { return !s.Disabled }

type Config struct {
	AppName     string `json:"app_name"`
	AssetPrefix string `json:"asset_prefix"` // assets are <prefix>_<os>_<arch>[.exe]

	Channel       string   `json:"channel"`
	AutoUpdate    bool     `json:"auto_update"`
	CheckInterval Duration `json:"check_interval"`
	Timeout       Duration `json:"timeout"`

	ManifestURL      string `json:"manifest_url"`
	RequireSignature bool   `json:"require_signature"`

	Sources []SourceConfig `json:"sources"`

	CacheTTL      Duration `json:"cache_ttl"`
	PrefetchCount int      `json:"prefetch_count"` // how many newer versions to pre-download
	KeepBlobs     int      `json:"keep_blobs"`     // LRU cap on cached binaries

	SnoozeUntil time.Time `json:"snooze_until,omitempty"`

	// Escape hatch for local development only. Never ship this enabled.
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`

	path string `json:"-"`
}

// Default is the configuration a freshly cloned template starts with.
func Default() *Config {
	return &Config{
		AppName:          AppName,
		AssetPrefix:      AppName,
		Channel:          ChannelStable,
		AutoUpdate:       false,
		CheckInterval:    Duration(6 * time.Hour),
		Timeout:          Duration(5 * time.Minute),
		RequireSignature: true,
		CacheTTL:         Duration(15 * time.Minute),
		PrefetchCount:    3,
		KeepBlobs:        6,
		Sources: []SourceConfig{
			{Type: SourceGitHub, Owner: "o25160526-pip", Repo: "go-selfupdate-template"},
			{Type: SourceAzure, Mode: AzureModeBlob, Disabled: true},
		},
	}
}

// Channels. `internal` reads draft/prerelease builds and only works when an
// update token is present in the environment, so no shipped binary can use it.
const (
	ChannelStable   = "stable"
	ChannelBeta     = "beta"
	ChannelInternal = "internal"
)

func ValidChannel(c string) bool {
	switch c {
	case ChannelStable, ChannelBeta, ChannelInternal:
		return true
	}
	return false
}

// Load reads defaults, then the config file, then environment overrides.
// A missing config file is not an error: the app must work on first run.
func Load() (*Config, error) {
	c := Default()
	path := FilePath()
	c.path = path

	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	c.applyEnv()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("APP_CHANNEL"); v != "" {
		c.Channel = v
	}
	if v := os.Getenv("APP_MANIFEST_URL"); v != "" {
		c.ManifestURL = v
	}
	if v, ok := envBool("APP_AUTO_UPDATE"); ok {
		c.AutoUpdate = v
	}
	if v, ok := envBool("APP_REQUIRE_SIGNATURE"); ok {
		c.RequireSignature = v
	}
	if v, ok := envBool("APP_INSECURE_SKIP_VERIFY"); ok {
		c.InsecureSkipVerify = v
	}
	if v := os.Getenv("APP_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Timeout = Duration(d)
		}
	}
	if v, ok := envInt("APP_PREFETCH_COUNT"); ok {
		c.PrefetchCount = v
	}
	// Per-source overrides, handy in CI where the repo is known from the workflow.
	for i := range c.Sources {
		switch c.Sources[i].Type {
		case SourceGitHub:
			if v := os.Getenv("APP_GITHUB_OWNER"); v != "" {
				c.Sources[i].Owner = v
			}
			if v := os.Getenv("APP_GITHUB_REPO"); v != "" {
				c.Sources[i].Repo = v
			}
		case SourceAzure:
			if v := os.Getenv("APP_AZURE_INDEX_URL"); v != "" {
				c.Sources[i].IndexURL = v
				c.Sources[i].Mode = AzureModeBlob
				c.Sources[i].Disabled = false
			}
		}
	}
}

func (c *Config) Validate() error {
	if !ValidChannel(c.Channel) {
		return fmt.Errorf("unknown channel %q (want stable, beta or internal)", c.Channel)
	}
	if c.AssetPrefix == "" {
		c.AssetPrefix = c.AppName
	}
	if c.Timeout <= 0 {
		c.Timeout = Duration(5 * time.Minute)
	}
	if c.PrefetchCount < 0 {
		c.PrefetchCount = 0
	}
	if c.KeepBlobs <= 0 {
		c.KeepBlobs = 6
	}
	enabled := 0
	for _, s := range c.Sources {
		if !s.Enabled() {
			continue
		}
		enabled++
		switch s.Type {
		case SourceGitHub:
			if s.Owner == "" || s.Repo == "" {
				return fmt.Errorf("github source needs owner and repo")
			}
		case SourceAzure:
			switch s.Mode {
			case AzureModeBlob, "":
				if s.IndexURL == "" {
					return fmt.Errorf("azure blob source needs index_url")
				}
			case AzureModeFeed:
				if s.Organization == "" || s.Feed == "" || s.Package == "" {
					return fmt.Errorf("azure feed source needs organization, feed and package")
				}
			default:
				return fmt.Errorf("unknown azure mode %q", s.Mode)
			}
		default:
			return fmt.Errorf("unknown source type %q", s.Type)
		}
	}
	if enabled == 0 {
		return fmt.Errorf("no update source is enabled")
	}
	return nil
}

func (c *Config) Path() string { return c.path }

// Save writes the config back, creating the directory if needed.
func (c *Config) Save() error {
	path := c.path
	if path == "" {
		path = FilePath()
		c.path = path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Snoozed reports whether the user asked to be left alone for now.
func (c *Config) Snoozed() bool {
	return !c.SnoozeUntil.IsZero() && time.Now().Before(c.SnoozeUntil)
}

// UpdateToken is the credential used to read draft/prerelease builds.
// It is read from the environment only. Never embed it in a shipped binary.
func UpdateToken() string {
	for _, k := range []string{"APP_UPDATE_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// AzureToken is the PAT for private Azure feeds/containers.
func AzureToken() string {
	for _, k := range []string{"APP_AZURE_TOKEN", "AZURE_DEVOPS_EXT_PAT", "SYSTEM_ACCESSTOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// ---- paths ----

func FilePath() string {
	if v := os.Getenv("APP_CONFIG"); v != "" {
		return v
	}
	return filepath.Join(Dir(), "config.json")
}

func Dir() string {
	if v := os.Getenv("APP_CONFIG_DIR"); v != "" {
		return v
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, AppName)
}

func CacheDir() string {
	if v := os.Getenv("APP_CACHE_DIR"); v != "" {
		return v
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, AppName)
}

func StateDir() string { return filepath.Join(CacheDir(), "state") }

func envBool(key string) (bool, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return false, false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, false
	}
	return b, true
}

func envInt(key string) (int, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}
