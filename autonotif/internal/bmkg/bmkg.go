package bmkg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"meshnode/autonotif/internal/meshtastic"
	"meshnode/autonotif/internal/notify"
	"meshnode/autonotif/internal/util"
)

var wilayahPrefixRE = regexp.MustCompile(`(?i)^Pusat gempa berada\s+di\s*(?:laut|darat)\s+`)
var distanceRE = regexp.MustCompile(`(?i)^(\d+)\s*km\s+(.+)$`)

var arahKeywords = map[string]struct{}{
	"barat laut": {},
	"barat daya": {},
	"timur laut": {},
	"tenggara":   {},
	"utara":      {},
	"selatan":    {},
	"barat":      {},
	"timur":      {},
	"baratdaya":  {},
	"baratlaut":  {},
	"timurlaut":  {},
	"laut":       {},
	"daya":       {},
}

var arahCanonical = map[string]string{
	"barat laut": "Barat Laut",
	"barat daya": "Barat Daya",
	"timur laut": "Timur Laut",
	"tenggara":   "Tenggara",
	"utara":      "Utara",
	"selatan":    "Selatan",
	"barat":      "Barat",
	"timur":      "Timur",
	"baratdaya":  "Barat Daya",
	"baratlaut":  "Barat Laut",
	"timurlaut":  "Timur Laut",
}

type Response struct {
	Infogempa struct {
		Gempa Earthquake `json:"gempa"`
	} `json:"Infogempa"`
}

type Earthquake struct {
	Source      string `json:"-"`
	SourceID    string `json:"-"`
	Tanggal     string `json:"Tanggal"`
	Jam         string `json:"Jam"`
	DateTime    string `json:"DateTime"`
	Coordinates string `json:"Coordinates"`
	Lintang     string `json:"Lintang"`
	Bujur       string `json:"Bujur"`
	Magnitude   string `json:"Magnitude"`
	Kedalaman   string `json:"Kedalaman"`
	Wilayah     string `json:"Wilayah"`
	Potensi     string `json:"Potensi"`
	Dirasakan   string `json:"Dirasakan"`
	Shakemap    string `json:"Shakemap"`
}

func (e Earthquake) ID() string {
	if strings.TrimSpace(e.SourceID) != "" {
		return strings.TrimSpace(e.Source) + "|" + strings.TrimSpace(e.SourceID)
	}
	parts := []string{e.DateTime, e.Tanggal, e.Jam, e.Coordinates, e.Magnitude, e.Kedalaman, e.Wilayah}
	return strings.Join(parts, "|")
}

func (e Earthquake) Message() string {
	jam := strings.TrimSpace(e.Jam)
	if jam == "" {
		jam = "?"
	}

	label := strings.TrimSpace(e.Source)
	if label == "" {
		label = "BMKG"
	}
	if strings.EqualFold(label, SourceInatews2) {
		base := fmt.Sprintf("%s | %s %s | M %s | Kdlm: %s | Area: %s | Koord: %s",
			strings.ToUpper(label),
			formatTanggalShort(e.Tanggal),
			formatJam(jam),
			e.Magnitude,
			formatKedalaman(e.Kedalaman),
			strings.TrimSpace(e.Wilayah),
			strings.TrimSpace(e.Coordinates),
		)
		return util.TruncateUTF8(base, meshtastic.MaxMessageBytes)
	}
	base := fmt.Sprintf("%s | %s %s | M %s | Kdlm: %s | Pusat: %s",
		strings.ToUpper(label),
		formatTanggalShort(e.Tanggal),
		formatJam(jam),
		e.Magnitude,
		formatKedalaman(e.Kedalaman),
		parseWilayah(e.Wilayah),
	)
	if hasTsunamiPotential(e.Potensi) {
		base += " | ⚠️ POTENSI TSUNAMI"
	}
	return util.TruncateUTF8(base, meshtastic.MaxMessageBytes)
}

type Fetcher struct {
	client *http.Client
	url    string
}

type LatestFetcher interface {
	Latest(context.Context) (Earthquake, error)
}

func NewLatestFetcher(cfg Config) (LatestFetcher, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Source)) {
	case "", SourceBMKG:
		return NewFetcher(cfg.URL), nil
	case SourceInatews2:
		return NewInatews2Fetcher(cfg.Inatews2URL), nil
	default:
		return nil, fmt.Errorf("unsupported bmkg source %q", cfg.Source)
	}
}

func NewFetcher(url string) *Fetcher {
	return &Fetcher{
		url: url,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (f *Fetcher) Latest(ctx context.Context) (Earthquake, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return Earthquake{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "MeshNode-AutoNotif/1.0 (+BMKG earthquake notification)")

	resp, err := f.client.Do(req)
	if err != nil {
		return Earthquake{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Earthquake{}, fmt.Errorf("bmkg returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var data Response
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return Earthquake{}, err
	}
	if data.Infogempa.Gempa.ID() == "||||||" {
		return Earthquake{}, errors.New("bmkg response does not contain gempa data")
	}
	return data.Infogempa.Gempa, nil
}

type Notifier struct {
	cfg       Config
	publisher notify.TextPublisher
	fetcher   LatestFetcher
	dryRun    bool
}

func NewNotifier(cfg Config, publisher notify.TextPublisher, dryRun bool) (*Notifier, error) {
	fetcher, err := NewLatestFetcher(cfg)
	if err != nil {
		return nil, err
	}
	return &Notifier{
		cfg:       cfg,
		publisher: publisher,
		fetcher:   fetcher,
		dryRun:    dryRun,
	}, nil
}

func (n *Notifier) Name() string { return "bmkg" }

func (n *Notifier) Run(ctx context.Context) error {
	state, err := LoadState(n.cfg.StateFile)
	if err != nil {
		slog.Warn("state file could not be loaded; starting with empty state", "error", err)
	}
	lastID := state.LastSentID

	if n.cfg.Once {
		return n.runOnce(ctx, lastID)
	}

	ticker := time.NewTicker(n.cfg.PollInterval)
	defer ticker.Stop()

	if err := n.fetchAndMaybePublish(ctx, &lastID, n.cfg.SendOnStart); err != nil {
		slog.Warn("initial fetch failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := n.fetchAndMaybePublish(ctx, &lastID, false); err != nil {
				slog.Warn("fetch/publish failed", "error", err)
			}
		}
	}
}

func (n *Notifier) runOnce(ctx context.Context, lastID string) error {
	gempa, err := n.fetcher.Latest(ctx)
	if err != nil {
		return err
	}
	if gempa.ID() == lastID {
		slog.Info("latest earthquake already sent; skipping", "id", gempa.ID(), "wilayah", gempa.Wilayah, "magnitude", gempa.Magnitude)
		return nil
	}
	if n.cfg.MinMagnitude > 0 && parseMagnitude(gempa.Magnitude) < n.cfg.MinMagnitude {
		slog.Info("earthquake below minimum magnitude; skipping", "id", gempa.ID(), "magnitude", gempa.Magnitude, "min_magnitude", n.cfg.MinMagnitude)
		return nil
	}
	msg := util.TrimRunes(gempa.Message(), 230)
	slog.Info("latest earthquake fetched", "id", gempa.ID(), "wilayah", gempa.Wilayah, "magnitude", gempa.Magnitude)
	if n.dryRun {
		fmt.Println(msg)
		return nil
	}
	if err := n.publisher.PublishText(msg); err != nil {
		return err
	}
	return SaveState(n.cfg.StateFile, gempa.ID())
}

func (n *Notifier) fetchAndMaybePublish(ctx context.Context, lastID *string, sendOnFirst bool) error {
	gempa, err := n.fetcher.Latest(ctx)
	if err != nil {
		return err
	}
	id := gempa.ID()
	if *lastID == "" {
		*lastID = id
		if !sendOnFirst {
			slog.Info("current earthquake loaded; not sent until a newer event appears", "id", id, "wilayah", gempa.Wilayah, "magnitude", gempa.Magnitude)
			return SaveState(n.cfg.StateFile, id)
		}
		if n.cfg.MinMagnitude > 0 && parseMagnitude(gempa.Magnitude) < n.cfg.MinMagnitude {
			slog.Info("earthquake below minimum magnitude; skipping", "id", id, "magnitude", gempa.Magnitude, "min_magnitude", n.cfg.MinMagnitude)
			return SaveState(n.cfg.StateFile, id)
		}
		if err := publishEarthquake(n.publisher, gempa, "startup earthquake notification sent"); err != nil {
			return err
		}
		return SaveState(n.cfg.StateFile, id)
	}
	if id == *lastID {
		return nil
	}
	if n.cfg.MinMagnitude > 0 && parseMagnitude(gempa.Magnitude) < n.cfg.MinMagnitude {
		slog.Info("earthquake below minimum magnitude; skipping", "id", id, "magnitude", gempa.Magnitude, "min_magnitude", n.cfg.MinMagnitude)
		*lastID = id
		return SaveState(n.cfg.StateFile, id)
	}
	if err := publishEarthquake(n.publisher, gempa, "earthquake notification sent"); err != nil {
		return err
	}
	*lastID = id
	return SaveState(n.cfg.StateFile, id)
}

func publishEarthquake(publisher notify.TextPublisher, gempa Earthquake, logMessage string) error {
	msg := util.TrimRunes(gempa.Message(), 230)
	if err := publisher.PublishText(msg); err != nil {
		return err
	}
	slog.Info(logMessage, "id", gempa.ID(), "wilayah", gempa.Wilayah, "magnitude", gempa.Magnitude, "message", msg)
	return nil
}

func formatTanggal(tanggal string) string {
	tanggal = strings.TrimSpace(tanggal)
	if tanggal == "" {
		return "?"
	}
	parts := strings.Fields(tanggal)
	if len(parts) != 3 {
		return tanggal
	}
	day, err := strconv.Atoi(parts[0])
	if err != nil {
		return tanggal
	}
	month, ok := monthNumber(parts[1])
	if !ok {
		return tanggal
	}
	return fmt.Sprintf("%d/%d/%s", day, month, parts[2])
}

func formatTanggalShort(tanggal string) string {
	formatted := formatTanggal(tanggal)
	parts := strings.Split(formatted, "/")
	if len(parts) != 3 {
		return formatted
	}
	year := parts[2]
	if len(year) == 4 {
		year = year[2:]
	}
	return fmt.Sprintf("%s/%s/%s", parts[0], parts[1], year)
}

func formatKedalaman(kedalaman string) string {
	return strings.ReplaceAll(strings.TrimSpace(kedalaman), " ", "")
}

func monthNumber(month string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(month)) {
	case "jan", "januari", "january":
		return 1, true
	case "feb", "februari", "february":
		return 2, true
	case "mar", "maret", "march":
		return 3, true
	case "apr", "april":
		return 4, true
	case "mei", "may":
		return 5, true
	case "jun", "juni", "june":
		return 6, true
	case "jul", "juli", "july":
		return 7, true
	case "agu", "agustus", "aug", "august":
		return 8, true
	case "sep", "sept", "september":
		return 9, true
	case "okt", "oktober", "oct", "october":
		return 10, true
	case "nov", "november":
		return 11, true
	case "des", "desember", "dec", "december":
		return 12, true
	default:
		return 0, false
	}
}

func formatJam(jam string) string {
	fields := strings.Fields(strings.TrimSpace(jam))
	if len(fields) == 0 {
		return "?"
	}
	timePart := fields[0]
	timePieces := strings.Split(timePart, ":")
	if len(timePieces) >= 3 {
		timePart = strings.Join(timePieces[:3], ":")
	} else if len(timePieces) >= 2 {
		timePart = strings.Join(timePieces[:2], ":")
	}
	zone := ""
	if len(fields) > 1 {
		zone = " " + fields[1]
	}
	return timePart + zone
}

func hasTsunamiPotential(potensi string) bool {
	text := strings.ToLower(strings.TrimSpace(potensi))
	if text == "" || !strings.Contains(text, "tsunami") {
		return false
	}
	return !strings.Contains(text, "tidak")
}

func parseMagnitude(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

func parseWilayah(wilayah string) string {
	wLower := strings.ToLower(wilayah)
	medium := ""
	if strings.Contains(wLower, "dilaut") || strings.Contains(wLower, "di laut") {
		medium = "laut"
	} else if strings.Contains(wLower, "didarat") || strings.Contains(wLower, "di darat") {
		medium = "darat"
	}

	stripped := strings.TrimSpace(wilayahPrefixRE.ReplaceAllString(wilayah, ""))
	matches := distanceRE.FindStringSubmatch(stripped)
	if matches == nil {
		return stripped
	}

	jarak := matches[1]
	rest := matches[2]
	words := strings.Fields(rest)
	arahWords := make([]string, 0, len(words))
	locWords := make([]string, 0, len(words))
	passed := false
	for _, word := range words {
		if !passed {
			if _, ok := arahKeywords[strings.ToLower(word)]; ok {
				arahWords = append(arahWords, word)
				continue
			}
		}
		passed = true
		locWords = append(locWords, word)
	}

	arah := strings.Join(arahWords, " ")
	if canonical, ok := arahCanonical[strings.ToLower(arah)]; ok {
		arah = canonical
	}
	nama := strings.Join(locWords, " ")
	suffix := ""
	if medium != "" {
		suffix = " (" + strings.ToUpper(medium[:1]) + medium[1:] + ")"
	}
	return strings.TrimSpace(fmt.Sprintf("%skm %s %s%s", jarak, arah, nama, suffix))
}
