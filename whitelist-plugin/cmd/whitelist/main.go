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
	// Constants for buffer sizes (avoid magic numbers)
	const (
		initBuf = 64 * 1024
		maxBuf  = 10 * 1024 * 1024
	)

	scanner := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 0, initBuf)
	scanner.Buffer(buf, maxBuf)

	for scanner.Scan() {
		// graceful shutdown check
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		response := processLine(scanner.Bytes(), h, io, logger)
		if _, err := os.Stdout.Write(response); err != nil {
			return err
		}
	}

	return scanner.Err()
}

// processLine is responsible for turning a raw line into a serialized response.
func processLine(line []byte, h handler.Handler, ioAd handler.IOAdapter, logger *log.Logger) []byte {
	logger.Printf("Received line: %s", line)
	inputMsg, err := ioAd.Input(line)
	if err != nil {
		logger.Printf("Invalid input: %v", err)
		return safeOutput(ioAd, handler.RejectMalformed(), logger)
	}

	outputMsg, err := h.Handle(inputMsg)
	if err != nil {
		logger.Printf("Handler error: %v", err)
		return safeOutput(ioAd, handler.RejectInternalWithError(inputMsg.Event, err), logger)
	}

	resp, err := ioAd.Output(outputMsg)
	if err != nil {
		logger.Printf("Failed to serialize output: %v", err)
		return safeOutput(ioAd, handler.RejectInternal(""), logger)
	}
	return resp
}

// safeOutput guarantees a serialized response, even if fallback serialization fails.
func safeOutput(ioAd handler.IOAdapter, msg handler.OutputMsg, logger *log.Logger) []byte {
	resp, err := ioAd.Output(msg)
	if err != nil {
		logger.Printf("Critical: failed to serialize fallback response: %v", err)
		// last-resort newline to keep stream well-formed
		return []byte("\n")
	}
	return resp
}
