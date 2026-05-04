// Package exporter streams events out of a quarantine StrFry LMDB by
// shelling out to `strfry export` inside the quarantine container.
//
// Why exec instead of opening LMDB directly: LMDB has well-defined
// reader/writer semantics, but strfry maintains its own indices and
// metadata above the raw events. Reading via `strfry export` lets the
// strfry binary mediate the LMDB transaction and guarantees we never
// see half-written state. The relay process and the export process
// coexist safely (LMDB allows many readers + one writer).
package exporter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"quarantine-rescuer/internal/runner"
)

// scanBuf is the bufio.Scanner buffer ceiling. StrFry's default
// maxEventSize is 64 KiB, but the JSON envelope plus large tag arrays
// can push a single line higher; 1 MiB is generous and still cheap.
const scanBuf = 1 << 20

// RawEvent is the minimum we parse out of each export line.
// Raw is retained verbatim so the forwarder can re-publish the exact
// signed event without re-serialisation drift.
type RawEvent struct {
	ID        string
	PubKey    string
	Kind      int
	CreatedAt int64
	Raw       []byte
}

// minEvent matches the shape of strfry export's JSONL output well enough
// to extract identifying fields. We deliberately ignore tags, content,
// and sig to keep parsing cheap when the LMDB has millions of events.
type minEvent struct {
	ID        string `json:"id"`
	PubKey    string `json:"pubkey"`
	Kind      int    `json:"kind"`
	CreatedAt int64  `json:"created_at"`
}

// Stream runs `docker exec <container> /app/strfry --config=<configPath> export`
// and emits one RawEvent per stdout line.
//
// The returned events channel closes on EOF or on context cancellation.
// The errs channel receives at most one error: a parse error mid-stream,
// or the command's non-zero exit. Callers should drain events first,
// then read errs.
func Stream(ctx context.Context, r runner.Runner, container, configPath string, logger *slog.Logger) (<-chan RawEvent, <-chan error) {
	if logger == nil {
		logger = slog.Default()
	}
	events := make(chan RawEvent, 256)
	errs := make(chan error, 1)

	stdout, wait, err := r.Stream(ctx, "docker", "exec", container,
		"/app/strfry", fmt.Sprintf("--config=%s", configPath), "export")
	if err != nil {
		close(events)
		errs <- fmt.Errorf("start strfry export: %w", err)
		close(errs)
		return events, errs
	}

	go func() {
		defer close(events)
		defer close(errs)
		defer stdout.Close()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), scanBuf)

		var lineNum int
		for scanner.Scan() {
			lineNum++
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			// Defensive copies: scanner reuses its buffer.
			raw := make([]byte, len(line))
			copy(raw, line)

			var ev minEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				logger.Warn("exporter: skipping malformed line", "line", lineNum, "err", err)
				continue
			}
			if ev.ID == "" || ev.PubKey == "" {
				logger.Warn("exporter: skipping line missing id/pubkey", "line", lineNum)
				continue
			}

			select {
			case events <- RawEvent{ID: ev.ID, PubKey: ev.PubKey, Kind: ev.Kind, CreatedAt: ev.CreatedAt, Raw: raw}:
			case <-ctx.Done():
				_ = wait()
				errs <- ctx.Err()
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- fmt.Errorf("scan strfry export: %w", err)
			_ = wait()
			return
		}
		if err := wait(); err != nil {
			errs <- err
			return
		}
	}()

	return events, errs
}

// Drain reads every event from a Stream into a slice, returning the first
// stream error it sees. Useful for tests and small-DB cases. For very
// large quarantines, prefer to consume the channel directly.
func Drain(events <-chan RawEvent, errs <-chan error) ([]RawEvent, error) {
	var out []RawEvent
	for ev := range events {
		out = append(out, ev)
	}
	if err, ok := <-errs; ok {
		return out, err
	}
	return out, nil
}
