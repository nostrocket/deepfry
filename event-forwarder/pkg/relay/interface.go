package relay

import (
	"context"

	"github.com/nbd-wtf/go-nostr"
)

// Relay defines the interface for relay operations.
// This allows us to mock it easily in tests without depending on external libraries.
// Note: *nostr.Relay implements this interface directly, so no wrapper is needed.
type Relay interface {
	QuerySync(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error)
	QueryEvents(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error)
	Subscribe(ctx context.Context, filters nostr.Filters, opts ...nostr.SubscriptionOption) (*nostr.Subscription, error)
	Publish(ctx context.Context, event nostr.Event) error
	Close() error
}
