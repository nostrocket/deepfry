package forwarder

import (
	"context"
	"fmt"

	"event-forwarder/pkg/telemetry"

	"github.com/nbd-wtf/go-nostr"
)

// realtimeStrategy implements SyncStrategy for realtime streaming mode.
type realtimeStrategy struct{ f *Forwarder }

func NewRealtimeStrategy(f *Forwarder) SyncStrategy { return &realtimeStrategy{f: f} }

func (s *realtimeStrategy) Mode() string { return SyncModeRealtime }

func (s *realtimeStrategy) Run(ctx context.Context) error {
	f := s.f
	f.logger.Printf("starting real-time sync mode for relay %s", f.cfg.SourceRelayURL)

	filter := nostr.Filter{Limit: f.cfg.Sync.MaxBatch}
	sub, err := f.sourceRelay.Subscribe(ctx, nostr.Filters{filter})
	if err != nil {
		f.emitTelemetryError(err, "realtime_subscribe")
		f.switchToWindowedMode("realtime_subscribe_failed")
		return f.fallbackToWindowedMode(ctx)
	}

	f.logger.Printf("real-time event stream established for %s (batch_limit: %d)", 
		f.cfg.SourceRelayURL, f.cfg.Sync.MaxBatch)
	for {
		select {
		case <-ctx.Done():
			if sub != nil {
				safeCloseSubscription(sub)
			}
			return ctx.Err()
		case <-sub.ClosedReason:
			f.logger.Printf("real-time subscription closed by relay %s, attempting to reconnect", f.cfg.SourceRelayURL)
			f.emitTelemetryMsgSev(fmt.Sprintf("subscription closed by relay %s", f.cfg.SourceRelayURL), 
				"realtime_disconnect", telemetry.ErrorSeverityWarning)
			f.connectRelays(ctx)
			return f.realtimeLoop(ctx)
		case <-sub.EndOfStoredEvents:
			// ignore EOSE in realtime
			continue
		case event, ok := <-sub.Events:
			if !ok {
				f.logger.Printf("real-time event channel closed for %s, attempting to reconnect", f.cfg.SourceRelayURL)
				f.emitTelemetryMsgSev(fmt.Sprintf("event channel closed for relay %s", f.cfg.SourceRelayURL), 
					"realtime_disconnect", telemetry.ErrorSeverityWarning)
				f.connectRelays(ctx)
				return f.realtimeLoop(ctx)
			}
			if err := f.processRealtimeEvent(ctx, event); err != nil {
				continue
			}
			f.eventsSinceUpdate++
			f.emitTelemetryRealtimeProgress(f.eventsSinceUpdate)
			if f.eventsSinceUpdate >= EventsPerWindowUpdate {
				if err := f.updateRealtimeWindow(ctx); err != nil {
					f.logger.Printf("error updating real-time window after %d events: %v", 
						f.eventsSinceUpdate, err)
				}
				f.eventsSinceUpdate = 0
				f.emitTelemetryRealtimeProgress(f.eventsSinceUpdate)
			}
		}
	}
}

func safeCloseSubscription(sub *nostr.Subscription) {
	defer func() { _ = recover() }()
	sub.Close()
}
