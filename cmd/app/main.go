package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/desktop"
	"github.com/o25160526-pip/go-selfupdate-template/internal/exitcode"
	"github.com/o25160526-pip/go-selfupdate-template/internal/features"
	"github.com/o25160526-pip/go-selfupdate-template/internal/updater"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) == 0 { usage(); return exitcode.Usage }
	if feature, ok := features.Lookup(args[0]); ok { if err := feature.Run(context.Background(), args[1:]); err != nil { fmt.Fprintln(os.Stderr, err); return 1 }; return 0 }
	switch args[0] {
	case "version": return versionCommand(args[1:])
	case "update": return updateCommand(args[1:])
	case "watch": return watchCommand()
	case "tray": return trayCommand()
	case "menu": return menuCommand()
	case "help", "-h", "--help": usage(); return 0
	default: fmt.Fprintln(os.Stderr, "unknown command:", args[0]); usage(); return exitcode.Usage
	}
}
func usage() { fmt.Println("Usage: app <version|update|watch|tray|menu>") }
func versionCommand(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError); asJSON := fs.Bool("json", false, "machine-readable JSON")
	if err := fs.Parse(args); err != nil { return exitcode.Usage }
	v, err := version.Self(); if err != nil { fmt.Fprintln(os.Stderr, err); return 1 }
	info := version.Info{Version:v.String(), Semver:v.Semver(), Commit:version.Commit, BuildDate:version.BuildDate, Channel:version.Channel, OS:runtime.GOOS, Arch:runtime.GOARCH}
	if *asJSON { _ = json.NewEncoder(os.Stdout).Encode(info) } else { fmt.Printf("%s (%s, %s/%s, commit %s)\n", info.Version, info.Channel, info.OS, info.Arch, info.Commit) }; return 0
}
func runtimeEngine() (*updater.Engine, *config.Config, error) {
	cfg, err := config.Load(); if err != nil { return nil,nil,err }; sources, err := updater.NewSources(cfg, &http.Client{Timeout:cfg.Timeout.Duration()}); if err != nil { return nil,nil,err }
	return &updater.Engine{Config:cfg, Sources:sources, Cache:updater.Cache{Dir:config.CacheDir(), KeepBlobs:cfg.KeepBlobs}},cfg,nil
}
func updateCommand(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError); wanted:=fs.String("version","","specific version"); check:=fs.Bool("check",false,"check only"); silent:=fs.Bool("silent",false,"quiet")
	if err:=fs.Parse(args); err!=nil{return exitcode.Usage}; engine,cfg,err:=runtimeEngine(); if err!=nil{fmt.Fprintln(os.Stderr,err);return exitcode.Usage}; req:=updater.UpdateRequest{CheckOnly:*check}
	if *wanted!=""{v,e:=version.Parse(*wanted);if e!=nil{fmt.Fprintln(os.Stderr,e);return exitcode.Usage};req.Version=&v};ctx,cancel:=context.WithTimeout(context.Background(),cfg.Timeout.Duration());defer cancel();result,err:=engine.Update(ctx,req)
	if err!=nil{code:=exitcode.From(err);if !*silent||code!=exitcode.UpToDate{fmt.Fprintln(os.Stderr,err)};return code};if !*silent{if *check{fmt.Printf("available: %s\n",result.Installed)}else{fmt.Printf("updated %s -> %s from %s\n",result.Previous,result.Installed,result.Source)}};return 0
}
func watchCommand() int { engine,cfg,err:=runtimeEngine();if err!=nil{fmt.Fprintln(os.Stderr,err);return exitcode.Usage};ctx,stop:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM);defer stop();err=engine.Watch(ctx,cfg.CheckInterval.Duration(),nil);if err==nil||ctx.Err()!=nil{return 0};fmt.Fprintln(os.Stderr,err);return exitcode.From(err) }
func trayCommand() int { engine,_,err:=runtimeEngine();if err!=nil{fmt.Fprintln(os.Stderr,err);return exitcode.Usage};ctx,stop:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM);defer stop();if err:=desktop.Run(ctx,engine);err!=nil{fmt.Fprintln(os.Stderr,err);return 1};return 0 }
func menuCommand() int {
	ctx:=context.Background();reader:=bufio.NewReader(os.Stdin)
	for{fmt.Println("\n1) Version\n2) Check\n3) Update");items:=features.MenuItems();for i,item:=range items{fmt.Printf("%d) %s\n",4+i,item.Label)};fmt.Println("0) Exit");fmt.Print("> ");line,_:=reader.ReadString('\n');choice:=strings.TrimSpace(line);switch choice{case"0":return 0;case"1":versionCommand(nil);case"2":updateCommand([]string{"--check"});case"3":updateCommand(nil);default:handled:=false;for i,item:=range items{if choice==fmt.Sprint(4+i){handled=true;if err:=item.Run(ctx);err!=nil{fmt.Fprintln(os.Stderr,err)};break}};if !handled{fmt.Println("invalid choice")}}}
}
