package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
	"whitelist-plugin/pkg/bloom"
	"whitelist-plugin/pkg/config"
	"whitelist-plugin/pkg/repository"
	"whitelist-plugin/pkg/server"
	"whitelist-plugin/pkg/whitelist"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stderr, "[whitelist-server] ", log.LstdFlags)

	cfg, err := config.LoadServerConfig()
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	// Page size for DQL uid-cursor pagination. Larger pages mean far fewer
	// round-trips over the ~1.5M Profile set; 10k/page loads in well under a
	// minute against a local Dgraph.
	const dgraphPageSize = 10000
	keyRepo := repository.NewGraphQLRepository(
		cfg.DgraphGraphQLURL, dgraphPageSize, logger,
		cfg.HTTPTimeout, cfg.IdleConnTimeout, cfg.QueryTimeout,
	)
	refresher := whitelist.NewWhitelistRefresher(ctx, keyRepo, cfg.RefreshInterval, cfg.RefreshRetryCount, logger)

	// Start HTTP server immediately so /health can respond during loading
	srv := server.NewWhitelistServer(refresher.Whitelist(), cfg.ServerListenAddr, cfg.Debug, logger)

	go func() {
		if err := srv.ListenAndServe(ctx); err != nil {
			logger.Fatalf("Server error: %v", err)
		}
	}()

	// Register bloom rebuild callback before Start() so the initial synchronous
	// refresh builds the first filter in lockstep with the whitelist (SRV-01, D-01).
	refresher.SetOnRefresh(func(keys [][32]byte) {
		// Rebuild bloom filter from the refreshed key set (D-01, D-09).
		b := bloom.NewBuilder(uint(len(keys)), cfg.BloomFPRate)
		for _, k := range keys {
			b.Add(k)
		}
		f, err := b.Build()
		if err != nil {
			logger.Printf("bloom build failed: %v", err)
			return // no swap — prior filter preserved (D-02)
		}
		if err := srv.SwapFilter(f); err != nil {
			logger.Printf("bloom serialize failed: %v", err)
			return // no stats update — prior state preserved (D-02)
		}
		srv.SetStats(len(keys), time.Now()) // keep /stats live per refresh (D-10)
	})

	// Block until initial whitelist is loaded
	logger.Printf("Loading whitelist from %s ...", cfg.DgraphGraphQLURL)
	refresher.Start()
	defer refresher.Stop()

	srv.SetReady(refresher.Whitelist().Len())
	logger.Printf("Whitelist loaded with %d entries", refresher.Whitelist().Len())

	// Block until shutdown
	<-ctx.Done()
}
