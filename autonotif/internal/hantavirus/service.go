package hantavirus

import (
	"context"
	"fmt"
	"log/slog"

	"meshnode/autonotif/internal/db"
)

// RunOnce performs a one-shot fetch from the Railway hantavirus API and
// persists annotated cases to SQLite. Used by the HANTAVIRUS_ONCE startup mode.
func RunOnce(ctx context.Context, cfg Config) error {
	database, err := db.Open(ctx, cfg.DBPath, Migrate)
	if err != nil {
		return err
	}
	defer database.Close()

	store := NewStore(database.DB)

	prevState, err := store.GetFetchState(ctx, SourceRailway)
	if err != nil {
		return fmt.Errorf("get fetch state: %w", err)
	}

	result, err := NewRailwayFetcher(cfg.RailwayURL).Fetch(ctx, prevState)
	if err != nil {
		return fmt.Errorf("railway fetch: %w", err)
	}

	if err := store.SaveFetchState(ctx, SourceRailway, result.State); err != nil {
		slog.Warn("save fetch state failed", "error", err)
	}

	if result.NotModified {
		slog.Info("hantavirus railway not modified", "etag", result.State.ETag)
		return nil
	}

	cases := Annotate(result.Cases)
	results, err := store.UpsertCases(ctx, cases)
	if err != nil {
		return fmt.Errorf("cases upsert: %w", err)
	}

	inserted, updated := 0, 0
	for _, r := range results {
		if r.Inserted {
			inserted++
		}
		if r.Updated {
			updated++
		}
	}
	slog.Info("hantavirus cases stored",
		"db_path", cfg.DBPath,
		"fetched", len(cases),
		"inserted", inserted,
		"updated", updated,
	)
	for i, c := range cases {
		if i >= 5 {
			break
		}
		slog.Info("hantavirus case", "summary", c.Summary(), "source_id", c.SourceID, "category", c.Category)
	}
	return nil
}
