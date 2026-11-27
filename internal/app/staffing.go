package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/inkuna-ai/inkuna/internal/orchestrator"
	"github.com/inkuna-ai/inkuna/internal/storage"
)

// staffingCache keeps the orchestrator's validated staffing decisions in the
// store, so a replan with the same inputs reuses one instead of calling the
// model again.
type staffingCache struct{ store *storage.Store }

func (cache staffingCache) Recall(ctx context.Context, key string) (orchestrator.Decision, time.Time, bool, error) {
	encoded, decidedAt, found, err := cache.store.StaffingDecision(ctx, key)
	if err != nil || !found {
		return orchestrator.Decision{}, time.Time{}, false, err
	}
	var decision orchestrator.Decision
	if err := json.Unmarshal(encoded, &decision); err != nil {
		return orchestrator.Decision{}, time.Time{}, false, fmt.Errorf("decode kept staffing decision: %w", err)
	}
	return decision, decidedAt, true, nil
}

func (cache staffingCache) Remember(ctx context.Context, key string, decision orchestrator.Decision) error {
	encoded, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode staffing decision: %w", err)
	}
	return cache.store.SaveStaffingDecision(ctx, key, encoded, time.Now().UTC())
}

// orchestratorDir is the empty directory staffing calls run in, so that no
// repository's CLAUDE.md or AGENTS.md loads into them. One directory under
// the data directory serves every call; a call with no tools writes nothing
// there, so there is nothing to clean up.
func (service *Service) orchestratorDir() (string, error) {
	directory := filepath.Join(service.dataDir, "orchestrator")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create the orchestrator's working directory: %w", err)
	}
	return directory, nil
}
