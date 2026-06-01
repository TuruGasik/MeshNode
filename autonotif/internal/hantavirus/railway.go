package hantavirus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultRailwayBaseURL = "https://hantavirus.up.railway.app"

// FetchResult is the outcome of a Railway fetch. Cases is empty when the API
// returned 304 Not Modified (NotModified=true).
type FetchResult struct {
	Cases       []Case
	NotModified bool
	State       FetchState
}

type RailwayFetcher struct {
	client      *http.Client
	baseURL     string
	maxRetries  int
	backoffBase time.Duration
}

type railwayCasesResponse struct {
	Cases []railwayCaseDTO `json:"cases"`
}

type railwayCaseDTO struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Age           *int                `json:"age"`
	Sex           any                 `json:"sex"`
	Status        string              `json:"status"`
	Date          string              `json:"date"`
	OnsetDate     string              `json:"onset_date"`
	Nationality   string              `json:"nationality"`
	ClinicalNotes string              `json:"clinical_notes"`
	SourceNotes   string              `json:"source_notes"`
	SourceURL     string              `json:"source_url"`
	Location      *railwayLocationDTO `json:"location"`
}

type railwayLocationDTO struct {
	City    string   `json:"city"`
	State   string   `json:"state"`
	Country string   `json:"country"`
	Venue   string   `json:"venue"`
	Lat     *float64 `json:"lat"`
	Lng     *float64 `json:"lng"`
}

func NewRailwayFetcher(baseURL string) *RailwayFetcher {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultRailwayBaseURL
	}
	return &RailwayFetcher{
		baseURL:     strings.TrimRight(baseURL, "/"),
		client:      &http.Client{Timeout: 15 * time.Second},
		maxRetries:  2,
		backoffBase: 500 * time.Millisecond,
	}
}

// Fetch retrieves all cases. If prev contains ETag/Last-Modified from a previous
// fetch, this issues a conditional GET; on 304 it returns NotModified=true and
// no cases. On 5xx/transient network errors it retries with exponential backoff.
func (f *RailwayFetcher) Fetch(ctx context.Context, prev FetchState) (FetchResult, error) {
	url := f.baseURL + "/api/cases"

	resp, state, err := f.doWithRetry(ctx, url, prev)
	if err != nil {
		return FetchResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return FetchResult{NotModified: true, State: state}, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return FetchResult{}, fmt.Errorf("hantavirus railway returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var data railwayCasesResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return FetchResult{}, fmt.Errorf("decode cases: %w", err)
	}

	cases := make([]Case, 0, len(data.Cases))
	for _, dto := range data.Cases {
		cases = append(cases, mapDTOToCase(dto))
	}
	return FetchResult{Cases: cases, State: state}, nil
}

// FetchCase retrieves one case by ID. No conditional GET (per-record endpoint).
func (f *RailwayFetcher) FetchCase(ctx context.Context, id string) (Case, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Case{}, errors.New("case id is required")
	}
	resp, _, err := f.doWithRetry(ctx, f.baseURL+"/api/cases/"+id, FetchState{})
	if err != nil {
		return Case{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Case{}, fmt.Errorf("hantavirus railway returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var dto railwayCaseDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return Case{}, fmt.Errorf("decode case: %w", err)
	}
	return mapDTOToCase(dto), nil
}

// doWithRetry runs the GET with conditional headers and retries on 5xx /
// transient network errors. Returns the final response (caller must close) and
// updated state with fresh validators.
func (f *RailwayFetcher) doWithRetry(ctx context.Context, url string, prev FetchState) (*http.Response, FetchState, error) {
	var lastErr error
	for attempt := 0; attempt <= f.maxRetries; attempt++ {
		if attempt > 0 {
			delay := f.backoffBase << (attempt - 1)
			select {
			case <-ctx.Done():
				return nil, FetchState{}, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, FetchState{}, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "MeshNode-AutoNotif/1.0 (+hantavirus railway fetcher)")
		if prev.ETag != "" {
			req.Header.Set("If-None-Match", prev.ETag)
		}
		if prev.LastModified != "" {
			req.Header.Set("If-Modified-Since", prev.LastModified)
		}

		resp, err := f.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		// Retry only on 5xx; client errors and successful responses are returned as-is.
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			lastErr = fmt.Errorf("hantavirus railway returned %s", resp.Status)
			resp.Body.Close()
			continue
		}

		state := FetchState{
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
			FetchedAt:    time.Now().UTC(),
		}
		// Preserve previous validators if upstream didn't send fresh ones (e.g. 304).
		if state.ETag == "" {
			state.ETag = prev.ETag
		}
		if state.LastModified == "" {
			state.LastModified = prev.LastModified
		}
		return resp, state, nil
	}
	return nil, FetchState{}, fmt.Errorf("hantavirus railway fetch failed after %d retries: %w", f.maxRetries, lastErr)
}

func mapDTOToCase(dto railwayCaseDTO) Case {
	raw, _ := json.Marshal(dto)
	c := Case{
		Source:      SourceRailway,
		SourceID:    dto.ID,
		Status:      NormalizeStatus(dto.Status),
		Age:         dto.Age,
		Sex:         NormalizeSex(dto.Sex),
		Name:        dto.Name,
		Nationality: dto.Nationality,
		OnsetDate:   ParseDateString(dto.OnsetDate),
		ReportDate:  ParseDateString(dto.Date),
		Details:     joinNonEmpty(" ", dto.ClinicalNotes, dto.SourceNotes),
		SourceURL:   dto.SourceURL,
		RawJSON:     string(raw),
	}
	if dto.Location != nil {
		c.Location = Location{
			City:    dto.Location.City,
			State:   dto.Location.State,
			Country: dto.Location.Country,
			Venue:   dto.Location.Venue,
			Lat:     dto.Location.Lat,
			Lng:     dto.Location.Lng,
		}
	}
	return c
}

func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, sep)
}
