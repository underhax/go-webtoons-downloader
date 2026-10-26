// Package main provides the command-line entrypoint for wtd.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/underhax/go-webtoons-downloader/internal/config"
	"github.com/underhax/go-webtoons-downloader/internal/syncer"
	"github.com/underhax/go-webtoons-downloader/internal/updater"
	"github.com/underhax/go-webtoons-downloader/internal/webtoon"
)

// AppVersion holds the release version string, overridden at link time via -ldflags.
var AppVersion = "dev"

func defaultRun(args []string) error {
	return run(args)
}

func defaultUpdate(ctx context.Context, client *http.Client, version string) error {
	if err := updater.Update(ctx, client, version); err != nil {
		return fmt.Errorf("updater update: %w", err)
	}
	return nil
}

func defaultExecuteSync(ctx context.Context, cfg *config.Config, client *webtoon.Client, out io.Writer) error {
	syncInstance, err := syncer.New(cfg, client, out)
	if err != nil {
		return fmt.Errorf("initialize syncer: %w", err)
	}

	if err := syncInstance.Run(ctx); err != nil {
		return fmt.Errorf("execute sync: %w", err)
	}

	return nil
}

var (
	runFunc         = defaultRun
	exitFunc        = os.Exit
	updateFunc      = defaultUpdate
	executeSyncFunc = defaultExecuteSync
	stderrWriter    = io.Writer(os.Stderr)
	stdoutWriter    = io.Writer(os.Stdout)
)

func init() {
	config.AppVersion = AppVersion
}

func main() {
	setUmask()
	updater.CleanupWindowsOldFiles()

	if err := runFunc(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			exitFunc(0)
			return
		}
		if _, perr := fmt.Fprintf(stderrWriter, "Error: %v\n", err); perr != nil {
			exitFunc(1)
			return
		}
		exitFunc(1)
	}
}

func run(args []string) error {
	config.AppVersion = AppVersion
	cfg, err := config.Parse(args, stderrWriter)
	if err != nil {
		return fmt.Errorf("parse configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.Version {
		if _, perr := fmt.Fprintf(stdoutWriter, "wtd %s\n", AppVersion); perr != nil {
			return fmt.Errorf("print version: %w", perr)
		}
		return nil
	}

	if cfg.Update {
		if uerr := updateFunc(ctx, http.DefaultClient, AppVersion); uerr != nil {
			return fmt.Errorf("self-update: %w", uerr)
		}
		return nil
	}

	client := webtoon.NewClient(cfg.Timeout)

	out := stdoutWriter
	if cfg.Silent {
		out = io.Discard
	}

	return executeSyncFunc(ctx, cfg, client, out)
}
