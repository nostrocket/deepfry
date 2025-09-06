package testutil

import (
	"context"

	"github.com/nbd-wtf/go-nostr"
)

// MockRelay is a reusable mock that implements relay.Relay for tests.
// It mirrors the previous test-local MockRelay behavior used in nsync and forwarder tests.
type MockRelay struct {
	QuerySyncReturn   []*nostr.Event
	QuerySyncError    error
	QueryEventsReturn chan *nostr.Event
	QueryEventsError  error
	SubscribeReturn   *nostr.Subscription
	SubscribeError    error
	PublishError      error
	CloseError        error

	QuerySyncCalls   []nostr.Filter
	QueryEventsCalls []nostr.Filter
	SubscribeCalls   []nostr.Filters
	PublishCalls     []nostr.Event
	CloseCalled      bool
}

func (m *MockRelay) QuerySync(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error) {
	m.QuerySyncCalls = append(m.QuerySyncCalls, filter)
	return m.QuerySyncReturn, m.QuerySyncError
}

func (m *MockRelay) QueryEvents(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error) {
	m.QueryEventsCalls = append(m.QueryEventsCalls, filter)
	if m.QueryEventsError != nil {
		return nil, m.QueryEventsError
	}

	if m.QueryEventsReturn == nil {
		ch := make(chan *nostr.Event, len(m.QuerySyncReturn))
		for _, event := range m.QuerySyncReturn {
			ch <- event
		}
		close(ch)
		return ch, nil
	}

	return m.QueryEventsReturn, nil
}

func (m *MockRelay) Subscribe(ctx context.Context, filters nostr.Filters, opts ...nostr.SubscriptionOption) (*nostr.Subscription, error) {
	m.SubscribeCalls = append(m.SubscribeCalls, filters)
	if m.SubscribeError != nil {
		return nil, m.SubscribeError
	}

	if m.SubscribeReturn == nil {
		events := make(chan *nostr.Event, len(m.QuerySyncReturn))
		eose := make(chan struct{}, 1)
		closed := make(chan string, 1)

		sub := &nostr.Subscription{
			Events:            events,
			EndOfStoredEvents: eose,
			ClosedReason:      closed,
		}

		go func() {
			defer close(events)
			for _, event := range m.QuerySyncReturn {
				select {
				case events <- event:
				case <-ctx.Done():
				}
			}
			select {
			case eose <- struct{}{}:
			case <-ctx.Done():
			}
		}()

		return sub, nil
	}

	return m.SubscribeReturn, nil
}

func (m *MockRelay) Publish(ctx context.Context, event nostr.Event) error {
	m.PublishCalls = append(m.PublishCalls, event)
	return m.PublishError
}

func (m *MockRelay) Close() error {
	m.CloseCalled = true
	return m.CloseError
}
