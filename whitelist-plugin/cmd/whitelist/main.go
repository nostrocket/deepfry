package main

import (
	"bufio"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
	"whitelist-plugin/pkg/handler"
	"whitelist-plugin/pkg/repository"
	"whitelist-plugin/pkg/whitelist"
)

func main() {
	// Setup context for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Initialize logger for errors (stderr only)
	logger := log.New(os.Stderr, "[whitelist-plugin] ", log.LstdFlags)

	// Initialize components
	keyRepo := repository.NewSimpleRepository()
	refresher := whitelist.NewWhitelistRefresher(keyRepo, 5*time.Minute, 3, logger)

	// Start background refresh
	refresher.Start()
	defer refresher.Stop()

	// Create handler and IO adapter
	h := handler.NewWhitelistHandler(refresher.Whitelist(), logger)
	ioAdapter := handler.NewJSONLIOAdapter(os.Stdout)

	// Run main loop
	if err := runEventLoop(ctx, h, ioAdapter, logger); err != nil {
		logger.Printf("Error in event loop: %v", err)
		os.Exit(1)
	}
}

func runEventLoop(ctx context.Context, h handler.Handler, io handler.IOAdapter, logger *log.Logger) error {
	scanner := bufio.NewScanner(os.Stdin)

	// Increase buffer size for large events
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()

		// Parse input
		inputMsg, err := io.Input(line)
		if err != nil {
			logger.Printf("Invalid input: %v", err)
			continue
		}

		// Process message
		outputMsg, err := h.Handle(inputMsg)
		if err != nil {
			logger.Printf("Handler error: %v", err)
			continue
		}

		// Write response
		response, err := io.Output(outputMsg)
		if err != nil {
			logger.Printf("Failed to serialize output: %v", err)
			continue
		}

		if _, err := os.Stdout.Write(response); err != nil {
			return err
		}
	}

	return scanner.Err()
}
