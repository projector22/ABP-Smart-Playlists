package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/projector22/abp/internal/abp"
	"github.com/projector22/abp/internal/config"
	"github.com/projector22/abp/internal/cron"
	"github.com/projector22/abp/internal/engine"
	"github.com/projector22/abp/internal/tui"
	ver "github.com/projector22/abp/internal/version"
)

// version is stamped in at build time by the Makefile / release workflow with
// -ldflags "-X main.version=<tag>". Builds made with plain `go install` leave it
// as "dev"; internal/version then falls back to the module version Go embeds.
var version = "dev"

const usage = `abp - smart playlists for Audiobookshelf podcasts

Usage:
  abp                         open the interactive TUI
  abp sync [-n] [-all | id…]  sync playlists (this is what cron runs)
  abp list                    list configured playlists
  abp cron install            write schedules from config.yaml into your crontab
  abp cron remove             remove all abp entries from your crontab
  abp cron show               show abp entries currently in your crontab
  abp -v | version            show the version and whether an update is available

Flags for sync:
  -n, -dry-run   show what would change without touching Audiobookshelf
  -all           sync every playlist

Config:       ` + "%s" + `
Credentials:  ` + "%s" + ` (private, mode 0600)
Env overrides: ABP_URL, ABP_API_KEY, ABP_CONFIG, ABP_DATA_DIR
Set ABP_NO_UPDATE_CHECK=1 to stop abp contacting GitHub to look for new releases.
`

// main dispatches to the TUI (no arguments) or to one of the command-line
// subcommands used for scripting and cron.
func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		if err := tui.Run(ver.Detect(version)); err != nil {
			fatal(err)
		}
		return
	}
	switch args[0] {
	case "sync":
		os.Exit(cmdSync(args[1:]))
	case "list", "ls":
		cmdList()
	case "cron":
		cmdCron(args[1:])
	case "version", "-v", "-version", "--version":
		cmdVersion()
	case "help", "-h", "--help":
		fmt.Printf(usage, config.Path(), config.CredentialsPath())
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		fmt.Fprintf(os.Stderr, usage, config.Path(), config.CredentialsPath())
		os.Exit(2)
	}
}

// fatal prints err to stderr and exits with status 1.
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "abp:", err)
	os.Exit(1)
}

// stamp returns the current local time formatted for log lines.
func stamp() string { return time.Now().Format("2006-01-02 15:04:05") }

// cmdSync implements "abp sync": it syncs the named playlists (or all of them
// with -all) and prints one log line per playlist. This is the command cron
// runs. It returns the process exit code: 1 if any playlist failed, else 0, so
// failures are visible to cron and to scripts.
func cmdSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dry := fs.Bool("dry-run", false, "")
	fs.BoolVar(dry, "n", false, "")
	all := fs.Bool("all", false, "")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	creds, err := config.LoadCredentials()
	if err != nil {
		fatal(err)
	}
	if !creds.Valid() {
		fatal(fmt.Errorf("no credentials: run `abp` once to connect, or set ABP_URL and ABP_API_KEY"))
	}

	var targets []config.Playlist
	switch {
	case *all:
		targets = cfg.Playlists
	case fs.NArg() > 0:
		for _, id := range fs.Args() {
			p := cfg.Find(id)
			if p == nil {
				fatal(fmt.Errorf("no playlist with id %q (see `abp list`)", id))
			}
			targets = append(targets, *p)
		}
	default:
		fatal(fmt.Errorf("say which playlist(s) to sync, or pass -all"))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := abp.NewClient(creds.URL, creds.APIKey)
	state := config.LoadState()

	failed := 0
	for _, pl := range targets {
		tctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		res, err := engine.Run(tctx, client, pl, state, *dry)
		cancel()
		if err != nil {
			failed++
			fmt.Printf("%s %s: ERROR %v\n", stamp(), pl.ID, err)
			continue
		}
		prefix := ""
		if *dry {
			prefix = "[dry-run] would be "
		}
		fmt.Printf("%s %s: %s%s\n", stamp(), pl.ID, prefix, res.Summary())
		for _, n := range res.Notes {
			fmt.Printf("%s %s: note: %s\n", stamp(), pl.ID, n)
		}
		for _, w := range res.Warnings {
			fmt.Printf("%s %s: warning: %s\n", stamp(), pl.ID, w)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// cmdList implements "abp list": a plain-text summary of the configured
// playlists, handy for finding the ids to pass to "abp sync".
func cmdList() {
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	if len(cfg.Playlists) == 0 {
		fmt.Println("no playlists yet; run `abp` to create one")
		return
	}
	for _, p := range cfg.Playlists {
		sched := "manual"
		if p.Schedule != "" {
			sched = p.Schedule
		}
		fmt.Printf("%-24s %3d eps  %2d podcasts  %s\n", p.ID, p.Limit, len(p.Podcasts), sched)
	}
}

// cmdCron implements "abp cron install|remove|show", which manages abp's entries
// in the user's crontab (the TUI does the same when a schedule is edited).
func cmdCron(args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("usage: abp cron install|remove|show"))
	}
	switch args[0] {
	case "install":
		cfg, err := config.Load()
		if err != nil {
			fatal(err)
		}
		if err := cron.Sync(cfg); err != nil {
			fatal(err)
		}
		n := 0
		for _, p := range cfg.Playlists {
			if p.Schedule != "" {
				n++
			}
		}
		fmt.Printf("crontab updated: %d scheduled playlist(s)\n", n)
	case "remove":
		if err := cron.Remove(); err != nil {
			fatal(err)
		}
		fmt.Println("removed all abp entries from your crontab")
	case "show":
		m, err := cron.Managed()
		if err != nil {
			fatal(err)
		}
		if len(m) == 0 {
			fmt.Println("no abp entries in crontab")
		}
		for id, s := range m {
			fmt.Printf("%-24s %s\n", id, strings.TrimSpace(s))
		}
	default:
		fatal(fmt.Errorf("unknown cron command %q", args[0]))
	}
}

// cmdVersion implements "abp -v": it prints this build's version and then asks
// GitHub whether a newer release exists, telling the user if they are up to
// date, can update, or are running a development build ahead of any release. A
// failed check is reported but never makes the command fail, so the version can
// always be read offline.
func cmdVersion() {
	b := ver.Detect(version)
	fmt.Println("abp", b.Long())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, line := range ver.Check(ctx, b, 0).Lines() {
		fmt.Println(line)
	}
}
