package deleter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

type fakeRunner struct {
	mu     sync.Mutex
	calls  [][]string
	failer func(args []string) error
}

func (f *fakeRunner) Stream(ctx context.Context, name string, args ...string) (io.ReadCloser, func() error, error) {
	return nil, nil, errors.New("not used")
}

func (f *fakeRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	full := append([]string{name}, args...)
	f.calls = append(f.calls, full)
	f.mu.Unlock()
	if f.failer != nil {
		if err := f.failer(args); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func newSilentLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDeleteByIDs_HappyPath(t *testing.T) {
	r := &fakeRunner{}
	d := New(r, "qcontainer", "/etc/strfry.conf", 100, newSilentLogger())
	ids := make([]string, 250)
	for i := range ids {
		ids[i] = string(rune('a' + i%26))
	}
	res := d.DeleteByIDs(context.Background(), ids)
	if len(res.Deleted) != 250 {
		t.Fatalf("Deleted = %d, want 250", len(res.Deleted))
	}
	if len(res.Failed) != 0 {
		t.Fatalf("Failed = %d, want 0", len(res.Failed))
	}
	if len(r.calls) != 3 { // batches of 100, 100, 50
		t.Fatalf("calls = %d, want 3 batches", len(r.calls))
	}
	for _, call := range r.calls {
		if call[0] != "docker" || call[1] != "exec" || call[2] != "qcontainer" {
			t.Errorf("unexpected call prefix: %v", call[:3])
		}
	}
}

func TestDeleteByIDs_BatchFailureHalves(t *testing.T) {
	// First batch of size 100 fails; halved to 50 succeeds.
	var seen []int
	r := &fakeRunner{
		failer: func(args []string) error {
			ids := extractIDsArgs(args)
			seen = append(seen, len(ids))
			if len(ids) >= 100 {
				return errors.New("simulated batch failure")
			}
			return nil
		},
	}
	d := New(r, "qcontainer", "/etc/strfry.conf", 100, newSilentLogger())
	ids := make([]string, 100)
	for i := range ids {
		ids[i] = "id" + string(rune('a'+i%26))
	}
	res := d.DeleteByIDs(context.Background(), ids)
	if len(res.Deleted) != 100 {
		t.Fatalf("Deleted = %d, want 100", len(res.Deleted))
	}
	if len(res.Failed) != 0 {
		t.Fatalf("Failed = %d, want 0", len(res.Failed))
	}
	// Expect 100 (fail) → 50, 50 (succeed). At least one entry in `seen` should be 100, then two of 50.
	gotHundred, gotFifty := 0, 0
	for _, n := range seen {
		if n == 100 {
			gotHundred++
		}
		if n == 50 {
			gotFifty++
		}
	}
	if gotHundred < 1 || gotFifty < 2 {
		t.Errorf("expected at least 1 batch of 100 and 2 of 50; seen=%v", seen)
	}
}

func TestDeleteByIDs_PoisonIDFailsOnce(t *testing.T) {
	r := &fakeRunner{
		failer: func(args []string) error {
			ids := extractIDsArgs(args)
			for _, id := range ids {
				if id == "POISON" {
					return errors.New("poison id rejected")
				}
			}
			return nil
		},
	}
	d := New(r, "qcontainer", "/etc/strfry.conf", 16, newSilentLogger())
	ids := []string{"a", "b", "c", "POISON", "e"}
	res := d.DeleteByIDs(context.Background(), ids)
	if len(res.Failed) != 1 || res.Failed[0] != "POISON" {
		t.Errorf("Failed = %v, want [POISON]", res.Failed)
	}
	if len(res.Deleted) != 4 {
		t.Errorf("Deleted = %d, want 4", len(res.Deleted))
	}
}

func extractIDsArgs(args []string) []string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--filter" {
			var f struct {
				IDs []string `json:"ids"`
			}
			_ = json.Unmarshal([]byte(args[i+1]), &f)
			return f.IDs
		}
	}
	return nil
}

func TestDeleteByIDs_EmptyInput(t *testing.T) {
	r := &fakeRunner{}
	d := New(r, "qcontainer", "/etc/strfry.conf", 100, newSilentLogger())
	res := d.DeleteByIDs(context.Background(), nil)
	if len(res.Deleted)+len(res.Failed) != 0 {
		t.Errorf("expected no work on empty input")
	}
	if len(r.calls) != 0 {
		t.Errorf("expected no docker calls on empty input, got %d", len(r.calls))
	}
}

func TestDeleteByIDs_BuildsCorrectArgs(t *testing.T) {
	r := &fakeRunner{}
	d := New(r, "Q", "/cfg", 10, newSilentLogger())
	d.DeleteByIDs(context.Background(), []string{"id1", "id2"})
	if len(r.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(r.calls))
	}
	args := r.calls[0]
	want := []string{"docker", "exec", "Q", "/app/strfry", "--config=/cfg", "delete", "--filter"}
	for i, w := range want {
		if i >= len(args) || args[i] != w {
			t.Errorf("arg[%d] = %q, want %q (full=%v)", i, safeIdx(args, i), w, args)
		}
	}
	if !strings.Contains(args[len(args)-1], `"ids":["id1","id2"]`) {
		t.Errorf("filter arg = %q, want ids array", args[len(args)-1])
	}
}

func safeIdx(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "<missing>"
}
