package main

import (
	"context"
	"event-forwarder/pkg/config"
	"event-forwarder/pkg/telemetry"
	"log"
	"time"
)

// CLI represents the command-line interface runner
type CLI struct {
	telemetry telemetry.TelemetryReader
	config    *config.Config
	logger    *log.Logger

	// State
	lastSnapshot telemetry.Snapshot
	done         chan struct{}
}

// NewCLI creates a new command-line interface runner
func NewCLI(telemetryReader telemetry.TelemetryReader, cfg *config.Config, logger *log.Logger) *CLI {
	return &CLI{
		telemetry: telemetryReader,
		config:    cfg,
		logger:    logger,
		done:      make(chan struct{}),
	}
}

// Run starts the CLI runner and blocks until shutdown
func (c *CLI) Run(ctx context.Context) error {
	c.logger.Printf("Starting Event Forwarder in quiet mode")
	c.logger.Printf("Source: %s", c.config.SourceRelayURL)
	c.logger.Printf("DeepFry: %s", c.config.DeepFryRelayURL)
	c.logger.Printf("Sync window: %d seconds", c.config.Sync.WindowSeconds)

	// Print periodic status updates
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Printf("Shutting down...")
			return nil
		case <-ticker.C:
			c.printStatus()
		case <-c.done:
			return nil
		}
	}
}

// SetError logs an error message
func (c *CLI) SetError(err string) {
	c.logger.Printf("ERROR: %s", err)
}

// Stop stops the CLI runner
func (c *CLI) Stop() {
	close(c.done)
}

// printStatus prints current telemetry status
func (c *CLI) printStatus() {
	snapshot := c.telemetry.Snapshot()

	// Only print if there are changes or significant activity
	if c.shouldPrintStatus(snapshot) {
		c.logger.Printf("Status - Events: received=%d, forwarded=%d, rate=%.1f/s, errors=%d",
			snapshot.EventsReceived,
			snapshot.EventsForwarded,
			snapshot.EventsPerSecond,
			snapshot.ErrorsTotal)

		// Print connection status
		c.logger.Printf("Connections - Source: %t, DeepFry: %t",
			snapshot.SourceRelayConnected,
			snapshot.DeepFryRelayConnected)

		// Print sync info if available
		if snapshot.SyncWindowFrom > 0 {
			c.logger.Printf("Sync window: %d to %d, lag: %.1fs, mode: %s",
				snapshot.SyncWindowFrom,
				snapshot.SyncWindowTo,
				snapshot.SyncLagSeconds,
				snapshot.CurrentSyncMode)
		}
	}

	c.lastSnapshot = snapshot
}

// shouldPrintStatus determines if we should print a status update
func (c *CLI) shouldPrintStatus(snapshot telemetry.Snapshot) bool {
	// Always print first status
	if c.lastSnapshot.EventsReceived == 0 && c.lastSnapshot.EventsForwarded == 0 {
		return true
	}

	// Print if event counts changed
	if snapshot.EventsReceived != c.lastSnapshot.EventsReceived ||
		snapshot.EventsForwarded != c.lastSnapshot.EventsForwarded {
		return true
	}

	// Print if there are errors
	if snapshot.ErrorsTotal > c.lastSnapshot.ErrorsTotal {
		return true
	}

	// Print if connection status changed
	if snapshot.SourceRelayConnected != c.lastSnapshot.SourceRelayConnected ||
		snapshot.DeepFryRelayConnected != c.lastSnapshot.DeepFryRelayConnected {
		return true
	}

	return false
}
