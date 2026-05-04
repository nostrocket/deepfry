// quarantine-rescue scans the quarantine StrFry LMDB for events whose
// author is now on the live whitelist, forwards those events to the
// main StrFry relay, and deletes them from quarantine on successful
// forward. Designed to run on the strfry host; calls `docker exec`
// against the quarantine container and uses the same whitelist
// endpoint the live plugin uses.
//
// See quarantine/SPEC.md for background on the quarantine subsystem.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"quarantine-rescuer/internal/deleter"
	"quarantine-rescuer/internal/exporter"
	"quarantine-rescuer/internal/forwarder"
	"quarantine-rescuer/internal/runner"
	"quarantine-rescuer/internal/whitelist"
)

// Build metadata, populated via -ldflags. See Makefile.
var (
	Version = "dev"
	Commit  = "unknown"
	Built   = "unknown"
)

type flags struct {
	dryRun               bool
	limit                int
	batchSize            int
	forwardConcurrency   int
	whitelistConcurrency int
	mainRelay            string
	quarantineContainer  string
	quarantineConfigPath string
	logLevel             string
	publishTimeout       time.Duration
	showVersion          bool
}

func parseFlags() *flags {
	f := &flags{}
	flag.BoolVar(&f.dryRun, "dry-run", false, "Skip publish and delete; only report what would happen.")
	flag.IntVar(&f.limit, "limit", 0, "Stop after processing this many events from the export. 0 = unlimited.")
	flag.IntVar(&f.batchSize, "batch-size", deleter.DefaultBatchSize, "Number of event ids per strfry delete invocation.")
	flag.IntVar(&f.forwardConcurrency, "forward-concurrency", 4, "Parallel pubkeys forwarded at once. Events for one pubkey are always sequential.")
	flag.IntVar(&f.whitelistConcurrency, "whitelist-concurrency", 8, "Parallel /check requests against the whitelist server.")
	flag.StringVar(&f.mainRelay, "main-relay", "ws://localhost:7777", "WebSocket URL of the main StrFry relay.")
	flag.StringVar(&f.quarantineContainer, "quarantine-container", "strfry-quarantine", "Docker container name running the quarantine StrFry instance.")
	flag.StringVar(&f.quarantineConfigPath, "quarantine-config", "/etc/strfry.conf", "Path to strfry.conf inside the quarantine container.")
	flag.StringVar(&f.logLevel, "log-level", "info", "Log level: debug, info, warn, error.")
	flag.DurationVar(&f.publishTimeout, "publish-timeout", forwarder.DefaultPublishTimeout, "Timeout for a single publish to the main relay.")
	flag.BoolVar(&f.showVersion, "version", false, "Print version and exit.")
	flag.Parse()
	return f
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func main() {
	f := parseFlags()
	if f.showVersion {
		fmt.Printf("quarantine-rescue version=%s commit=%s built=%s\n", Version, Commit, Built)
		return
	}

	logger := newLogger(f.logLevel)
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, f, logger); err != nil {
		logger.Error("rescue failed", "err", err)
		os.Exit(1)
	}
}

type summary struct {
	pubkeysSeen          int
	pubkeysWhitelisted   int
	eventsExported       int
	eventsToForward      int
	eventsForwarded      int
	eventsFailedForward  int
	eventsDeleted        int
	eventsFailedDelete   int
	whitelistCacheMisses int
}

func run(ctx context.Context, f *flags, logger *slog.Logger) error {
	start := time.Now()

	cfg, err := whitelist.LoadConfig()
	if err != nil {
		return fmt.Errorf("load whitelist config: %w", err)
	}
	logger.Info("loaded whitelist config", "server_url", cfg.ServerURL, "check_timeout", cfg.CheckTimeout)

	wlClient := whitelist.NewClient(cfg.ServerURL, cfg.CheckTimeout, logger)
	healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
	if err := wlClient.CheckHealth(healthCtx); err != nil {
		healthCancel()
		return fmt.Errorf("whitelist server not reachable; aborting before any work: %w", err)
	}
	healthCancel()

	r := runner.Exec{}

	// Phase 1: export and group by pubkey.
	logger.Info("phase 1: exporting from quarantine",
		"container", f.quarantineContainer, "config", f.quarantineConfigPath)
	eventsByPubkey, totalEvents, err := collect(ctx, r, f.quarantineContainer, f.quarantineConfigPath, f.limit, logger)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	sum := summary{
		pubkeysSeen:    len(eventsByPubkey),
		eventsExported: totalEvents,
	}
	logger.Info("phase 1 complete", "pubkeys", sum.pubkeysSeen, "events", sum.eventsExported)
	if len(eventsByPubkey) == 0 {
		logSummary(logger, &sum, time.Since(start))
		return nil
	}

	// Phase 2: whitelist check, drop non-whitelisted.
	logger.Info("phase 2: checking whitelist", "pubkeys", len(eventsByPubkey))
	whitelisted := filterWhitelisted(ctx, wlClient, eventsByPubkey, f.whitelistConcurrency, logger)
	sum.pubkeysWhitelisted = len(whitelisted)
	for _, evts := range whitelisted {
		sum.eventsToForward += len(evts)
	}
	logger.Info("phase 2 complete",
		"pubkeys_whitelisted", sum.pubkeysWhitelisted,
		"events_to_forward", sum.eventsToForward)
	if sum.eventsToForward == 0 {
		logSummary(logger, &sum, time.Since(start))
		return nil
	}

	if f.dryRun {
		logger.Info("dry-run: skipping forward and delete")
		logSummary(logger, &sum, time.Since(start))
		return nil
	}

	// Phase 3: forward to main relay.
	logger.Info("phase 3: forwarding to main relay", "relay", f.mainRelay)
	fwd := forwarder.New(f.mainRelay, f.forwardConcurrency, f.publishTimeout, logger)
	fwdRes := fwd.Forward(ctx, whitelisted)
	sum.eventsForwarded = len(fwdRes.SuccessIDs)
	sum.eventsFailedForward = len(fwdRes.FailedIDs)
	logger.Info("phase 3 complete",
		"forwarded", sum.eventsForwarded, "failed", sum.eventsFailedForward)
	if sum.eventsForwarded == 0 {
		logSummary(logger, &sum, time.Since(start))
		return nil
	}

	// Phase 4: delete only the successfully forwarded events.
	logger.Info("phase 4: deleting from quarantine", "ids", sum.eventsForwarded)
	del := deleter.New(r, f.quarantineContainer, f.quarantineConfigPath, f.batchSize, logger)
	delRes := del.DeleteByIDs(ctx, fwdRes.SuccessIDs)
	sum.eventsDeleted = len(delRes.Deleted)
	sum.eventsFailedDelete = len(delRes.Failed)
	logger.Info("phase 4 complete", "deleted", sum.eventsDeleted, "failed", sum.eventsFailedDelete)

	logSummary(logger, &sum, time.Since(start))
	return nil
}

func collect(ctx context.Context, r runner.Runner, container, configPath string, limit int, logger *slog.Logger) (map[string][]exporter.RawEvent, int, error) {
	events, errs := exporter.Stream(ctx, r, container, configPath, logger)
	byPubkey := make(map[string][]exporter.RawEvent)
	total := 0
	for ev := range events {
		byPubkey[ev.PubKey] = append(byPubkey[ev.PubKey], ev)
		total++
		if limit > 0 && total >= limit {
			logger.Info("export limit reached; stopping early", "limit", limit)
			break
		}
	}
	if err, ok := <-errs; ok {
		return byPubkey, total, err
	}
	return byPubkey, total, nil
}

func filterWhitelisted(ctx context.Context, c *whitelist.Client, eventsByPubkey map[string][]exporter.RawEvent, concurrency int, logger *slog.Logger) map[string][]exporter.RawEvent {
	if concurrency <= 0 {
		concurrency = 8
	}
	type result struct {
		pubkey      string
		whitelisted bool
	}

	pubkeys := make([]string, 0, len(eventsByPubkey))
	for pk := range eventsByPubkey {
		pubkeys = append(pubkeys, pk)
	}

	sem := make(chan struct{}, concurrency)
	results := make(chan result, len(pubkeys))
	var wg sync.WaitGroup
	for _, pk := range pubkeys {
		wg.Add(1)
		sem <- struct{}{}
		go func(pubkey string) {
			defer wg.Done()
			defer func() { <-sem }()
			results <- result{pubkey: pubkey, whitelisted: c.IsWhitelisted(ctx, pubkey)}
		}(pk)
	}
	wg.Wait()
	close(results)

	out := make(map[string][]exporter.RawEvent)
	for r := range results {
		if r.whitelisted {
			out[r.pubkey] = eventsByPubkey[r.pubkey]
			logger.Debug("whitelisted pubkey", "pubkey", r.pubkey, "events", len(eventsByPubkey[r.pubkey]))
		}
	}
	return out
}

func logSummary(logger *slog.Logger, s *summary, elapsed time.Duration) {
	logger.Info("rescue summary",
		"pubkeys_seen", s.pubkeysSeen,
		"pubkeys_whitelisted", s.pubkeysWhitelisted,
		"events_exported", s.eventsExported,
		"events_to_forward", s.eventsToForward,
		"events_forwarded", s.eventsForwarded,
		"events_failed_forward", s.eventsFailedForward,
		"events_deleted", s.eventsDeleted,
		"events_failed_delete", s.eventsFailedDelete,
		"duration_ms", elapsed.Milliseconds(),
	)
}
