package whitelist

import (
	"context"
	"log"
	"sync"
	"time"
	"whitelist-plugin/pkg/repository"
)

type WhitelistRefresher struct {
	whitelist  *Whitelist
	keyRepo    repository.KeyRepository
	interval   time.Duration
	ctx        context.Context
	cancel     context.CancelFunc
	waitGroup  sync.WaitGroup
	retryCount int
	logger     *log.Logger
}

func NewWhitelistRefresher(ctx context.Context, keyRepo repository.KeyRepository, interval time.Duration, retryCount int, logger *log.Logger) *WhitelistRefresher {
	ctx, cancel := context.WithCancel(ctx)
	r := &WhitelistRefresher{
		whitelist:  NewWhiteList([][32]byte{}),
		keyRepo:    keyRepo,
		interval:   interval,
		ctx:        ctx,
		cancel:     cancel,
		retryCount: retryCount,
		logger:     logger,
	}
	return r
}

func (r *WhitelistRefresher) Start() {
	// Initial refresh
	r.refresh()

	// Start periodic refresh
	r.waitGroup.Add(1)
	go func() {
		defer r.waitGroup.Done()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-ticker.C:
				r.refresh()
			}
		}
	}()
}

func (r *WhitelistRefresher) Stop() {
	r.cancel()
	r.waitGroup.Wait()
}

func (r *WhitelistRefresher) refresh() {
	for attempt := 0; attempt <= r.retryCount; attempt++ {
		keys, err := r.keyRepo.GetAll(r.ctx)
		if err != nil {
			// If context was cancelled, stop retrying immediately
			if r.ctx.Err() != nil {
				r.logger.Printf("Refresh cancelled")
				return
			}
			r.logger.Printf("Failed to fetch keys (attempt %d/%d): %v", attempt+1, r.retryCount+1, err)
			if attempt < r.retryCount {
				select {
				case <-r.ctx.Done():
					r.logger.Printf("Refresh cancelled during retry backoff")
					return
				case <-time.After(time.Second * time.Duration(attempt+1)):
				}
			}
			continue
		}
		r.whitelist.UpdateKeys(keys)
		r.logger.Printf("whitelist refreshed with %d keys", len(keys))
		return
	}
	r.logger.Printf("Refresh failed after %d attempts", r.retryCount+1)
}

func (r *WhitelistRefresher) Whitelist() *Whitelist {
	return r.whitelist
}
