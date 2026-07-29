// Command newfeature scaffolds a feature package and wires it up.
//
// This generator is not sugar. Adding a feature by hand means remembering four
// separate steps, and three months from now the author of this repo will get
// one of them wrong too.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const module = "github.com/o25160526-pip/go-selfupdate-template"

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

func main() {
	name := flag.String("name", "", "feature name: one lowercase word, also used as the CLI verb")
	summary := flag.String("summary", "", "one line description shown in help output")
	flag.Parse()

	if !nameRe.MatchString(*name) {
		fatal("invalid name %q: use one lowercase word such as report", *name)
	}
	if *summary == "" {
		*summary = "TODO: describe " + *name
	}

	dir := filepath.Join("internal", "features", *name)
	if _, err := os.Stat(dir); err == nil {
		fatal("%s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("mkdir: %v", err)
	}

	write(filepath.Join(dir, *name+".go"), featureSource(*name, *summary))
	write(filepath.Join(dir, *name+"_test.go"), testSource(*name))

	if err := addImport(*name); err != nil {
		fatal("register the feature: %v", err)
	}
	fmt.Printf("created %s and registered it\n", dir)
	fmt.Printf("try: go run ./cmd/app %s\n", *name)
}

// addImport inserts the blank import into internal/features/all/all.go.
func addImport(name string) error {
	path := filepath.Join("internal", "features", "all", "all.go")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	src := string(b)
	imp := "\t_ " + quote(module+"/internal/features/"+name) + "\n"
	if strings.Contains(src, imp) {
		return nil
	}
	idx := strings.LastIndex(src, ")")
	if idx < 0 {
		return fmt.Errorf("could not find the import block in %s", path)
	}
	return os.WriteFile(path, []byte(src[:idx]+imp+src[idx:]), 0o644)
}

func quote(s string) string { return fmt.Sprintf("%q", s) }

func featureSource(name, summary string) string {
	lines := []string{
		"// Package " + name + " is a feature of this app.",
		"package " + name,
		"",
		"import (",
		"\t\"context\"",
		"\t\"fmt\"",
		"",
		"\t" + quote(module+"/internal/features"),
		")",
		"",
		"func init() { features.Register(&Feature{}) }",
		"",
		"type Feature struct{}",
		"",
		"func (f *Feature) ID() string      { return " + quote(name) + " }",
		"func (f *Feature) Summary() string { return " + quote(summary) + " }",
		"",
		"// MenuItems adds entries to the interactive menu and the tray.",
		"// Return nil if this feature has no interactive surface.",
		"func (f *Feature) MenuItems() []features.MenuItem {",
		"\treturn []features.MenuItem{{",
		"\t\tLabel: " + quote("Run "+name) + ",",
		"\t\tRun:   func(ctx context.Context) error { return f.Run(ctx, nil) },",
		"\t}}",
		"}",
		"",
		"// Run handles: app " + name + " [args...]",
		"func (f *Feature) Run(ctx context.Context, args []string) error {",
		"\tfmt.Println(" + quote("TODO: implement "+name) + ")",
		"\treturn nil",
		"}",
		"",
	}
	return strings.Join(lines, "\n")
}

func testSource(name string) string {
	lines := []string{
		"package " + name,
		"",
		"import (",
		"\t\"context\"",
		"\t\"testing\"",
		")",
		"",
		"func TestRunDoesNotError(t *testing.T) {",
		"\tif err := (&Feature{}).Run(context.Background(), nil); err != nil {",
		"\t\tt.Fatal(err)",
		"\t}",
		"}",
		"",
		"func TestIDIsStable(t *testing.T) {",
		"\tif got := (&Feature{}).ID(); got != " + quote(name) + " {",
		"\t\tt.Fatalf(\"feature ID changed to %q: the CLI verb is part of the contract\", got)",
		"\t}",
		"}",
		"",
	}
	return strings.Join(lines, "\n")
}

func write(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fatal("write %s: %v", path, err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "newfeature: "+format+"\n", args...)
	os.Exit(1)
}
