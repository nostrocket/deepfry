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

	keyRepo := repository.NewGraphQLRepository(
		cfg.DgraphGraphQLURL, 1000, logger,
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
