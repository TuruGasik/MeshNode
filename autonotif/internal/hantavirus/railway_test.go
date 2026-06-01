package hantavirus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRailwayFetchMapsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cases" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("ETag", `W/"v1"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2026 07:28:00 GMT")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cases":[{"id":"PUSSG01","name":"Singapore Men","age":34,"sex":"male","status":"asymptomatic","date":"2026-05-07","onset_date":"2026-05-06","nationality":"Singaporean","clinical_notes":"notes","location":{"city":"Singapore","country":"Singapore","lat":1.3521,"lng":103.8198},"source_url":"https://example.test"}]}`))
	}))
	defer server.Close()

	res, err := NewRailwayFetcher(server.URL).Fetch(context.Background(), FetchState{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if res.NotModified {
		t.Fatal("Fetch() should not be NotModified on first call")
	}
	if len(res.Cases) != 1 {
		t.Fatalf("Fetch() cases length = %d", len(res.Cases))
	}
	got := res.Cases[0]
	if got.SourceID != "PUSSG01" || got.Status != "asymptomatic" || got.Sex != "male" || got.Location.Country != "Singapore" {
		t.Fatalf("Fetch() mapped unexpected case: %+v", got)
	}
	if got.ReportDate == nil || got.OnsetDate == nil {
		t.Fatalf("Fetch() should parse dates: %+v", got)
	}
	if got.RawJSON == "" {
		t.Fatal("Fetch() should keep RawJSON")
	}
	if res.State.ETag != `W/"v1"` || res.State.LastModified == "" {
		t.Fatalf("Fetch() state validators not captured: %+v", res.State)
	}
}

func TestRailwayFetchHandles304NotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != `W/"v1"` {
			t.Fatalf("expected If-None-Match header, got %q", r.Header.Get("If-None-Match"))
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	prev := FetchState{ETag: `W/"v1"`, LastModified: "Wed, 21 Oct 2026 07:28:00 GMT"}
	res, err := NewRailwayFetcher(server.URL).Fetch(context.Background(), prev)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !res.NotModified {
		t.Fatal("Fetch() should report NotModified")
	}
	if len(res.Cases) != 0 {
		t.Fatalf("Fetch() cases on 304 should be empty, got %d", len(res.Cases))
	}
	// Validators should be carried over so the next fetch can use them.
	if res.State.ETag != prev.ETag {
		t.Fatalf("Fetch() state.ETag = %q, want preserved %q", res.State.ETag, prev.ETag)
	}
}

func TestRailwayFetchRetriesOn5xx(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cases":[]}`))
	}))
	defer server.Close()

	f := NewRailwayFetcher(server.URL)
	f.backoffBase = 1 * time.Millisecond
	res, err := f.Fetch(context.Background(), FetchState{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", calls)
	}
	if len(res.Cases) != 0 {
		t.Fatalf("Fetch() cases length = %d, want 0", len(res.Cases))
	}
}

func TestRailwayFetchGivesUpAfterRetries(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	f := NewRailwayFetcher(server.URL)
	f.backoffBase = 1 * time.Millisecond
	if _, err := f.Fetch(context.Background(), FetchState{}); err == nil {
		t.Fatal("Fetch() expected error after exhausting retries")
	}
	if got := atomic.LoadInt32(&calls); got != int32(f.maxRetries+1) {
		t.Fatalf("expected %d total calls, got %d", f.maxRetries+1, got)
	}
}

func TestRailwayFetchCase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cases/PUSSG01" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"PUSSG01","name":"Singapore Men","sex":"male","status":"asymptomatic","date":"2026-05-07","location":{"city":"Singapore","country":"Singapore"}}`))
	}))
	defer server.Close()

	got, err := NewRailwayFetcher(server.URL).FetchCase(context.Background(), "PUSSG01")
	if err != nil {
		t.Fatalf("FetchCase() error = %v", err)
	}
	if got.SourceID != "PUSSG01" || got.Name != "Singapore Men" {
		t.Fatalf("FetchCase() mapped unexpected case: %+v", got)
	}
}
