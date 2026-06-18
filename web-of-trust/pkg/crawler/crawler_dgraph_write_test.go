package crawler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeFollowStore struct {
	mu       sync.Mutex
	errs     map[string]error
	addCalls []string
}

func (f *fakeFollowStore) AddFollowers(ctx context.Context, signerPubkey string, kind3createdAt int64, follows map[string]struct{}, debug bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCalls = append(f.addCalls, signerPubkey)
	return f.errs[signerPubkey]
}

func (f *fakeFollowStore) TouchLastDBUpdate(ctx context.Context, pubkey string) (bool, error) {
	return true, nil
}

func (f *fakeFollowStore) Close() error { return nil }

func (f *fakeFollowStore) saw(pubkey string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.addCalls {
		if call == pubkey {
			return true
		}
	}
	return false
}

func signedKind3Event(t *testing.T, follows ...string) *nostr.Event {
	t.Helper()

	secret := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(secret)
	if err != nil {
		t.Fatalf("GetPublicKey failed: %v", err)
	}

	tags := make(nostr.Tags, 0, len(follows))
	for _, follow := range follows {
		tags = append(tags, nostr.Tag{"p", follow})
	}
	event := &nostr.Event{
		PubKey:    pubkey,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Kind:      3,
		Tags:      tags,
	}
	if err := event.Sign(secret); err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	return event
}

func TestFetchAndUpdateFollows_TransientDgraphWriteDoesNotAbortBatch(t *testing.T) {
	firstFollow := signedKind3Event(t).PubKey
	secondFollow := signedKind3Event(t).PubKey
	first := signedKind3Event(t, firstFollow)
	second := signedKind3Event(t, secondFollow)

	store := &fakeFollowStore{
		errs: map[string]error{
			first.PubKey: status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
		},
	}

	queryFn := func(ctx context.Context, rs *relayState, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
		eventsChan <- first
		eventsChan <- second
		return nil
	}

	rs := &relayState{url: "wss://events.example", alive: true}
	rs.filterCap.Store(10)
	c := newTestCrawler([]*relayState{rs}, 500*time.Millisecond, 0, queryFn)
	c.dgClient = store

	result, err := c.FetchAndUpdateFollows(context.Background(), map[string]int64{
		first.PubKey:  0,
		second.PubKey: 0,
	})
	if err != nil {
		t.Fatalf("FetchAndUpdateFollows returned error for transient write: %v", err)
	}
	if _, ok := result.SkipAttempt[first.PubKey]; !ok {
		t.Fatalf("SkipAttempt missing transient pubkey %s", first.PubKey)
	}
	if _, ok := result.SkipAttempt[second.PubKey]; ok {
		t.Fatalf("SkipAttempt contains successful pubkey %s", second.PubKey)
	}
	if _, ok := result.Hits[first.PubKey]; ok {
		t.Fatalf("Hits contains transient-failed pubkey %s", first.PubKey)
	}
	if _, ok := result.Hits[second.PubKey]; !ok {
		t.Fatalf("Hits missing successful pubkey %s", second.PubKey)
	}
	if !store.saw(first.PubKey) || !store.saw(second.PubKey) {
		t.Fatalf("expected AddFollowers calls for both events, got %#v", store.addCalls)
	}
}

func TestFetchAndUpdateFollows_FatalDgraphWritePassthrough(t *testing.T) {
	follow := signedKind3Event(t).PubKey
	event := signedKind3Event(t, follow)

	store := &fakeFollowStore{
		errs: map[string]error{
			event.PubKey: status.Error(codes.ResourceExhausted, "message too large"),
		},
	}

	queryFn := func(ctx context.Context, rs *relayState, filter nostr.Filter, eventsChan chan<- *nostr.Event) error {
		eventsChan <- event
		return nil
	}

	rs := &relayState{url: "wss://events.example", alive: true}
	rs.filterCap.Store(10)
	c := newTestCrawler([]*relayState{rs}, 500*time.Millisecond, 0, queryFn)
	c.dgClient = store

	result, err := c.FetchAndUpdateFollows(context.Background(), map[string]int64{
		event.PubKey: 0,
	})
	if err == nil {
		t.Fatal("FetchAndUpdateFollows returned nil for fatal write error")
	}
	if len(result.Hits) != 0 {
		t.Fatalf("expected no hits after fatal write, got %#v", result.Hits)
	}
	if len(result.SkipAttempt) != 0 {
		t.Fatalf("expected no transient skip after fatal write, got %#v", result.SkipAttempt)
	}
}
