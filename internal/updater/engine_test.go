package updater

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

type memorySource struct {
	release Release
	files map[string][]byte
}
func (m *memorySource) Name() string { return "memory" }
func (m *memorySource) List(context.Context, ListOptions) (ListResult, error) { return ListResult{Releases: []Release{m.release}}, nil }
func (m *memorySource) Fetch(_ context.Context, a Asset, w io.Writer, offset int64) error {
	body, ok := m.files[a.Name]
	if !ok { return fmt.Errorf("missing %s", a.Name) }
	if offset > int64(len(body)) { return fmt.Errorf("bad offset") }
	_, err := w.Write(body[offset:])
	return err
}

func TestEngineUpdatesVerifiedBinary(t *testing.T) {
	t.Setenv("APP_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("APP_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	target := filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil { t.Fatal(err) }
	newBody := []byte("new-binary")
	name := BinaryName("app", "linux", "amd64")
	// Force the test platform through a matching generic release by selecting
	// the real runtime name below in the Engine's AssetFor call.
	name = SelfBinaryName("app")
	checksums := []byte(SHA256Hex(newBody) + "  " + name + "\n")
	v := version.MustParse("1.26.0729.2100")
	source := &memorySource{files: map[string][]byte{name: newBody, ChecksumsFile: checksums}}
	source.release = Release{Version: v, Source: source.Name(), PublishedAt: time.Now(), Assets: []Asset{{Name: name}, {Name: ChecksumsFile}}}
	cfg := config.Default()
	cfg.AssetPrefix = "app"
	cfg.InsecureSkipVerify = true
	engine := Engine{Config: cfg, Sources: []Source{source}, Cache: Cache{Dir: config.CacheDir(), KeepBlobs: 2}, Target: target}
	result, err := engine.Update(context.Background(), UpdateRequest{})
	if err != nil { t.Fatal(err) }
	if !result.Updated || !result.Installed.Equal(v) { t.Fatalf("unexpected result: %+v", result) }
	got, err := os.ReadFile(target)
	if err != nil { t.Fatal(err) }
	if string(got) != string(newBody) { t.Fatalf("target = %q", got) }
}
