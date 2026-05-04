// Package lmdbreader streams events out of a strfry quarantine LMDB by
// opening the database read-only directly. This bypasses the running
// strfry relay and avoids spawning a second strfry process — the latter
// caused an OOM kill of the quarantine relay in the first iteration.
//
// LMDB safety: opening as a reader alongside a running writer is fully
// supported. Multiple readers and one writer coexist via the lock file
// (lock.mdb). Our read transaction sees a consistent snapshot from the
// moment it began; strfry's writer continues unaffected.
//
// We rely on strfry's documented LMDB schema (see
// https://github.com/hoytech/strfry/blob/master/golpe.yaml). Two tables:
//
//   - EventPayload (raw, MDB_INTEGERKEY): keys are uint64 levIds. Values
//     start with a one-byte compression flag:
//     0x00 — no compression; rest is raw event JSON
//     0x01 — zstd; followed by 4-byte native-endian dict id, then payload
//   - CompressionDictionary: maps dict id to the zstd dictionary bytes.
//
// We don't touch the indexed Event table — the JSON in EventPayload
// already contains everything we need (id, pubkey, kind, created_at).
package lmdbreader

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/PowerDNS/lmdb-go/lmdb"
	"github.com/klauspost/compress/zstd"

	"quarantine-rescuer/internal/event"
)

// DefaultMapSize matches strfry's default dbParams__mapsize from
// golpe.yaml. LMDB requires a reader's mapsize to be ≥ the writer's;
// setting this to the strfry default is the safest choice.
const DefaultMapSize int64 = 10995116277760

// MaxDBs is the value passed to SetMaxDBs. Strfry uses ~6 named DBIs;
// we set 32 to leave headroom for future schema growth.
const MaxDBs = 32

// payloadDBI and dictDBI are the named LMDB tables we open.
const (
	payloadDBI = "EventPayload"
	dictDBI    = "CompressionDictionary"
)

// RawEvent is re-exported from internal/event for back-compatibility
// of intra-module call sites; the underlying type lives there.
type RawEvent = event.RawEvent

// minEvent is the JSON subset we need to extract pubkey/kind/created_at
// from each payload. Everything else stays in the raw bytes for forwarding.
type minEvent struct {
	ID        string `json:"id"`
	PubKey    string `json:"pubkey"`
	Kind      int    `json:"kind"`
	CreatedAt int64  `json:"created_at"`
}

// Stream opens the LMDB at lmdbPath read-only, iterates the EventPayload
// table, decompresses each value as needed, and emits one RawEvent per
// event. The events channel closes on EOF or error; errs receives at most
// one terminal error after the events channel is drained.
func Stream(ctx context.Context, lmdbPath string, mapSize int64, logger *slog.Logger) (<-chan RawEvent, <-chan error) {
	if logger == nil {
		logger = slog.Default()
	}
	if mapSize <= 0 {
		mapSize = DefaultMapSize
	}
	events := make(chan RawEvent, 256)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		env, err := openEnv(lmdbPath, mapSize)
		if err != nil {
			errs <- fmt.Errorf("open lmdb env at %s: %w", lmdbPath, err)
			return
		}
		defer env.Close()

		dec, err := zstd.NewReader(nil) // no default dict; dicts are loaded lazily per dictID
		if err != nil {
			errs <- fmt.Errorf("init zstd decoder: %w", err)
			return
		}
		defer dec.Close()

		dictCache := newDictCache()

		err = env.View(func(txn *lmdb.Txn) error {
			payloadDB, err := txn.OpenDBI(payloadDBI, 0)
			if err != nil {
				return fmt.Errorf("open %s: %w", payloadDBI, err)
			}
			dictDB, err := txn.OpenDBI(dictDBI, 0)
			if err != nil && !lmdb.IsNotFound(err) {
				// Missing CompressionDictionary table is fine — it just
				// means nothing was ever compressed. Real errors aren't.
				return fmt.Errorf("open %s: %w", dictDBI, err)
			}

			cur, err := txn.OpenCursor(payloadDB)
			if err != nil {
				return fmt.Errorf("open cursor: %w", err)
			}
			defer cur.Close()

			op := uint(lmdb.First)
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				_, val, err := cur.Get(nil, nil, op)
				op = lmdb.Next
				if lmdb.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return fmt.Errorf("cursor get: %w", err)
				}
				jsonBytes, err := decodePayload(val, dec, dictCache, txn, dictDB)
				if err != nil {
					logger.Warn("lmdbreader: skipping undecodable event", "err", err)
					continue
				}
				ev, err := parseMinEvent(jsonBytes)
				if err != nil {
					logger.Warn("lmdbreader: skipping unparseable JSON", "err", err)
					continue
				}
				if ev.ID == "" || ev.PubKey == "" {
					logger.Warn("lmdbreader: skipping event with empty id/pubkey")
					continue
				}
				// Copy: cursor reuses memory, and we send to a channel
				// that may outlive this iteration.
				rawCopy := make([]byte, len(jsonBytes))
				copy(rawCopy, jsonBytes)

				select {
				case events <- RawEvent{
					ID:        ev.ID,
					PubKey:    ev.PubKey,
					Kind:      ev.Kind,
					CreatedAt: ev.CreatedAt,
					Raw:       rawCopy,
				}:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		})
		if err != nil {
			errs <- err
		}
	}()

	return events, errs
}

func openEnv(path string, mapSize int64) (*lmdb.Env, error) {
	env, err := lmdb.NewEnv()
	if err != nil {
		return nil, fmt.Errorf("new env: %w", err)
	}
	if err := env.SetMaxDBs(MaxDBs); err != nil {
		env.Close()
		return nil, fmt.Errorf("set max dbs: %w", err)
	}
	if err := env.SetMapSize(mapSize); err != nil {
		env.Close()
		return nil, fmt.Errorf("set mapsize: %w", err)
	}
	if err := env.Open(path, lmdb.Readonly, 0o644); err != nil {
		env.Close()
		return nil, fmt.Errorf("open: %w", err)
	}
	return env, nil
}

// decodePayload turns one EventPayload value into the raw event JSON.
// val format: [flag byte][optional dict id (4 bytes, native endian) when flag=1][payload].
func decodePayload(val []byte, dec *zstd.Decoder, cache *dictCache, txn *lmdb.Txn, dictDB lmdb.DBI) ([]byte, error) {
	if len(val) == 0 {
		return nil, errors.New("empty value")
	}
	switch val[0] {
	case 0x00:
		return val[1:], nil
	case 0x01:
		if len(val) < 5 {
			return nil, fmt.Errorf("zstd payload truncated: %d bytes", len(val))
		}
		dictID := binary.NativeEndian.Uint32(val[1:5])
		body := val[5:]
		dec, err := cache.decoderFor(dictID, txn, dictDB)
		if err != nil {
			return nil, fmt.Errorf("load dict %d: %w", dictID, err)
		}
		out, err := dec.DecodeAll(body, nil)
		if err != nil {
			return nil, fmt.Errorf("zstd decode (dict %d): %w", dictID, err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown compression flag 0x%02x", val[0])
	}
}

func parseMinEvent(jsonBytes []byte) (*minEvent, error) {
	var ev minEvent
	if err := json.Unmarshal(jsonBytes, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}
