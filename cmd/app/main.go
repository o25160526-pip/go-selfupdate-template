package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/exitcode"
	"github.com/o25160526-pip/go-selfupdate-template/internal/features"
	"github.com/o25160526-pip/go-selfupdate-template/internal/updater"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 { usage(); return exitcode.Usage }
	switch args[0] {
	case "version": return versionCommand(args[1:])
	case "update": return updateCommand(args[1:])
	case "menu": return menuCommand()
	case "help", "-h", "--help": usage(); return 0
	default: fmt.Fprintln(os.Stderr, "unknown command:", args[0]); usage(); return exitcode.Usage
	}
}

func usage() {
	fmt.Println("Usage: app <version|update|menu>")
	fmt.Println("  app version [--json]")
	fmt.Println("  app update [--version 1.YY.MMDD.HHmm] [--check] [--silent]")
}

func versionCommand(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil { return exitcode.Usage }
	v, err := version.Self()
	if err != nil { fmt.Fprintln(os.Stderr, err); return 1 }
	info := version.Info{Version: v.String(), Semver: v.Semver(), Commit: version.Commit, BuildDate: version.BuildDate, Channel: version.Channel, OS: runtime.GOOS, Arch: runtime.GOARCH}
	if *asJSON { _ = json.NewEncoder(os.Stdout).Encode(info) } else { fmt.Printf("%s (%s, %s/%s, commit %s)\n", info.Version, info.Channel, info.OS, info.Arch, info.Commit) }
	return 0
}

func updateCommand(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	wanted := fs.String("version", "", "specific display or semver version")
	check := fs.Bool("check", false, "check only")
	silent := fs.Bool("silent", false, "suppress normal output")
	if err := fs.Parse(args); err != nil { return exitcode.Usage }
	cfg, err := config.Load()
	if err != nil { fmt.Fprintln(os.Stderr, err); return exitcode.Usage }
	client := &http.Client{Timeout: cfg.Timeout.Duration()}
	sources, err := updater.NewSources(cfg, client)
	if err != nil { fmt.Fprintln(os.Stderr, err); return exitcode.Usage }
	request := updater.UpdateRequest{CheckOnly: *check}
	if *wanted != "" {
		v, parseErr := version.Parse(*wanted)
		if parseErr != nil { fmt.Fprintln(os.Stderr, parseErr); return exitcode.Usage }
		request.Version = &v
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout.Duration())
	defer cancel()
	engine := updater.Engine{Config: cfg, Sources: sources, Cache: updater.Cache{Dir: config.CacheDir(), KeepBlobs: cfg.KeepBlobs}}
	result, err := engine.Update(ctx, request)
	if err != nil {
		code := exitcode.From(err)
		if !*silent || code != exitcode.UpToDate { fmt.Fprintln(os.Stderr, err) }
		return code
	}
	if !*silent {
		if *check { fmt.Printf("available: %s\n", result.Installed) } else { fmt.Printf("updated %s -> %s from %s\n", result.Previous, result.Installed, result.Source) }
	}
	return exitcode.OK
}

func menuCommand() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := features.StartAll(ctx); err != nil { fmt.Fprintln(os.Stderr, err); return 1 }
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\nSelf-update menu")
		fmt.Println("1) Show version")
		fmt.Println("2) Check for update")
		fmt.Println("3) Install latest")
		base := 4
		for i, item := range features.MenuItems() { fmt.Printf("%d) %s\n", base+i, item.Title) }
		fmt.Println("0) Exit")
		fmt.Print("> ")
		line, _ := reader.ReadString('\n')
		switch strings.TrimSpace(line) {
		case "0": return 0
		case "1": versionCommand(nil)
		case "2": updateCommand([]string{"--check"})
		case "3": updateCommand(nil)
		default: fmt.Println("invalid choice")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
