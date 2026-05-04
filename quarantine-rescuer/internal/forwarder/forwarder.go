// Package forwarder publishes rescued events to the main StrFry relay
// over NIP-01 WebSocket. It deliberately publishes one pubkey's events
// sequentially in oldest-first order — replaceable kinds (kind 0
// profile, kind 3 follows) are last-write-wins on the relay, and
// arriving out of order would let an older copy clobber a newer one.
//
// Across pubkeys we run a small worker pool to keep latency bounded
// without overwhelming the relay or the host.
package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"

	"quarantine-rescuer/internal/exporter"
)

// DefaultPublishTimeout caps a single relay.Publish call.
const DefaultPublishTimeout = 5 * time.Second

// Result reports the outcome of a forwarding pass.
type Result struct {
	SuccessIDs []string
	FailedIDs  []string
}

// Forwarder publishes events to a relay through one or more workers.
type Forwarder struct {
	relayURL       string
	workers        int
	publishTimeout time.Duration
	logger         *slog.Logger
}

func New(relayURL string, workers int, publishTimeout time.Duration, logger *slog.Logger) *Forwarder {
	if workers <= 0 {
		workers = 4
	}
	if publishTimeout <= 0 {
		publishTimeout = DefaultPublishTimeout
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Forwarder{
		relayURL:       relayURL,
		workers:        workers,
		publishTimeout: publishTimeout,
		logger:         logger,
	}
}

// Forward publishes every event in eventsByPubkey to the configured
// relay. Each worker holds its own dedicated relay connection.
//
// Events for the same pubkey are always handled by a single worker in
// oldest-first order. Events for different pubkeys can run in parallel.
func (f *Forwarder) Forward(ctx context.Context, eventsByPubkey map[string][]exporter.RawEvent) Result {
	type work struct {
		pubkey string
		events []exporter.RawEvent
	}

	jobs := make(chan work)
	var (
		mu      sync.Mutex
		success []string
		failed  []string
	)

	addSuccess := func(id string) {
		mu.Lock()
		success = append(success, id)
		mu.Unlock()
	}
	addFailed := func(id string) {
		mu.Lock()
		failed = append(failed, id)
		mu.Unlock()
	}

	worker := func(workerID int) {
		relay, err := f.connect(ctx)
		if err != nil {
			f.logger.Error("forwarder: worker could not connect; failing assigned events",
				"worker", workerID, "relay", f.relayURL, "err", err)
			for w := range jobs {
				for _, ev := range w.events {
					addFailed(ev.ID)
				}
			}
			return
		}
		defer relay.Close()

		for w := range jobs {
			for _, raw := range w.events {
				select {
				case <-ctx.Done():
					addFailed(raw.ID)
					continue
				default:
				}
				evt, err := decodeEvent(raw.Raw)
				if err != nil {
					f.logger.Warn("forwarder: cannot decode event; skipping",
						"event_id", raw.ID, "err", err)
					addFailed(raw.ID)
					continue
				}
				pubCtx, cancel := context.WithTimeout(ctx, f.publishTimeout)
				err = relay.Publish(pubCtx, *evt)
				cancel()
				if err != nil {
					f.logger.Warn("forwarder: publish rejected",
						"worker", workerID, "event_id", raw.ID,
						"pubkey", raw.PubKey, "kind", raw.Kind, "err", err)
					addFailed(raw.ID)
					continue
				}
				addSuccess(raw.ID)
			}
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < f.workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id)
		}(i)
	}

	for pk, evts := range eventsByPubkey {
		sortedEvts := append([]exporter.RawEvent(nil), evts...)
		sort.SliceStable(sortedEvts, func(i, j int) bool {
			return sortedEvts[i].CreatedAt < sortedEvts[j].CreatedAt
		})
		select {
		case jobs <- work{pubkey: pk, events: sortedEvts}:
		case <-ctx.Done():
			for _, ev := range sortedEvts {
				addFailed(ev.ID)
			}
		}
	}
	close(jobs)
	wg.Wait()

	return Result{SuccessIDs: success, FailedIDs: failed}
}

func (f *Forwarder) connect(ctx context.Context) (*nostr.Relay, error) {
	dialCtx, cancel := context.WithTimeout(ctx, f.publishTimeout)
	defer cancel()
	return nostr.RelayConnect(dialCtx, f.relayURL)
}

func decodeEvent(raw []byte) (*nostr.Event, error) {
	var evt nostr.Event
	if err := json.Unmarshal(raw, &evt); err != nil {
		return nil, fmt.Errorf("unmarshal event: %w", err)
	}
	return &evt, nil
}
