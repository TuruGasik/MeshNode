package hantavirus

import (
	"context"
	"testing"

	"meshnode/autonotif/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(context.Background(), ":memory:", Migrate)
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	store := NewStore(database.DB)
	t.Cleanup(func() { _ = database.Close() })
	return store
}

func TestStoreUpsertCasesInsertsAndLists(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	age := 34
	reportDate := ParseDateString("2026-05-07")
	lat, lng := 1.3521, 103.8198

	results, err := store.UpsertCases(ctx, Annotate([]Case{{
		Source:      SourceRailway,
		SourceID:    "PUSSG01",
		Status:      "asymptomatic",
		Age:         &age,
		Sex:         "male",
		Name:        "Singapore Men",
		Nationality: "Singaporean",
		Location:    Location{City: "Singapore", Country: "Singapore", Lat: &lat, Lng: &lng},
		ReportDate:  reportDate,
		SourceURL:   "https://example.test",
	}}))
	if err != nil {
		t.Fatalf("UpsertCases() error = %v", err)
	}
	if len(results) != 1 || !results[0].Inserted || results[0].Updated {
		t.Fatalf("UpsertCases() unexpected results: %+v", results)
	}

	seen, err := store.SeenStableID(ctx, "hantavirus.railway:pussg01")
	if err != nil || !seen {
		t.Fatalf("SeenStableID() = %v, %v", seen, err)
	}

	cases, err := store.ListCases(ctx)
	if err != nil {
		t.Fatalf("ListCases() error = %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("ListCases() length = %d", len(cases))
	}
	got := cases[0]
	if got.SourceID != "PUSSG01" || got.Name != "Singapore Men" || got.Age == nil || *got.Age != 34 || got.Location.Lat == nil {
		t.Fatalf("ListCases() returned unexpected case: %+v", got)
	}
	if got.Category != "asymptomatic_case" {
		t.Fatalf("ListCases() category = %q, want asymptomatic_case", got.Category)
	}
}

func TestStoreUpsertCasesMarksUnchangedAndUpdated(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	c := Annotate([]Case{{Source: SourceRailway, SourceID: "PUSSG02", Status: "confirmed", Location: Location{City: "Johannesburg"}}})

	first, err := store.UpsertCases(ctx, c)
	if err != nil {
		t.Fatalf("first UpsertCases() error = %v", err)
	}
	if !first[0].Inserted || first[0].Updated {
		t.Fatalf("first UpsertCases() result = %+v", first[0])
	}

	second, err := store.UpsertCases(ctx, c)
	if err != nil {
		t.Fatalf("second UpsertCases() error = %v", err)
	}
	if second[0].Inserted || second[0].Updated {
		t.Fatalf("second UpsertCases() should be unchanged: %+v", second[0])
	}

	c[0].Status = "deceased"
	c = Annotate(c)
	third, err := store.UpsertCases(ctx, c)
	if err != nil {
		t.Fatalf("third UpsertCases() error = %v", err)
	}
	if third[0].Inserted || !third[0].Updated {
		t.Fatalf("third UpsertCases() should update: %+v", third[0])
	}
}

func TestStoreFetchStateRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	got, err := store.GetFetchState(ctx, SourceRailway)
	if err != nil {
		t.Fatalf("GetFetchState() error = %v", err)
	}
	if got.ETag != "" || got.LastModified != "" {
		t.Fatalf("expected empty initial state, got %+v", got)
	}

	st := FetchState{ETag: `W/"abc123"`, LastModified: "Wed, 21 Oct 2026 07:28:00 GMT"}
	if err := store.SaveFetchState(ctx, SourceRailway, st); err != nil {
		t.Fatalf("SaveFetchState() error = %v", err)
	}

	got, err = store.GetFetchState(ctx, SourceRailway)
	if err != nil {
		t.Fatalf("GetFetchState() error = %v", err)
	}
	if got.ETag != st.ETag || got.LastModified != st.LastModified {
		t.Fatalf("GetFetchState() = %+v, want %+v", got, st)
	}

	// Update should replace, not duplicate.
	st2 := FetchState{ETag: `W/"def456"`, LastModified: "Thu, 22 Oct 2026 07:28:00 GMT"}
	if err := store.SaveFetchState(ctx, SourceRailway, st2); err != nil {
		t.Fatalf("SaveFetchState() second error = %v", err)
	}
	got, err = store.GetFetchState(ctx, SourceRailway)
	if err != nil {
		t.Fatalf("GetFetchState() error = %v", err)
	}
	if got.ETag != st2.ETag {
		t.Fatalf("GetFetchState() after update = %+v, want %+v", got, st2)
	}
}
