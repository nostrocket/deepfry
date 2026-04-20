// Command router is the StrFry writePolicy plugin that routes non-whitelisted
// events to a quarantine relay for later analysis. See quarantine/SPEC.md.
//
// It is an additive alternative to cmd/whitelist; the whitelist plugin is
// preserved unchanged for rollback.
package main

import (
	"bufio"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"whitelist-plugin/pkg/client"
	"whitelist-plugin/pkg/config"
	"whitelist-plugin/pkg/handler"
	"whitelist-plugin/pkg/quarantine"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stderr, "[router-plugin] ", log.LstdFlags)

	cfg, err := config.LoadRouterConfig()
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	checker := client.NewWhitelistClient(cfg.ServerURL, cfg.CheckTimeout, logger)
	if err := checker.CheckHealth(); err != nil {
		logger.Printf("WARNING: %v", err)
		logger.Printf("Events will be rejected until the whitelist server is reachable")
	} else {
		logger.Printf("Connected to whitelist server at %s", cfg.ServerURL)
	}

	var publisher *quarantine.Publisher
	if cfg.Quarantine.Enabled {
		publisher = quarantine.NewPublisher(quarantine.Config{
			RelayURL:        cfg.Quarantine.RelayURL,
			BufferSize:      cfg.Quarantine.BufferSize,
			PublishTimeout:  cfg.Quarantine.PublishTimeout,
			MetricsInterval: cfg.Quarantine.MetricsInterval,
		}, logger)
		publisher.Start(ctx)
		logger.Printf("Quarantine publisher started -> %s (buffer=%d)", cfg.Quarantine.RelayURL, cfg.Quarantine.BufferSize)
	} else {
		logger.Printf("Quarantine disabled by config; plugin will behave like the whitelist plugin")
	}

	h := handler.NewRouterHandler(checker, publisher, cfg.Quarantine.Enabled, logger)
	io := handler.NewRouterIOAdapter(os.Stdout)

	if err := runEventLoop(ctx, h, io, logger); err != nil {
		logger.Printf("Error in event loop: %v", err)
		if publisher != nil {
			publisher.Stop(2 * time.Second)
		}
		os.Exit(1)
	}

	if publisher != nil {
		publisher.Stop(2 * time.Second)
	}
}

type routerHandler interface {
	Handle(input handler.RouterInputMsg) (handler.OutputMsg, error)
}

type routerIO interface {
	Input(input []byte) (handler.RouterInputMsg, error)
	Output(msg handler.OutputMsg) ([]byte, error)
}

func runEventLoop(ctx context.Context, h routerHandler, io routerIO, logger *log.Logger) error {
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

func processLine(line []byte, h routerHandler, io routerIO, logger *log.Logger) []byte {
	inputMsg, err := io.Input(line)
	if err != nil {
		logger.Printf("Invalid input: %v", err)
		return safeOutput(io, handler.RejectMalformed(), logger)
	}

	outputMsg, err := h.Handle(inputMsg)
	if err != nil {
		logger.Printf("Handler error: %v", err)
		return safeOutput(io, handler.RejectInternalWithError("", err), logger)
	}

	resp, err := io.Output(outputMsg)
	if err != nil {
		logger.Printf("Failed to serialize output: %v", err)
		return safeOutput(io, handler.RejectInternal(""), logger)
	}
	return resp
}

func safeOutput(io routerIO, msg handler.OutputMsg, logger *log.Logger) []byte {
	resp, err := io.Output(msg)
	if err != nil {
		logger.Printf("Critical: failed to serialize fallback response: %v", err)
		return []byte("\n")
	}
	return resp
}
