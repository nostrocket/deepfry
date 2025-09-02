package nsync

import (
	"context"
	"event-forwarder/pkg/config"
	"event-forwarder/pkg/crypto"
	"fmt"
	"strconv"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

type Relay interface {
	QuerySync(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error)
	Publish(ctx context.Context, event nostr.Event) error
	Close() error
	//URL() string
}

const SyncEventKind = 30078

type Window struct {
	From time.Time
	To   time.Time
}

type SyncTracker struct {
	relay     Relay
	keyPair   crypto.KeyPair
	sourceURL string
}

func NewSyncTracker(relay Relay, config *config.Config) *SyncTracker {
	return &SyncTracker{
		relay:     relay,
		keyPair:   config.NostrKeyPair,
		sourceURL: config.SourceRelayURL,
	}
}

func (st *SyncTracker) GetLastWindow(ctx context.Context) (*Window, error) {
	filter := nostr.Filter{
		Kinds:   []int{SyncEventKind},
		Authors: []string{st.keyPair.PublicKeyHex},
		Tags:    nostr.TagMap{"d": []string{st.sourceURL}},
		Limit:   1,
	}

	events, err := st.relay.QuerySync(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query sync events: %w", err)
	}

	if len(events) == 0 {
		return nil, nil
	}

	event := events[0]
	return st.parseWindow(event)
}

func (st *SyncTracker) UpdateWindow(ctx context.Context, window Window) error {
	event := nostr.Event{
		PubKey:    st.keyPair.PublicKeyHex,
		CreatedAt: nostr.Now(),
		Kind:      SyncEventKind,
		Tags: nostr.Tags{
			{"d", st.sourceURL},
			{"from", strconv.FormatInt(window.From.Unix(), 10)},
			{"to", strconv.FormatInt(window.To.Unix(), 10)},
		},
		Content: "",
	}

	if err := event.Sign(st.keyPair.PrivateKeyHex); err != nil {
		return fmt.Errorf("failed to sign sync event: %w", err)
	}

	if err := st.relay.Publish(ctx, event); err != nil {
		return fmt.Errorf("failed to publish sync event: %w", err)
	}

	return nil
}

func (st *SyncTracker) parseWindow(event *nostr.Event) (*Window, error) {
	var fromStr, toStr string

	for _, tag := range event.Tags {
		if len(tag) >= 2 {
			switch tag[0] {
			case "from":
				fromStr = tag[1]
			case "to":
				toStr = tag[1]
			}
		}
	}

	if fromStr == "" || toStr == "" {
		return nil, fmt.Errorf("missing from/to tags in sync event")
	}

	fromUnix, err := strconv.ParseInt(fromStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid from timestamp: %w", err)
	}

	toUnix, err := strconv.ParseInt(toStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid to timestamp: %w", err)
	}

	return &Window{
		From: time.Unix(fromUnix, 0).UTC(),
		To:   time.Unix(toUnix, 0).UTC(),
	}, nil
}

func (w Window) Next(duration time.Duration) Window {
	return Window{
		From: w.To.UTC(),
		To:   w.To.Add(duration).UTC(),
	}
}

func NewWindow(duration time.Duration) Window {
	now := time.Now().UTC()
	windowStart := now.Truncate(duration)
	return Window{
		From: windowStart,
		To:   windowStart.Add(duration),
	}
}
