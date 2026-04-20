// Package quarantine implements the async publisher that forwards
// non-whitelisted Nostr events to the quarantine relay.
//
// The plugin hot path must never block on this — Enqueue drops on full queue,
// and a single background goroutine maintains the WS connection and publishes.
// See quarantine/SPEC.md §6.4.
package quarantine

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const (
	// DefaultBufferSize is the enqueue channel size when config omits one.
	DefaultBufferSize = 10000

	// DefaultPublishTimeout caps one Relay.Publish call.
	DefaultPublishTimeout = 5 * time.Second

	// DefaultMetricsInterval controls how often the publisher logs counters.
	DefaultMetricsInterval = 60 * time.Second

	initialReconnectDelay = 500 * time.Millisecond
	maxReconnectDelay     = 30 * time.Second
)

// Metrics is a snapshot of publisher counters.
type Metrics struct {
	Enqueued       uint64
	Dropped        uint64
	Published      uint64
	PublishErrors  uint64
	ReconnectCount uint64
	Connected      bool
}

// Publisher asynchronously forwards events to a Nostr relay.
type Publisher struct {
	relayURL        string
	publishTimeout  time.Duration
	metricsInterval time.Duration
	logger          *log.Logger

	queue chan nostr.Event
	done  chan struct{}

	enqueued       atomic.Uint64
	dropped        atomic.Uint64
	published      atomic.Uint64
	publishErrors  atomic.Uint64
	reconnectCount atomic.Uint64
	connected      atomic.Bool

	stopOnce sync.Once
}

// Config configures a Publisher.
type Config struct {
	RelayURL        string
	BufferSize      int
	PublishTimeout  time.Duration
	MetricsInterval time.Duration
}

// NewPublisher constructs a Publisher. Call Start to begin draining.
func NewPublisher(cfg Config, logger *log.Logger) *Publisher {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = DefaultBufferSize
	}
	if cfg.PublishTimeout <= 0 {
		cfg.PublishTimeout = DefaultPublishTimeout
	}
	if cfg.MetricsInterval <= 0 {
		cfg.MetricsInterval = DefaultMetricsInterval
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Publisher{
		relayURL:        cfg.RelayURL,
		publishTimeout:  cfg.PublishTimeout,
		metricsInterval: cfg.MetricsInterval,
		logger:          logger,
		queue:           make(chan nostr.Event, cfg.BufferSize),
		done:            make(chan struct{}),
	}
}

// Enqueue attempts to enqueue an event for publication. Never blocks.
// Returns true if the event was enqueued; false if the queue was full.
func (p *Publisher) Enqueue(evt nostr.Event) bool {
	select {
	case p.queue <- evt:
		p.enqueued.Add(1)
		return true
	default:
		p.dropped.Add(1)
		return false
	}
}

// Start begins the background drain + metrics goroutines.
// The returned goroutines exit when ctx is cancelled or Stop is called.
func (p *Publisher) Start(ctx context.Context) {
	go p.runDrain(ctx)
	go p.runMetrics(ctx)
}

// Stop cancels the drain loop and waits up to timeout for it to finish.
func (p *Publisher) Stop(timeout time.Duration) {
	p.stopOnce.Do(func() { close(p.done) })
	if timeout <= 0 {
		return
	}
	select {
	case <-time.After(timeout):
	}
}

// Metrics returns a snapshot of current counters.
func (p *Publisher) Metrics() Metrics {
	return Metrics{
		Enqueued:       p.enqueued.Load(),
		Dropped:        p.dropped.Load(),
		Published:      p.published.Load(),
		PublishErrors:  p.publishErrors.Load(),
		ReconnectCount: p.reconnectCount.Load(),
		Connected:      p.connected.Load(),
	}
}

// runDrain owns the single WS connection and publishes queued events.
func (p *Publisher) runDrain(ctx context.Context) {
	var relay *nostr.Relay
	defer func() {
		if relay != nil {
			_ = relay.Close()
		}
	}()

	delay := initialReconnectDelay
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.done:
			return
		default:
		}

		if relay == nil || !relay.IsConnected() {
			newRelay, err := p.connect(ctx)
			if err != nil {
				p.logger.Printf("quarantine: connect to %s failed: %v (retry in %s)", p.relayURL, err, delay)
				if !sleepCancel(ctx, p.done, delay) {
					return
				}
				delay = nextBackoff(delay)
				continue
			}
			relay = newRelay
			p.connected.Store(true)
			p.reconnectCount.Add(1)
			delay = initialReconnectDelay
			p.logger.Printf("quarantine: connected to %s", p.relayURL)
		}

		select {
		case <-ctx.Done():
			return
		case <-p.done:
			return
		case evt := <-p.queue:
			if err := p.publishOne(ctx, relay, evt); err != nil {
				p.publishErrors.Add(1)
				p.logger.Printf("quarantine: publish id=%s failed: %v", evt.ID, err)
				// Force reconnect on next iteration.
				_ = relay.Close()
				relay = nil
				p.connected.Store(false)
			} else {
				p.published.Add(1)
			}
		}
	}
}

func (p *Publisher) connect(ctx context.Context) (*nostr.Relay, error) {
	dialCtx, cancel := context.WithTimeout(ctx, p.publishTimeout)
	defer cancel()
	return nostr.RelayConnect(dialCtx, p.relayURL)
}

func (p *Publisher) publishOne(ctx context.Context, relay *nostr.Relay, evt nostr.Event) error {
	pubCtx, cancel := context.WithTimeout(ctx, p.publishTimeout)
	defer cancel()
	return relay.Publish(pubCtx, evt)
}

func (p *Publisher) runMetrics(ctx context.Context) {
	ticker := time.NewTicker(p.metricsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.done:
			return
		case <-ticker.C:
			m := p.Metrics()
			p.logger.Printf("quarantine metrics: enqueued=%d dropped=%d published=%d publishErrors=%d reconnects=%d connected=%t queueDepth=%d",
				m.Enqueued, m.Dropped, m.Published, m.PublishErrors, m.ReconnectCount, m.Connected, len(p.queue))
		}
	}
}

// nextBackoff doubles the delay up to maxReconnectDelay.
func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > maxReconnectDelay {
		return maxReconnectDelay
	}
	return d
}

// sleepCancel sleeps for d, returning false if ctx or done fires first.
func sleepCancel(ctx context.Context, done <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-done:
		return false
	case <-t.C:
		return true
	}
}
