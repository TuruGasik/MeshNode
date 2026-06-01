package hantavirus

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type Store struct {
	db *sql.DB
}

type UpsertResult struct {
	StableID string
	Inserted bool
	Updated  bool
}

// FetchState holds HTTP cache validators (ETag, Last-Modified) so subsequent
// fetches can use conditional GET and skip work when nothing changed upstream.
type FetchState struct {
	ETag         string
	LastModified string
	FetchedAt    time.Time
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS hantavirus_cases (
  stable_id     TEXT PRIMARY KEY,
  source        TEXT NOT NULL,
  source_id     TEXT NOT NULL,
  category      TEXT NOT NULL DEFAULT 'unknown',
  confidence    INTEGER NOT NULL DEFAULT 0,
  status        TEXT,
  age           INTEGER,
  sex           TEXT,
  name          TEXT,
  nationality   TEXT,
  city          TEXT,
  state         TEXT,
  country       TEXT,
  venue         TEXT,
  lat           REAL,
  lng           REAL,
  onset_date    TEXT,
  report_date   TEXT,
  details       TEXT,
  source_url    TEXT,
  raw_json      TEXT,
  content_hash  TEXT NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_hantavirus_cases_last_seen_at ON hantavirus_cases(last_seen_at);
CREATE INDEX IF NOT EXISTS idx_hantavirus_cases_category    ON hantavirus_cases(category);
CREATE INDEX IF NOT EXISTS idx_hantavirus_cases_confidence  ON hantavirus_cases(confidence);

CREATE TABLE IF NOT EXISTS hantavirus_fetch_state (
  source        TEXT PRIMARY KEY,
  etag          TEXT,
  last_modified TEXT,
  fetched_at    TEXT NOT NULL
);
`)
	return err
}

func (s *Store) UpsertCases(ctx context.Context, cases []Case) ([]UpsertResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	results := make([]UpsertResult, 0, len(cases))
	for _, c := range cases {
		var result UpsertResult
		result, err = upsertCase(ctx, tx, c)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) ListCases(ctx context.Context) ([]Case, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT source, source_id, status, age, sex, name, nationality,
       city, state, country, venue, lat, lng,
       onset_date, report_date, details, source_url, raw_json,
       category, confidence
FROM hantavirus_cases
ORDER BY COALESCE(report_date, onset_date, last_seen_at) DESC, stable_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cases []Case
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, rows.Err()
}

func (s *Store) SeenStableID(ctx context.Context, stableID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM hantavirus_cases WHERE stable_id = ?`, stableID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) GetFetchState(ctx context.Context, source string) (FetchState, error) {
	var etag, lastModified, fetchedAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT etag, last_modified, fetched_at FROM hantavirus_fetch_state WHERE source = ?`, source,
	).Scan(&etag, &lastModified, &fetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return FetchState{}, nil
	}
	if err != nil {
		return FetchState{}, err
	}
	st := FetchState{ETag: etag.String, LastModified: lastModified.String}
	if t := parseDBTime(fetchedAt); t != nil {
		st.FetchedAt = *t
	}
	return st, nil
}

func (s *Store) SaveFetchState(ctx context.Context, source string, st FetchState) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO hantavirus_fetch_state (source, etag, last_modified, fetched_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(source) DO UPDATE SET
  etag = excluded.etag,
  last_modified = excluded.last_modified,
  fetched_at = excluded.fetched_at
`, source, nullString(st.ETag), nullString(st.LastModified), now)
	return err
}

func upsertCase(ctx context.Context, tx *sql.Tx, c Case) (UpsertResult, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	stableID := c.StableID()
	contentHash := shortHash(caseContentString(c))

	var previousHash string
	err := tx.QueryRowContext(ctx, `SELECT content_hash FROM hantavirus_cases WHERE stable_id = ?`, stableID).Scan(&previousHash)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, insertCaseSQL, caseInsertArgs(c, stableID, contentHash, now)...)
		return UpsertResult{StableID: stableID, Inserted: true}, err
	}
	if err != nil {
		return UpsertResult{}, err
	}

	_, err = tx.ExecContext(ctx, updateCaseSQL, caseUpdateArgs(c, contentHash, now, stableID)...)
	return UpsertResult{StableID: stableID, Updated: previousHash != contentHash}, err
}

const insertCaseSQL = `
INSERT INTO hantavirus_cases (
  stable_id, source, source_id, category, confidence,
  status, age, sex, name, nationality,
  city, state, country, venue, lat, lng,
  onset_date, report_date, details, source_url, raw_json,
  content_hash, first_seen_at, last_seen_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const updateCaseSQL = `
UPDATE hantavirus_cases SET
  source = ?, source_id = ?, category = ?, confidence = ?,
  status = ?, age = ?, sex = ?, name = ?, nationality = ?,
  city = ?, state = ?, country = ?, venue = ?, lat = ?, lng = ?,
  onset_date = ?, report_date = ?, details = ?, source_url = ?, raw_json = ?,
  content_hash = ?, last_seen_at = ?
WHERE stable_id = ?`

func caseInsertArgs(c Case, stableID, contentHash, now string) []any {
	args := []any{stableID}
	args = append(args, caseFields(c)...)
	args = append(args, contentHash, now, now)
	return args
}

func caseUpdateArgs(c Case, contentHash, lastSeenAt, stableID string) []any {
	args := caseFields(c)
	args = append(args, contentHash, lastSeenAt, stableID)
	return args
}

func caseFields(c Case) []any {
	category := c.Category
	if category == "" {
		category = "unknown"
	}
	return []any{
		c.Source,
		c.SourceID,
		category,
		c.Confidence,
		nullString(c.Status),
		nullInt(c.Age),
		nullString(c.Sex),
		nullString(c.Name),
		nullString(c.Nationality),
		nullString(c.Location.City),
		nullString(c.Location.State),
		nullString(c.Location.Country),
		nullString(c.Location.Venue),
		nullFloat(c.Location.Lat),
		nullFloat(c.Location.Lng),
		nullTime(c.OnsetDate),
		nullTime(c.ReportDate),
		nullString(c.Details),
		nullString(c.SourceURL),
		nullString(c.RawJSON),
	}
}

func scanCase(rows *sql.Rows) (Case, error) {
	var c Case
	var age sql.NullInt64
	var lat, lng sql.NullFloat64
	var onset, report sql.NullString
	var status, sex, name, nationality, city, state, country, venue sql.NullString
	var details, sourceURL, rawJSON, category sql.NullString
	var confidence sql.NullInt64

	if err := rows.Scan(
		&c.Source, &c.SourceID, &status, &age, &sex, &name, &nationality,
		&city, &state, &country, &venue, &lat, &lng,
		&onset, &report, &details, &sourceURL, &rawJSON,
		&category, &confidence,
	); err != nil {
		return Case{}, err
	}
	c.Status = status.String
	if age.Valid {
		v := int(age.Int64)
		c.Age = &v
	}
	c.Sex = sex.String
	c.Name = name.String
	c.Nationality = nationality.String
	c.Location = Location{City: city.String, State: state.String, Country: country.String, Venue: venue.String}
	if lat.Valid {
		v := lat.Float64
		c.Location.Lat = &v
	}
	if lng.Valid {
		v := lng.Float64
		c.Location.Lng = &v
	}
	c.OnsetDate = parseDBTime(onset)
	c.ReportDate = parseDBTime(report)
	c.Details = details.String
	c.SourceURL = sourceURL.String
	c.RawJSON = rawJSON.String
	c.Category = category.String
	if confidence.Valid {
		c.Confidence = int(confidence.Int64)
	}
	return c, nil
}

func caseContentString(c Case) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%d",
		c.Source, c.SourceID, c.Status, intToken(c.Age), c.Sex, c.Name, c.Nationality,
		c.Location.City, c.Location.State, c.Location.Country, c.Location.Venue,
		floatToken(c.Location.Lat), floatToken(c.Location.Lng),
		dateTimeToken(c.OnsetDate), dateTimeToken(c.ReportDate),
		c.Details, c.SourceURL, c.Category, c.Confidence,
	)
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullTime(v *time.Time) any {
	if v == nil || v.IsZero() {
		return nil
	}
	return v.UTC().Format(time.RFC3339)
}

func parseDBTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	return ParseDateString(v.String)
}

func floatToken(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.6f", *v)
}

func dateTimeToken(v *time.Time) string {
	if v == nil || v.IsZero() {
		return ""
	}
	return v.UTC().Format(time.RFC3339)
}
