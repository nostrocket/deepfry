package exporter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	stdout   string
	waitErr  error
	startErr error
}

func (f fakeRunner) Stream(ctx context.Context, name string, args ...string) (io.ReadCloser, func() error, error) {
	if f.startErr != nil {
		return nil, nil, f.startErr
	}
	rc := io.NopCloser(bytes.NewBufferString(f.stdout))
	return rc, func() error { return f.waitErr }, nil
}

func (f fakeRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return nil, errors.New("not used")
}

func newSilentLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestStream_ParsesValidLines(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"id":"a","pubkey":"p1","kind":1,"created_at":100,"content":"hi","sig":"x","tags":[]}`,
		`{"id":"b","pubkey":"p2","kind":0,"created_at":200,"content":"meta","sig":"y","tags":[]}`,
		`{"id":"c","pubkey":"p1","kind":3,"created_at":150,"content":"","sig":"z","tags":[]}`,
	}, "\n")

	events, errs := Stream(context.Background(), fakeRunner{stdout: jsonl}, "c", "/etc/strfry.conf", newSilentLogger())
	got, err := Drain(events, errs)
	if err != nil {
		t.Fatalf("drain err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].ID != "a" || got[0].PubKey != "p1" || got[0].Kind != 1 || got[0].CreatedAt != 100 {
		t.Errorf("event 0 mismatch: %+v", got[0])
	}
	if string(got[1].Raw) == "" {
		t.Error("Raw should be retained")
	}
}

func TestStream_SkipsMalformed(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"id":"a","pubkey":"p1","kind":1,"created_at":100}`,
		`not json at all`,
		`{"id":"","pubkey":"p2","kind":1,"created_at":200}`, // missing id
		`{"id":"d","pubkey":"","kind":1,"created_at":200}`,  // missing pubkey
		``, // blank
		`{"id":"e","pubkey":"p3","kind":1,"created_at":300}`,
	}, "\n")
	events, errs := Stream(context.Background(), fakeRunner{stdout: jsonl}, "c", "/etc/strfry.conf", newSilentLogger())
	got, err := Drain(events, errs)
	if err != nil {
		t.Fatalf("drain err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (a, e); events=%+v", len(got), got)
	}
	if got[0].ID != "a" || got[1].ID != "e" {
		t.Errorf("ids = %s,%s want a,e", got[0].ID, got[1].ID)
	}
}

func TestStream_SurfacesWaitError(t *testing.T) {
	wantErr := errors.New("boom")
	events, errs := Stream(context.Background(), fakeRunner{stdout: "", waitErr: wantErr}, "c", "/etc/strfry.conf", newSilentLogger())
	if _, err := Drain(events, errs); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestStream_StartError(t *testing.T) {
	wantErr := errors.New("docker not running")
	events, errs := Stream(context.Background(), fakeRunner{startErr: wantErr}, "c", "/etc/strfry.conf", newSilentLogger())
	got, err := Drain(events, errs)
	if len(got) != 0 {
		t.Errorf("got %d events on start error, want 0", len(got))
	}
	if err == nil || !strings.Contains(err.Error(), "docker not running") {
		t.Fatalf("err = %v, want wrapped 'docker not running'", err)
	}
}

func TestStream_ContextCancel(t *testing.T) {
	// Pump enough lines that we're guaranteed to still be reading when ctx cancels.
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString(`{"id":"a","pubkey":"p","kind":1,"created_at":1}` + "\n")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := Stream(ctx, fakeRunner{stdout: b.String()}, "c", "/etc/strfry.conf", newSilentLogger())

	// Drain a few then cancel.
	count := 0
	for ev := range events {
		_ = ev
		count++
		if count == 5 {
			cancel()
		}
		if count > 1010 {
			break
		}
	}
	// Best-effort: just ensure we don't deadlock and cancel surfaces.
	select {
	case <-errs:
	case <-time.After(2 * time.Second):
		t.Fatal("errs channel did not close after cancel")
	}
}
