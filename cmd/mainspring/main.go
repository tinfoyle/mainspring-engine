package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/app"
	"github.com/tinfoyle/mainspring-engine/internal/config"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	logger := newLogger(cfg)
	if len(os.Args) < 2 {
		printUsage()
		return 2
	}

	if os.Args[1] == "version" {
		fmt.Println(version)
		return 0
	}
	if os.Args[1] == "healthcheck" {
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: mainspring healthcheck <url>")
			return 2
		}
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			fmt.Fprintln(os.Stderr, response.Status)
			return 1
		}
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, logger, cfg, os.Args[1], os.Args[2:]); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		logger.Error("mainspring stopped", "mode", os.Args[1], "error", err)
		return 1
	}

	return 0
}

func newLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: mainspring <control|gateway|tenant|rag|worker|runner-controller|runner|migrate|provision|healthcheck|version>")
}
