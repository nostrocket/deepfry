package main

import (
	"bufio"
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"whitelist-plugin/pkg/bloomgate"
	"whitelist-plugin/pkg/config"
	"whitelist-plugin/pkg/handler"
	"whitelist-plugin/pkg/quarantine"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stderr, "[bloom-plugin] ", log.LstdFlags)

	cfg, err := config.LoadBloomConfig()
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	// Construct the BloomChecker — ready gate opens on first filter store (GATE-06/D-06).
	checker := bloomgate.NewBloomChecker(logger)

	// Construct and start the BloomFetcher: disk-first cold start (D-04) then background
	// periodic conditional-GET ticker. The fetcher is the single writer of checker.Store.
	// The quarantine side-channel adds zero per-event HTTP; the fetcher path is unchanged.
	fetcher := bloomgate.NewBloomFetcher(checker, cfg, logger)
	fetcher.Start()
	defer fetcher.Stop()

	// Quarantine side-channel: when enabled, forward not-in-set kind-0/1/3 events
	// fire-and-forget to the quarantine relay. Reuses the router's Publisher + the
	// checker-agnostic RouterHandler over the BloomChecker (M24-QUARANTINE-BLOOM).
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
		logger.Printf("Quarantine disabled by config; bloom plugin will behave byte-identically to legacy (reject-only)")
	}

	// RouterHandler is checker-agnostic: it takes any handler.Checker, and the
	// BloomChecker satisfies that interface. RouterIOAdapter parses the full event
	// (kind + content) so the heuristics gate can run. The mainline reject decision
	// is unchanged regardless of the quarantine outcome.
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
