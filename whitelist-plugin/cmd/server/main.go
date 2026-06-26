package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
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

	// Block until initial whitelist is loaded
	logger.Printf("Loading whitelist from %s ...", cfg.DgraphGraphQLURL)
	refresher.Start()
	defer refresher.Stop()

	srv.SetReady(refresher.Whitelist().Len())
	logger.Printf("Whitelist loaded with %d entries", refresher.Whitelist().Len())

	// Block until shutdown
	<-ctx.Done()
}
