package main

import (
	"bufio"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"whitelist-plugin/pkg/client"
	"whitelist-plugin/pkg/config"
	"whitelist-plugin/pkg/handler"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stderr, "[whitelist-plugin] ", log.LstdFlags)

	cfg, err := config.LoadClientConfig()
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	checker := client.NewWhitelistClient(cfg.ServerURL, cfg.CheckTimeout, logger)

	if err := checker.CheckHealth(); err != nil {
		logger.Printf("WARNING: %v", err)
		logger.Printf("Events will be rejected until the server is reachable")
	} else {
		logger.Printf("Connected to whitelist server at %s", cfg.ServerURL)
	}

	h := handler.NewWhitelistHandler(checker, logger)
	ioAdapter := handler.NewJSONLIOAdapter(os.Stdout)

	if err := runEventLoop(ctx, h, ioAdapter, logger); err != nil {
		logger.Printf("Error in event loop: %v", err)
		os.Exit(1)
	}
}

func runEventLoop(ctx context.Context, h handler.Handler, io handler.IOAdapter, logger *log.Logger) error {
	const (
		initBuf = 64 * 1024
		maxBuf  = 10 * 1024 * 1024
	)

	scanner := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 0, initBuf)
	scanner.Buffer(buf, maxBuf)

	type scanResult struct {
		line []byte
		err  error
	}
	lines := make(chan scanResult)

	go func() {
		defer close(lines)
		for scanner.Scan() {
			line := make([]byte, len(scanner.Bytes()))
			copy(line, scanner.Bytes())
			lines <- scanResult{line: line}
		}
		if err := scanner.Err(); err != nil {
			lines <- scanResult{err: err}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case result, ok := <-lines:
			if !ok {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			response := processLine(result.line, h, io, logger)
			if _, err := os.Stdout.Write(response); err != nil {
				return err
			}
		}
	}
}

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
		return safeOutput(ioAd, handler.RejectInternalWithError(inputMsg.Event.ID, err), logger)
	}

	resp, err := ioAd.Output(outputMsg)
	if err != nil {
		logger.Printf("Failed to serialize output: %v", err)
		return safeOutput(ioAd, handler.RejectInternal(""), logger)
	}
	return resp
}

func safeOutput(ioAd handler.IOAdapter, msg handler.OutputMsg, logger *log.Logger) []byte {
	resp, err := ioAd.Output(msg)
	if err != nil {
		logger.Printf("Critical: failed to serialize fallback response: %v", err)
		return []byte("\n")
	}
	return resp
}
