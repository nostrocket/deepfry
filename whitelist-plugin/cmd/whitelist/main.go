package main

import (
	"bufio"
	"log"
	"os"
	"time"
	"whitelist-plugin/pkg/repository"
	"whitelist-plugin/pkg/whitelist"
)

func main() {
	// Initialize logger for errors (stderr to avoid mixing with stdout responses)
	logger := log.New(os.Stderr, "[whitelist-plugin] ", log.LstdFlags)

	// Initialize repository (using simple repository for now)
	keyRepo := repository.NewSimpleRepository()

	// Initialize whitelist refresher with 5-minute refresh interval
	refresher := whitelist.NewWhitelistRefresher(
		keyRepo,
		5*time.Minute, // refresh interval
		3,             // retry count
		logger,
	)

	// Start background refresh
	refresher.Start()
	defer refresher.Stop()

	// for _, k := range refresher.Whitelist.Keys() {
	// 	logger.Println(k)
	// }

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()

		os.Stdout.Write(line)
	}

	// Check for scanner errors
	if err := scanner.Err(); err != nil {
		logger.Printf("Error reading stdin: %v", err)
		os.Exit(1)
	}
}
