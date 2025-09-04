package main

import (
	"context"
	"event-forwarder/pkg/config"
	"event-forwarder/pkg/forwarder"
	"event-forwarder/pkg/telemetry"
	"event-forwarder/pkg/version"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Check for version flag first
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		info := version.Info()
		fmt.Printf("fwd version %s, commit %s, built %s\n", info.Version, info.Commit, info.Built)
		return
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	if cfg == nil {
		// Help was shown, exit gracefully
		os.Exit(0)
	}

	// Create telemetry system
	telemetryConfig := telemetry.DefaultConfig()
	aggregator := telemetry.NewAggregator(telemetry.RealClock{}, telemetryConfig)

	// Create logger - either quiet for CLI mode or discarded for TUI mode
	var logger *log.Logger
	if cfg.QuietMode {
		logger = log.New(os.Stdout, "[fwd] ", log.LstdFlags)
	} else {
		// Create a silent logger to avoid interfering with TUI
		logger = log.New(io.Discard, "", 0)
	}

	// Create forwarder with telemetry
	fwd := forwarder.New(cfg, logger, aggregator)

	// Create context for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start telemetry aggregator
	aggregator.Start(ctx)
	defer aggregator.Stop()

	if cfg.QuietMode {
		// Run in CLI mode
		cli := NewCLI(aggregator, cfg, logger)

		// Start forwarder in background
		go func() {
			if err := fwd.Start(ctx); err != nil && err != context.Canceled {
				cli.SetError(fmt.Sprintf("Forwarder error: %v", err))
			}
		}()

		// Run CLI (blocking)
		if err := cli.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "CLI error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Run in TUI mode
		tui := NewTUI(aggregator, cfg)

		// Start forwarder in background after TUI is set up
		go func() {
			// Small delay to let TUI initialize completely
			time.Sleep(100 * time.Millisecond)
			if err := fwd.Start(ctx); err != nil && err != context.Canceled {
				tui.SetError(fmt.Sprintf("Forwarder error: %v", err))
			}
		}()

		// Run TUI (blocking)
		if err := tui.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
			os.Exit(1)
		}
	}
}
