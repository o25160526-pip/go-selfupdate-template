// Command genversion prints the build version for the current moment.
//
// Two problems this solves, both of which would break the pipeline in
// production and neither of which is obvious:
//
//  1. Timezone drift. GitHub runners are UTC, Azure agents depend on the pool.
//     The same commit would get two different versions. Everything here is UTC.
//
//  2. Version collision. Two commits inside the same minute produce the same
//     HHmm, the tag already exists, and the pipeline dies halfway through with
//     a tag but no release (clients then 404). We bump the minute until a free
//     slot is found.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

func main() {
	checkTags := flag.Bool("check-tags", true, "bump the minute while the git tag already exists")
	format := flag.String("format", "all", "display | semver | tag | all")
	ghOutput := flag.String("github-output", "", "append key=value lines to this file (use $GITHUB_OUTPUT)")
	at := flag.String("at", "", "RFC3339 timestamp to use instead of now (for tests)")
	maxBump := flag.Int("max-bump", 240, "give up after this many minute bumps")
	flag.Parse()

	now := time.Now().UTC()
	if *at != "" {
		p, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			fatal("parse -at: %v", err)
		}
		now = p.UTC()
	}

	v := version.FromTime(now)
	if *checkTags {
		bumped := 0
		for tagExists(v.Tag()) {
			bumped++
			if bumped > *maxBump {
				fatal("no free version slot after %d minute bumps starting at %s", *maxBump, v)
			}
			v = version.FromTime(v.Time().Add(time.Minute))
		}
		if bumped > 0 {
			fmt.Fprintf(os.Stderr, "genversion: tag collision, bumped %d minute(s) -> %s\n", bumped, v)
		}
	}

	switch *format {
	case "display":
		fmt.Println(v.String())
	case "semver":
		fmt.Println(v.Semver())
	case "tag":
		fmt.Println(v.Tag())
	case "all":
		fmt.Printf("display=%s\nsemver=%s\ntag=%s\n", v.String(), v.Semver(), v.Tag())
	default:
		fatal("unknown -format %q", *format)
	}

	if *ghOutput != "" {
		f, err := os.OpenFile(*ghOutput, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fatal("open github output: %v", err)
		}
		defer f.Close()
		fmt.Fprintf(f, "display=%s\nsemver=%s\ntag=%s\n", v.String(), v.Semver(), v.Tag())
	}
}

func tagExists(tag string) bool {
	// Local tags only; CI must fetch tags first (fetch-depth: 0, fetch-tags: true).
	cmd := exec.Command("git", "rev-parse", "-q", "--verify", "refs/tags/"+tag)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Run(); err == nil {
		return true
	}
	// Also check the remote, in case another job already pushed the tag.
	out, err := exec.Command("git", "ls-remote", "--tags", "origin", "refs/tags/"+tag).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "genversion: "+format+"\n", args...)
	os.Exit(1)
}
