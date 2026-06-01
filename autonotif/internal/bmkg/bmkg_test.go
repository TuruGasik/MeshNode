package bmkg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEarthquakeMessageFormatsCompactBMKGText(t *testing.T) {
	quake := Earthquake{
		Tanggal:   "04 Mei 2026",
		Jam:       "12:34:56 WIB",
		Magnitude: "5.6",
		Kedalaman: "10 km",
		Wilayah:   "Pusat gempa berada di laut 20 km Barat Laut Luwu Timur",
		Potensi:   "Tidak berpotensi tsunami",
	}

	got := quake.Message()
	checks := []string{
		"BMKG | 4/5/26 12:34:56 WIB",
		"M 5.6",
		"Kdlm: 10km",
		"Pusat: 20km Barat Laut Luwu Timur (Laut)",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Fatalf("Message() = %q, missing %q", got, check)
		}
	}
	if strings.Contains(got, "POTENSI TSUNAMI") {
		t.Fatalf("Message() should not flag tsunami for negative potential: %q", got)
	}
}

func TestEarthquakeMessageFlagsTsunamiPotential(t *testing.T) {
	quake := Earthquake{Tanggal: "04 Mei 2026", Jam: "12:34 WIB", Magnitude: "7.1", Kedalaman: "20 km", Wilayah: "100 km timur laut X", Potensi: "Berpotensi tsunami"}
	if !strings.Contains(quake.Message(), "⚠️ POTENSI TSUNAMI") {
		t.Fatalf("Message() should include tsunami warning: %q", quake.Message())
	}
}

func TestInatews2EarthquakeMessageUsesSourceLabel(t *testing.T) {
	quake := Earthquake{Source: SourceInatews2, SourceID: "bmg2026jlyc", Tanggal: "15 Mei 2026", Jam: "14:30:07 WIB", Magnitude: "2.9", Kedalaman: "15 km", Wilayah: "Seram, Indonesia", Coordinates: "-2.65,129.37"}
	got := quake.Message()
	checks := []string{
		"INATEWS2 | 15/5/26 14:30:07 WIB",
		"M 2.9",
		"Kdlm: 15km",
		"Area: Seram, Indonesia",
		"Koord: -2.65,129.37",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Fatalf("Message() = %q, missing %q", got, check)
		}
	}
	if quake.ID() != "inatews2|bmg2026jlyc" {
		t.Fatalf("ID() = %q", quake.ID())
	}
}

func TestFetcherLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Infogempa":{"gempa":{"Tanggal":"04 Mei 2026","Jam":"12:34:56 WIB","DateTime":"2026-05-04T05:34:56+00:00","Coordinates":"-1,120","Magnitude":"5.0","Kedalaman":"10 km","Wilayah":"Pusat gempa berada di darat 10 km Barat Luwu"}}}`))
	}))
	defer server.Close()

	quake, err := NewFetcher(server.URL).Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if quake.Magnitude != "5.0" || quake.Wilayah == "" {
		t.Fatalf("Latest() decoded unexpected quake: %+v", quake)
	}
}

func TestFetcherLatestRejectsEmptyGempa(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Infogempa":{"gempa":{}}}`))
	}))
	defer server.Close()

	_, err := NewFetcher(server.URL).Latest(context.Background())
	if err == nil {
		t.Fatal("Latest() expected error for empty gempa")
	}
}

func TestInatews2FetcherLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Origin"); got != "https://inatews.bmkg.go.id" {
			t.Fatalf("Origin header = %q", got)
		}
		if got := r.Header.Get("Referer"); got != "https://inatews.bmkg.go.id/" {
			t.Fatalf("Referer header = %q", got)
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "Chrome/148.0.0.0") {
			t.Fatalf("User-Agent header = %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Infogempa>
  <gempa>
    <eventid>bmg2026jlyc</eventid>
    <status>confirmed</status>
    <waktu>2026/05/15 14:30:07.686</waktu>
    <lintang>-2.65</lintang>
    <bujur>129.37</bujur>
    <dalam>15</dalam>
    <mag>2.9</mag>
    <fokal>undetermined</fokal>
    <area>Seram, Indonesia</area>
  </gempa>
  <gempa>
    <eventid>bmg2026jlxs</eventid>
    <status>confirmed</status>
    <waktu>2026/05/15 14:17:28.532</waktu>
    <lintang>-10.19</lintang>
    <bujur>119.32</bujur>
    <dalam>6</dalam>
    <mag>2.9</mag>
    <fokal>undetermined</fokal>
    <area>Sumba Region, Indonesia</area>
  </gempa>
</Infogempa>`))
	}))
	defer server.Close()

	quake, err := NewInatews2Fetcher(server.URL).Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if quake.ID() != "inatews2|bmg2026jlyc" {
		t.Fatalf("Latest() picked unexpected quake ID: %+v", quake)
	}
	if quake.Tanggal != "15 Mei 2026" || quake.Jam != "21:30:07 WIB" || quake.DateTime != "2026-05-15T21:30:07+07:00" {
		t.Fatalf("Latest() decoded unexpected time fields: %+v", quake)
	}
	if quake.Coordinates != "-2.65,129.37" || quake.Kedalaman != "15 km" || quake.Wilayah != "Seram, Indonesia" {
		t.Fatalf("Latest() decoded unexpected quake: %+v", quake)
	}
}

func TestInatews2FetcherLatestRejectsEmptyGempa(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<Infogempa></Infogempa>`))
	}))
	defer server.Close()

	_, err := NewInatews2Fetcher(server.URL).Latest(context.Background())
	if err == nil {
		t.Fatal("Latest() expected error for empty inatews2 gempa")
	}
}

func TestParseMagnitude(t *testing.T) {
	cases := []struct {
		input string
		want  float64
	}{
		{"5.6", 5.6},
		{"  3.0  ", 3.0},
		{"7", 7.0},
		{"", 0},
		{"abc", 0},
	}
	for _, c := range cases {
		if got := parseMagnitude(c.input); got != c.want {
			t.Fatalf("parseMagnitude(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestNotifierSkipsBelowMinMagnitude(t *testing.T) {
	published := 0
	pub := &fakePublisher{publishFn: func(string) error { published++; return nil }}

	xmlBody := `<Infogempa>
  <gempa><eventid>ev001</eventid><waktu>2026/05/15 10:00:00</waktu><lintang>-2.0</lintang><bujur>120.0</bujur><dalam>10</dalam><mag>3.5</mag><area>Test</area></gempa>
</Infogempa>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(xmlBody))
	}))
	defer server.Close()

	cfg := Config{Source: SourceInatews2, Inatews2URL: server.URL, MinMagnitude: 4.0, StateFile: ""}
	notifier, err := NewNotifier(cfg, pub, false)
	if err != nil {
		t.Fatalf("NewNotifier error = %v", err)
	}

	var lastID string
	if err := notifier.fetchAndMaybePublish(context.Background(), &lastID, true); err != nil {
		t.Fatalf("fetchAndMaybePublish error = %v", err)
	}
	if published != 0 {
		t.Fatalf("expected 0 publishes for M3.5 with min 4.0, got %d", published)
	}
	if lastID != "inatews2|ev001" {
		t.Fatalf("lastID should be updated even when skipped, got %q", lastID)
	}
}

func TestNotifierPublishesAboveMinMagnitude(t *testing.T) {
	published := 0
	pub := &fakePublisher{publishFn: func(string) error { published++; return nil }}

	xmlBody := `<Infogempa>
  <gempa><eventid>ev002</eventid><waktu>2026/05/15 10:00:00</waktu><lintang>-2.0</lintang><bujur>120.0</bujur><dalam>10</dalam><mag>5.2</mag><area>Test</area></gempa>
</Infogempa>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(xmlBody))
	}))
	defer server.Close()

	cfg := Config{Source: SourceInatews2, Inatews2URL: server.URL, MinMagnitude: 4.0, StateFile: ""}
	notifier, err := NewNotifier(cfg, pub, false)
	if err != nil {
		t.Fatalf("NewNotifier error = %v", err)
	}

	var lastID string
	if err := notifier.fetchAndMaybePublish(context.Background(), &lastID, true); err != nil {
		t.Fatalf("fetchAndMaybePublish error = %v", err)
	}
	if published != 1 {
		t.Fatalf("expected 1 publish for M5.2 with min 4.0, got %d", published)
	}
}

type fakePublisher struct {
	publishFn func(string) error
}

func (f *fakePublisher) PublishText(text string) error { return f.publishFn(text) }
func (f *fakePublisher) PublishNodeInfo() error        { return nil }

func TestNewLatestFetcherSelectsSource(t *testing.T) {
	jsonFetcher, err := NewLatestFetcher(Config{Source: SourceBMKG, URL: "https://example.test/bmkg.json"})
	if err != nil {
		t.Fatalf("NewLatestFetcher(bmkg) error = %v", err)
	}
	if _, ok := jsonFetcher.(*Fetcher); !ok {
		t.Fatalf("NewLatestFetcher(bmkg) = %T", jsonFetcher)
	}

	xmlFetcher, err := NewLatestFetcher(Config{Source: SourceInatews2, Inatews2URL: "https://example.test/live30event.xml"})
	if err != nil {
		t.Fatalf("NewLatestFetcher(inatews2) error = %v", err)
	}
	if _, ok := xmlFetcher.(*Inatews2Fetcher); !ok {
		t.Fatalf("NewLatestFetcher(inatews2) = %T", xmlFetcher)
	}

	if _, err := NewLatestFetcher(Config{Source: "other"}); err == nil {
		t.Fatal("NewLatestFetcher expected error for unknown source")
	}
}
