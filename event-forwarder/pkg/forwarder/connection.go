package forwarder

import (
	"context"
	"fmt"
	"log"
	"time"

	"event-forwarder/pkg/relay"
	"event-forwarder/pkg/telemetry"

	"github.com/nbd-wtf/go-nostr"
)

// connectionManagerImpl implements ConnectionManager using go-nostr relays.
type connectionManagerImpl struct {
	cfgSourceURL  string
	cfgDeepfryURL string
	telemetryEmit func(event telemetry.TelemetryEvent)

	source  relay.Relay
	deepfry relay.Relay
}

// NewConnectionManager creates a ConnectionManager with URLs and a telemetry emitter.
func NewConnectionManager(sourceURL, deepfryURL string, emit func(telemetry.TelemetryEvent)) ConnectionManager {
	return &connectionManagerImpl{cfgSourceURL: sourceURL, cfgDeepfryURL: deepfryURL, telemetryEmit: emit}
}

func (c *connectionManagerImpl) Source() relay.Relay  { return c.source }
func (c *connectionManagerImpl) Deepfry() relay.Relay { return c.deepfry }

func (c *connectionManagerImpl) Connect(ctx context.Context) error {
	c.source = c.attemptConnect(ctx, "source", c.cfgSourceURL)
	c.emitConn("source", true)

	c.deepfry = c.attemptConnect(ctx, "deepfry", c.cfgDeepfryURL)
	c.emitConn("deepfry", true)
	return nil
}

func (c *connectionManagerImpl) Reconnect(ctx context.Context) error {
	c.Close()
	return c.Connect(ctx)
}

func (c *connectionManagerImpl) Close() {
	if c.source != nil {
		_ = c.source.Close()
		c.emitConn("source", false)
	}
	if c.deepfry != nil {
		_ = c.deepfry.Close()
		c.emitConn("deepfry", false)
	}
}

func (c *connectionManagerImpl) attemptConnect(ctx context.Context, name, url string) *nostr.Relay {
	maxAttempts := 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		r, err := nostr.RelayConnect(ctx, url)
		if err != nil {
			c.emitErr(err, fmt.Sprintf("attempt %d/%d failed to connect to %s relay (%s): %s", 
				attempt, maxAttempts, name, url, err), telemetry.ErrorSeverityError)
			c.emitConn(name, false)
		} else {
			c.emitConn(name, true)
			return r
		}
		time.Sleep(time.Second * time.Duration(attempt*2)) // exponential backoff
	}
	log.Panicf("failed to connect to %s relay (%s) after %d attempts", name, url, maxAttempts)
	return nil
}

func (c *connectionManagerImpl) emitConn(relayName string, connected bool) {
	if c.telemetryEmit != nil {
		c.telemetryEmit(telemetry.NewConnectionStatusChanged(relayName, connected))
	}
}

func (c *connectionManagerImpl) emitErr(err error, where string, severity telemetry.ErrorSeverity) {
	if c.telemetryEmit != nil {
		c.telemetryEmit(telemetry.NewForwarderError(err, where, severity))
	}
}
