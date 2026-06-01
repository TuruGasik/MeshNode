package bmkg

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Inatews2Fetcher struct {
	client *http.Client
	url    string
}

type inatews2Response struct {
	Quakes []inatews2Earthquake `xml:"gempa"`
}

type inatews2Earthquake struct {
	EventID string `xml:"eventid"`
	Status  string `xml:"status"`
	Waktu   string `xml:"waktu"`
	Lintang string `xml:"lintang"`
	Bujur   string `xml:"bujur"`
	Dalam   string `xml:"dalam"`
	Mag     string `xml:"mag"`
	Fokal   string `xml:"fokal"`
	Area    string `xml:"area"`
}

func NewInatews2Fetcher(url string) *Inatews2Fetcher {
	return &Inatews2Fetcher{
		url: url,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (f *Inatews2Fetcher) Latest(ctx context.Context) (Earthquake, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return Earthquake{}, err
	}
	setInatews2Headers(req)

	resp, err := f.client.Do(req)
	if err != nil {
		return Earthquake{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Earthquake{}, fmt.Errorf("inatews2 returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var data inatews2Response
	if err := xml.NewDecoder(resp.Body).Decode(&data); err != nil {
		return Earthquake{}, err
	}
	if len(data.Quakes) == 0 {
		return Earthquake{}, errors.New("inatews2 response does not contain gempa data")
	}
	quake := data.Quakes[0].Earthquake()
	if quake.ID() == "inatews2|" {
		return Earthquake{}, errors.New("inatews2 response does not contain event id")
	}
	return quake, nil
}

func setInatews2Headers(req *http.Request) {
	headers := map[string]string{
		"Accept":               "*/*",
		"Accept-Language":      "en-US,en;q=0.9,id;q=0.8",
		"DNT":                  "1",
		"Origin":               "https://inatews.bmkg.go.id",
		"Priority":             "u=1, i",
		"Referer":              "https://inatews.bmkg.go.id/",
		"Sec-CH-UA":            `"Chromium";v="148", "Google Chrome";v="148", "Not/A)Brand";v="99"`,
		"Sec-CH-UA-Mobile":     "?0",
		"Sec-CH-UA-Platform":   `"Windows"`,
		"Sec-Fetch-Dest":       "empty",
		"Sec-Fetch-Mode":       "cors",
		"Sec-Fetch-Site":       "cross-site",
		"Sec-GPC":              "1",
		"User-Agent":           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
		"X-Browser-Channel":    "stable",
		"X-Browser-Copyright":  "Copyright 2026 Google LLC. All Rights Reserved.",
		"X-Browser-Validation": "/cenh4vmufqmGPuhsKGCCcd62Bk=",
		"X-Browser-Year":       "2026",
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
}

func (q inatews2Earthquake) Earthquake() Earthquake {
	tanggal, jam, dateTime := formatInatews2Time(q.Waktu)
	return Earthquake{
		Source:      SourceInatews2,
		SourceID:    strings.TrimSpace(q.EventID),
		Tanggal:     tanggal,
		Jam:         jam,
		DateTime:    dateTime,
		Coordinates: strings.TrimSpace(q.Lintang) + "," + strings.TrimSpace(q.Bujur),
		Lintang:     strings.TrimSpace(q.Lintang),
		Bujur:       strings.TrimSpace(q.Bujur),
		Magnitude:   strings.TrimSpace(q.Mag),
		Kedalaman:   formatInatews2Depth(q.Dalam),
		Wilayah:     strings.TrimSpace(q.Area),
	}
}

func formatInatews2Time(value string) (string, string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", ""
	}
	wib := time.FixedZone("WIB", 7*60*60)
	for _, layout := range []string{"2006/01/02 15:04:05.000", "2006/01/02 15:04:05"} {
		parsed, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			parsedWIB := parsed.In(wib)
			return fmt.Sprintf("%02d %s %04d", parsedWIB.Day(), monthNameID(parsedWIB.Month()), parsedWIB.Year()), parsedWIB.Format("15:04:05 MST"), parsedWIB.Format(time.RFC3339)
		}
	}
	return value, "", value
}

func formatInatews2Depth(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(value), "km") {
		return value
	}
	return value + " km"
}

func monthNameID(month time.Month) string {
	switch month {
	case time.January:
		return "Januari"
	case time.February:
		return "Februari"
	case time.March:
		return "Maret"
	case time.April:
		return "April"
	case time.May:
		return "Mei"
	case time.June:
		return "Juni"
	case time.July:
		return "Juli"
	case time.August:
		return "Agustus"
	case time.September:
		return "September"
	case time.October:
		return "Oktober"
	case time.November:
		return "November"
	case time.December:
		return "Desember"
	default:
		return ""
	}
}
