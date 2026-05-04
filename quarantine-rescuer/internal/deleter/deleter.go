// Package deleter removes successfully forwarded events from the
// quarantine LMDB by shelling out to `strfry delete --filter`.
//
// We delete by event id, not by author, so a race where new events
// arrive in quarantine from the same pubkey between export and delete
// cannot accidentally lose them. A second `strfry delete` writer is
// safe alongside the running relay: LMDB serialises writers via the
// process-shared lock file.
package deleter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"quarantine-rescuer/internal/runner"
)

// DefaultBatchSize is the number of event ids per `strfry delete`
// invocation. 500 ids of 64 hex chars + JSON overhead ≈ 33 KiB of
// argv, well under common Linux ARG_MAX (typically 128 KiB+).
const DefaultBatchSize = 500

// MinBatchSize bounds the halve-and-retry recursion. Below this we
// give up on the batch and fail each id individually so progress is
// not blocked by one bad id.
const MinBatchSize = 8

// Result reports per-id outcome.
type Result struct {
	Deleted []string
	Failed  []string
}

// Deleter runs strfry delete inside the quarantine container.
type Deleter struct {
	runner     runner.Runner
	container  string
	configPath string
	batchSize  int
	logger     *slog.Logger
}

func New(r runner.Runner, container, configPath string, batchSize int, logger *slog.Logger) *Deleter {
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Deleter{
		runner:     r,
		container:  container,
		configPath: configPath,
		batchSize:  batchSize,
		logger:     logger,
	}
}

// DeleteByIDs deletes the given event ids in batches.
// Errors on a single batch trigger a halve-and-retry; once the batch
// shrinks below MinBatchSize we fail each id one at a time so a
// poison id can't block progress for the rest.
func (d *Deleter) DeleteByIDs(ctx context.Context, ids []string) Result {
	var res Result
	d.deleteRange(ctx, ids, d.batchSize, &res)
	return res
}

func (d *Deleter) deleteRange(ctx context.Context, ids []string, batchSize int, res *Result) {
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]

		if err := d.runDelete(ctx, batch); err != nil {
			if len(batch) == 1 {
				d.logger.Warn("deleter: single-id delete failed",
					"event_id", batch[0], "err", err)
				res.Failed = append(res.Failed, batch[0])
				continue
			}
			if batchSize/2 < MinBatchSize {
				d.logger.Warn("deleter: batch failed below MinBatchSize; failing one-by-one",
					"size", len(batch), "err", err)
				for _, id := range batch {
					if err := d.runDelete(ctx, []string{id}); err != nil {
						d.logger.Warn("deleter: single-id delete failed",
							"event_id", id, "err", err)
						res.Failed = append(res.Failed, id)
					} else {
						res.Deleted = append(res.Deleted, id)
					}
				}
				continue
			}
			d.logger.Warn("deleter: batch failed; halving and retrying",
				"size", len(batch), "err", err)
			d.deleteRange(ctx, batch, batchSize/2, res)
			continue
		}
		res.Deleted = append(res.Deleted, batch...)
	}
}

func (d *Deleter) runDelete(ctx context.Context, ids []string) error {
	filter := struct {
		IDs []string `json:"ids"`
	}{IDs: ids}
	encoded, err := json.Marshal(filter)
	if err != nil {
		return fmt.Errorf("marshal filter: %w", err)
	}
	_, err = d.runner.Output(ctx, "docker", "exec", d.container,
		"/app/strfry", fmt.Sprintf("--config=%s", d.configPath),
		"delete", "--filter", string(encoded))
	return err
}
