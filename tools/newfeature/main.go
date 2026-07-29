package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var valid = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func main() {
	name := flag.String("name", "", "feature package name")
	flag.Parse()
	if !valid.MatchString(*name) { fmt.Fprintln(os.Stderr, "name must match ^[a-z][a-z0-9_]*$"); os.Exit(2) }
	dir := filepath.Join("internal", "features", *name)
	if err := os.MkdirAll(dir, 0o755); err != nil { fail(err) }
	body := fmt.Sprintf(`package %s

import (
    "context"
    "github.com/o25160526-pip/go-selfupdate-template/internal/features"
)

type Feature struct{}
func (Feature) ID() string { return %q }
func (Feature) Start(context.Context) error { return nil }
func (Feature) MenuItems() []features.MenuItem { return nil }

func init() { features.Register(Feature{}) }
`, *name, *name)
	path := filepath.Join(dir, *name+".go")
	if _, err := os.Stat(path); err == nil { fail(fmt.Errorf("%s already exists", path)) }
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { fail(err) }
	fmt.Println(path)
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
