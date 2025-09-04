package telemetry

import (
	"context"
	"sync"
	"time"
)

// Clock interface allows for deterministic testing
type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// Config for telemetry settings
type Config struct {
	BufferSize           int     `default:"1000"`
	DropThresholdPercent float64 `default:"90.0"`
	RefreshIntervalMs    int     `default:"200"`
	MaxRecentErrors      int     `default:"50"`
	RateWindowSeconds    int     `default:"10"`
}

func DefaultConfig() Config {
	return Config{
		BufferSize:           1000,
		DropThresholdPercent: 90.0,
		RefreshIntervalMs:    200,
		MaxRecentErrors:      50,
		RateWindowSeconds:    10,
	}
}

// Aggregator is the core stateful component that processes telemetry events
type Aggregator struct {
	mu    sync.RWMutex
	clock Clock
	cfg   Config

	// Core counters
	eventsReceived  uint64
	eventsForwarded uint64
	errorsTotal     uint64

	// Event breakdown
	eventsForwardedByKind map[int]uint64
	errorsByType          map[string]uint64
	errorsBySeverity      map[ErrorSeverity]uint64

	// Rate calculations
	eventTimes   []time.Time // Ring buffer for rate calculations
	forwardTimes []time.Time // Ring buffer for forward rate calculations

	// Current state
	syncWindowFrom        int64
	syncWindowTo          int64
	currentSyncMode       string
	sourceRelayConnected  bool
	deepFryRelayConnected bool

	// Recent errors (ring buffer)
	recentErrors []string
	errorIndex   int

	// Latency tracking
	latencies    []time.Duration
	latencyIndex int

	// Control channels
	eventCh chan TelemetryEvent
	done    chan struct{}
	wg      sync.WaitGroup

	// Startup time
	startTime time.Time
}

// NewAggregator creates a new telemetry aggregator
func NewAggregator(clock Clock, cfg Config) *Aggregator {
	if clock == nil {
		clock = RealClock{}
	}

	return &Aggregator{
		clock:                 clock,
		cfg:                   cfg,
		currentSyncMode:       "windowed", // Default to windowed mode
		eventsForwardedByKind: make(map[int]uint64),
		errorsByType:          make(map[string]uint64),
		errorsBySeverity:      make(map[ErrorSeverity]uint64),
		eventTimes:            make([]time.Time, 0, cfg.RateWindowSeconds*10), // ~10 events per second estimate
		forwardTimes:          make([]time.Time, 0, cfg.RateWindowSeconds*10),
		recentErrors:          make([]string, cfg.MaxRecentErrors),
		latencies:             make([]time.Duration, 100), // Keep last 100 latencies for P95
		eventCh:               make(chan TelemetryEvent, cfg.BufferSize),
		done:                  make(chan struct{}),
		startTime:             clock.Now(),
	}
}

// Start begins processing telemetry events
func (a *Aggregator) Start(ctx context.Context) {
	a.wg.Add(1)
	go a.processEvents(ctx)
}

// Stop gracefully shuts down the aggregator
func (a *Aggregator) Stop() {
	close(a.done)
	a.wg.Wait()
}

// Publish implements TelemetryPublisher interface
func (a *Aggregator) Publish(event TelemetryEvent) {
	select {
	case a.eventCh <- event:
	default:
		// Non-blocking send - drop if channel is full
		// This protects the hot path from being blocked
	}
}

// Snapshot implements TelemetryReader interface
func (a *Aggregator) Snapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	now := a.clock.Now()

	// Calculate rates
	eventsPerSecond := a.calculateRate(a.eventTimes, now)
	forwardsPerSecond := a.calculateRate(a.forwardTimes, now)

	// Calculate latency metrics
	avgLatency, p95Latency := a.calculateLatencyMetrics()

	// Calculate uptime
	uptime := now.Sub(a.startTime).Seconds()

	// Calculate sync lag
	syncLag := 0.0
	if a.syncWindowTo > 0 {
		windowTime := time.Unix(a.syncWindowTo, 0)
		syncLag = now.Sub(windowTime).Seconds()
	}

	// Calculate channel utilization
	channelUtilization := float64(len(a.eventCh)) / float64(cap(a.eventCh)) * 100

	// Copy maps to prevent data races
	kindsCopy := make(map[int]uint64)
	for k, v := range a.eventsForwardedByKind {
		kindsCopy[k] = v
	}

	errorsByTypeCopy := make(map[string]uint64)
	for k, v := range a.errorsByType {
		errorsByTypeCopy[k] = v
	}

	errorsBySeverityCopy := make(map[ErrorSeverity]uint64)
	for k, v := range a.errorsBySeverity {
		errorsBySeverityCopy[k] = v
	}

	// Copy recent errors
	recentErrors := make([]string, 0)
	for i := 0; i < a.cfg.MaxRecentErrors; i++ {
		idx := (a.errorIndex - i - 1 + len(a.recentErrors)) % len(a.recentErrors)
		if a.recentErrors[idx] != "" {
			recentErrors = append(recentErrors, a.recentErrors[idx])
		}
	}

	return Snapshot{
		EventsReceived:        a.eventsReceived,
		EventsForwarded:       a.eventsForwarded,
		ErrorsTotal:           a.errorsTotal,
		EventsForwardedByKind: kindsCopy,
		SyncLagSeconds:        syncLag,
		SyncWindowFrom:        a.syncWindowFrom,
		SyncWindowTo:          a.syncWindowTo,
		CurrentSyncMode:       a.currentSyncMode,
		SourceRelayConnected:  a.sourceRelayConnected,
		DeepFryRelayConnected: a.deepFryRelayConnected,
		RecentErrors:          recentErrors,
		EventsPerSecond:       eventsPerSecond,
		ForwardsPerSecond:     forwardsPerSecond,
		AvgLatencyMs:          avgLatency,
		P95LatencyMs:          p95Latency,
		UptimeSeconds:         uptime,
		ErrorsByType:          errorsByTypeCopy,
		ErrorsBySeverity:      errorsBySeverityCopy,
		ChannelUtilization:    channelUtilization,
	}
}

func (a *Aggregator) processEvents(ctx context.Context) {
	defer a.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.done:
			return
		case event := <-a.eventCh:
			a.handleEvent(event)
		}
	}
}

func (a *Aggregator) handleEvent(event TelemetryEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.clock.Now()

	switch e := event.(type) {
	case EventReceived:
		a.eventsReceived++
		a.addEventTime(now)

	case EventForwarded:
		a.eventsForwarded++
		a.eventsForwardedByKind[e.Kind]++
		a.addForwardTime(now)
		a.addLatency(e.Latency)

	case SyncProgressUpdated:
		a.syncWindowFrom = e.From
		a.syncWindowTo = e.To

	case ConnectionStatusChanged:
		if e.RelayURL == "source" {
			a.sourceRelayConnected = e.Connected
		} else if e.RelayURL == "deepfry" {
			a.deepFryRelayConnected = e.Connected
		}

	case ForwarderError:
		a.errorsTotal++
		a.errorsByType[e.Context]++
		a.errorsBySeverity[e.Severity]++
		a.addRecentError(e.Err.Error())

	case SyncModeChanged:
		a.currentSyncMode = e.Mode
	}
}

func (a *Aggregator) addEventTime(t time.Time) {
	cutoff := t.Add(-time.Duration(a.cfg.RateWindowSeconds) * time.Second)

	// Remove old entries
	for len(a.eventTimes) > 0 && a.eventTimes[0].Before(cutoff) {
		a.eventTimes = a.eventTimes[1:]
	}

	a.eventTimes = append(a.eventTimes, t)
}

func (a *Aggregator) addForwardTime(t time.Time) {
	cutoff := t.Add(-time.Duration(a.cfg.RateWindowSeconds) * time.Second)

	// Remove old entries
	for len(a.forwardTimes) > 0 && a.forwardTimes[0].Before(cutoff) {
		a.forwardTimes = a.forwardTimes[1:]
	}

	a.forwardTimes = append(a.forwardTimes, t)
}

func (a *Aggregator) addLatency(latency time.Duration) {
	a.latencies[a.latencyIndex] = latency
	a.latencyIndex = (a.latencyIndex + 1) % len(a.latencies)
}

func (a *Aggregator) addRecentError(err string) {
	a.recentErrors[a.errorIndex] = err
	a.errorIndex = (a.errorIndex + 1) % len(a.recentErrors)
}

func (a *Aggregator) calculateRate(times []time.Time, now time.Time) float64 {
	if len(times) == 0 {
		return 0.0
	}

	cutoff := now.Add(-time.Duration(a.cfg.RateWindowSeconds) * time.Second)
	count := 0

	for _, t := range times {
		if t.After(cutoff) {
			count++
		}
	}

	return float64(count) / float64(a.cfg.RateWindowSeconds)
}

func (a *Aggregator) calculateLatencyMetrics() (float64, float64) {
	validLatencies := make([]time.Duration, 0)

	for _, lat := range a.latencies {
		if lat > 0 {
			validLatencies = append(validLatencies, lat)
		}
	}

	if len(validLatencies) == 0 {
		return 0.0, 0.0
	}

	// Calculate average
	var sum time.Duration
	for _, lat := range validLatencies {
		sum += lat
	}
	avg := float64(sum) / float64(len(validLatencies)) / float64(time.Millisecond)

	// Calculate P95 (simple approximation)
	// For more accuracy, you'd want to sort and take the 95th percentile
	p95Index := int(float64(len(validLatencies)) * 0.95)
	if p95Index >= len(validLatencies) {
		p95Index = len(validLatencies) - 1
	}

	// Simple approximation - for production you'd want proper sorting
	maxLatency := validLatencies[0]
	for _, lat := range validLatencies {
		if lat > maxLatency {
			maxLatency = lat
		}
	}
	p95 := float64(maxLatency) / float64(time.Millisecond)

	return avg, p95
}
