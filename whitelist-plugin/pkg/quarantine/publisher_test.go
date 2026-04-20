package quarantine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nbd-wtf/go-nostr"
)

// waitFor polls check every 20ms up to timeout. Fails the test if not satisfied.
func waitFor(t *testing.T, timeout time.Duration, msg string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %s", timeout, msg)
}

// fakeRelay is a minimal NIP-01 relay: accepts EVENT messages and replies OK.
type fakeRelay struct {
	srv       *httptest.Server
	received  chan nostr.Event
	rejectAll atomic.Bool
	closeOnce sync.Once
	connMu    sync.Mutex
	conns     []*websocket.Conn
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	r := &fakeRelay{received: make(chan nostr.Event, 100)}
	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handle)
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.stop)
	return r
}

func (r *fakeRelay) wsURL() string {
	return "ws" + strings.TrimPrefix(r.srv.URL, "http")
}

func (r *fakeRelay) handle(w http.ResponseWriter, req *http.Request) {
	c, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	r.connMu.Lock()
	r.conns = append(r.conns, c)
	r.connMu.Unlock()
	defer c.CloseNow()

	ctx := req.Context()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var msg []json.RawMessage
		if err := json.Unmarshal(data, &msg); err != nil || len(msg) < 2 {
			continue
		}
		var typ string
		if err := json.Unmarshal(msg[0], &typ); err != nil {
			continue
		}
		if typ != "EVENT" {
			continue
		}
		var evt nostr.Event
		if err := json.Unmarshal(msg[1], &evt); err != nil {
			continue
		}
		ok := !r.rejectAll.Load()
		reply, _ := json.Marshal([]interface{}{"OK", evt.ID, ok, ""})
		_ = c.Write(ctx, websocket.MessageText, reply)
		if ok {
			select {
			case r.received <- evt:
			default:
			}
		}
	}
}

func (r *fakeRelay) stop() {
	r.closeOnce.Do(func() {
		r.connMu.Lock()
		for _, c := range r.conns {
			_ = c.CloseNow()
		}
		r.connMu.Unlock()
		r.srv.Close()
	})
}

// forceCloseConns drops all active WS connections to simulate relay outage.
func (r *fakeRelay) forceCloseConns() {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	for _, c := range r.conns {
		_ = c.CloseNow()
	}
	r.conns = nil
}

func makeEvent(id string) nostr.Event {
	return nostr.Event{
		ID:        id,
		PubKey:    "pubkey-" + id,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Kind:      1,
		Content:   "hello " + id,
	}
}

func silentLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func TestPublisher_HappyPath(t *testing.T) {
	relay := newFakeRelay(t)

	p := NewPublisher(Config{
		RelayURL:        relay.wsURL(),
		BufferSize:      10,
		PublishTimeout:  2 * time.Second,
		MetricsInterval: time.Hour, // don't care about metrics ticks here
	}, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	for i := 0; i < 3; i++ {
		if !p.Enqueue(makeEvent(string(rune('a' + i)))) {
			t.Fatalf("enqueue %d failed", i)
		}
	}

	seen := 0
	timeout := time.After(3 * time.Second)
	for seen < 3 {
		select {
		case <-relay.received:
			seen++
		case <-timeout:
			t.Fatalf("only received %d/3 events; metrics=%+v", seen, p.Metrics())
		}
	}

	waitFor(t, 2*time.Second, fmt.Sprintf("published >= 3, metrics=%+v", p.Metrics()),
		func() bool { return p.Metrics().Published >= 3 })
}

func TestPublisher_BackpressureDrops(t *testing.T) {
	// No relay URL dialled so the publisher stays disconnected and the queue fills.
	p := NewPublisher(Config{
		RelayURL:        "ws://127.0.0.1:1", // unreachable
		BufferSize:      2,
		PublishTimeout:  100 * time.Millisecond,
		MetricsInterval: time.Hour,
	}, silentLogger())

	// Don't start the drain — we want the queue to stay full deterministically.
	_ = p

	if !p.Enqueue(makeEvent("1")) {
		t.Fatal("enqueue 1 failed")
	}
	if !p.Enqueue(makeEvent("2")) {
		t.Fatal("enqueue 2 failed")
	}
	if p.Enqueue(makeEvent("3")) {
		t.Fatal("expected enqueue 3 to drop")
	}
	if p.Enqueue(makeEvent("4")) {
		t.Fatal("expected enqueue 4 to drop")
	}
	m := p.Metrics()
	if m.Enqueued != 2 {
		t.Fatalf("enqueued = %d, want 2", m.Enqueued)
	}
	if m.Dropped != 2 {
		t.Fatalf("dropped = %d, want 2", m.Dropped)
	}
}

func TestPublisher_ReconnectAfterRelayDrops(t *testing.T) {
	relay := newFakeRelay(t)

	p := NewPublisher(Config{
		RelayURL:        relay.wsURL(),
		BufferSize:      10,
		PublishTimeout:  2 * time.Second,
		MetricsInterval: time.Hour,
	}, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop(time.Second)

	p.Enqueue(makeEvent("before"))
	select {
	case <-relay.received:
	case <-time.After(2 * time.Second):
		t.Fatal("didn't receive 'before' event")
	}

	// Drop the connection — publisher notices on next publish attempt.
	relay.forceCloseConns()

	// After reconnect, new events should reach the relay. The first event after
	// disconnect may be lost (fire-and-forget); send several and accept any arrival.
	var lastReceivedID atomic.Value
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case evt, ok := <-relay.received:
				if !ok {
					return
				}
				lastReceivedID.Store(evt.ID)
				if strings.HasPrefix(evt.ID, "after-") {
					return
				}
			case <-time.After(10 * time.Second):
				return
			}
		}
	}()

	for i := 0; i < 5; i++ {
		p.Enqueue(makeEvent(fmt.Sprintf("after-%d", i)))
		time.Sleep(150 * time.Millisecond)
	}

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatalf("no post-reconnect event received; metrics=%+v", p.Metrics())
	}

	if v, ok := lastReceivedID.Load().(string); !ok || !strings.HasPrefix(v, "after-") {
		t.Fatalf("no 'after-*' event observed; lastReceived=%v metrics=%+v", v, p.Metrics())
	}

	if rc := p.Metrics().ReconnectCount; rc < 2 {
		t.Fatalf("reconnectCount = %d, want >= 2", rc)
	}
}

func TestPublisher_StopIsIdempotent(t *testing.T) {
	p := NewPublisher(Config{
		RelayURL:        "ws://127.0.0.1:1",
		BufferSize:      1,
		MetricsInterval: time.Hour,
	}, silentLogger())
	p.Stop(0)
	p.Stop(0)
}

func TestPublisher_MetricsTickerAndContextCancel(t *testing.T) {
	var logBuf strings.Builder
	p := NewPublisher(Config{
		RelayURL:        "ws://127.0.0.1:1", // unreachable; keeps the drain in reconnect loop
		BufferSize:      1,
		PublishTimeout:  50 * time.Millisecond,
		MetricsInterval: 40 * time.Millisecond,
	}, log.New(&logBuf, "", 0))

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	// Let the metrics ticker fire at least once and the reconnect loop iterate.
	time.Sleep(150 * time.Millisecond)

	if !strings.Contains(logBuf.String(), "quarantine metrics:") {
		t.Fatalf("expected metrics log line, got %q", logBuf.String())
	}

	// Cancel context and confirm goroutines exit promptly.
	cancel()
	p.Stop(500 * time.Millisecond)
}

func TestPublisher_DefaultsApplied(t *testing.T) {
	p := NewPublisher(Config{RelayURL: "ws://127.0.0.1:1"}, nil)
	if cap(p.queue) != DefaultBufferSize {
		t.Fatalf("queue cap = %d, want %d", cap(p.queue), DefaultBufferSize)
	}
	if p.publishTimeout != DefaultPublishTimeout {
		t.Fatalf("publishTimeout = %v, want %v", p.publishTimeout, DefaultPublishTimeout)
	}
	if p.metricsInterval != DefaultMetricsInterval {
		t.Fatalf("metricsInterval = %v, want %v", p.metricsInterval, DefaultMetricsInterval)
	}
}
