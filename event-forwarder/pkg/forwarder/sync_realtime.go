package forwarder

import (
	"context"

	"event-forwarder/pkg/telemetry"

	"github.com/nbd-wtf/go-nostr"
)

// realtimeStrategy implements SyncStrategy for realtime streaming mode.
type realtimeStrategy struct{ f *Forwarder }

func NewRealtimeStrategy(f *Forwarder) SyncStrategy { return &realtimeStrategy{f: f} }

func (s *realtimeStrategy) Mode() string { return SyncModeRealtime }

func (s *realtimeStrategy) Run(ctx context.Context) error {
	f := s.f
	f.logger.Printf("starting real-time sync mode")

	filter := nostr.Filter{Limit: f.cfg.Sync.MaxBatch}
	sub, err := f.sourceRelay.Subscribe(ctx, nostr.Filters{filter})
	if err != nil {
		f.emitTelemetryError(err, "realtime_subscribe")
		f.switchToWindowedMode("realtime_subscribe_failed")
		return f.fallbackToWindowedMode(ctx)
	}

	f.logger.Printf("real-time event stream established")
	for {
		select {
		case <-ctx.Done():
			if sub != nil {
				safeCloseSubscription(sub)
			}
			return ctx.Err()
		case <-sub.ClosedReason:
			f.logger.Printf("real-time subscription closed by relay, attempting to reconnect")
			f.emitTelemetryMsgSev("subscription closed by relay", "realtime_disconnect", telemetry.ErrorSeverityWarning)
			f.connectRelays(ctx)
			return f.realtimeLoop(ctx)
		case <-sub.EndOfStoredEvents:
			// ignore EOSE in realtime
			continue
		case event, ok := <-sub.Events:
			if !ok {
				f.logger.Printf("real-time event channel closed, attempting to reconnect")
				f.emitTelemetryMsgSev("event channel closed", "realtime_disconnect", telemetry.ErrorSeverityWarning)
				f.connectRelays(ctx)
				return f.realtimeLoop(ctx)
			}
			if event == nil {
				f.emitTelemetryMsgSev("nil event", "realtime_event_validation", telemetry.ErrorSeverityInfo)
				continue
			}
			if err := f.processRealtimeEvent(ctx, event); err != nil {
				continue
			}
			f.eventsSinceUpdate++
			f.emitTelemetryRealtimeProgress(f.eventsSinceUpdate)
			if f.eventsSinceUpdate >= EventsPerWindowUpdate {
				if err := f.updateRealtimeWindow(ctx); err != nil {
					f.logger.Printf("error updating real-time window: %v", err)
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
