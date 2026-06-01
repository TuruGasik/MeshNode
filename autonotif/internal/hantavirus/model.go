package hantavirus

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	SourceRailway = "hantavirus.railway"
)

var whitespaceRE = regexp.MustCompile(`\s+`)
var aggregateGroupRE = regexp.MustCompile(`\(\d+\)`)

// Case is the canonical hantavirus case format. Single-source for now
// (Railway), but kept as a neutral struct so adding sources later is cheap.
type Case struct {
	Source      string
	SourceID    string
	Status      string
	Age         *int
	Sex         string
	Name        string
	Nationality string
	Location    Location
	OnsetDate   *time.Time
	ReportDate  *time.Time
	Details     string
	SourceURL   string
	RawJSON     string
	// Derived
	Category   string
	Confidence int
}

type Location struct {
	City    string
	State   string
	Country string
	Venue   string
	Lat     *float64
	Lng     *float64
}

// StableID is the canonical primary key for a case. Single-source for now,
// so SourceID alone is unique. Kept namespaced (source:id) to stay forward
// compatible if a second source is added later.
func (c Case) StableID() string {
	return normalizeToken(c.Source) + ":" + normalizeToken(c.SourceID)
}

func (c Case) EventTime() time.Time {
	for _, t := range []*time.Time{c.ReportDate, c.OnsetDate} {
		if t != nil && !t.IsZero() {
			return *t
		}
	}
	return time.Time{}
}

func (c Case) Summary() string {
	where := firstNonEmpty(c.Location.Venue, c.Location.City, c.Location.Country, "unknown location")
	when := "unknown date"
	if t := c.EventTime(); !t.IsZero() {
		when = t.Format("2006-01-02")
	}
	label := firstNonEmpty(c.Name, c.SourceID, "case")
	status := firstNonEmpty(c.Status, "unknown")
	return fmt.Sprintf("Hantavirus | %s | %s | %s | %s", when, status, label, where)
}

// Annotate fills Category and Confidence for every case. Call this after
// fetching/mapping but before persisting/notifying.
func Annotate(cases []Case) []Case {
	out := make([]Case, len(cases))
	for i, c := range cases {
		c.Category = ClassifyCase(c)
		c.Confidence = ConfidenceScore(c)
		out[i] = c
	}
	return out
}

// ClassifyCase assigns a category string based on status and name.
func ClassifyCase(c Case) string {
	if aggregateGroupRE.MatchString(c.Name) {
		return "aggregate_group"
	}
	switch c.Status {
	case "deceased":
		return "deceased_case"
	case "confirmed":
		return "confirmed_case"
	case "suspected":
		return "suspected_case"
	case "asymptomatic":
		return "asymptomatic_case"
	case "symptomatic":
		return "symptomatic_case"
	case "monitoring":
		return "monitoring_contact"
	default:
		return "unknown"
	}
}

// ConfidenceScore returns a 0–100 score for how reliable and complete a case is.
func ConfidenceScore(c Case) int {
	switch c.Category {
	case "confirmed_case":
		return 100
	case "deceased_case":
		return 90
	case "suspected_case":
		return 80
	case "symptomatic_case":
		return 70
	case "asymptomatic_case":
		return 60
	case "monitoring_contact":
		return 30
	case "aggregate_group":
		return 20
	default:
		return 10
	}
}

func NormalizeStatus(s string) string {
	s = normalizeSpaces(s)
	switch strings.ToLower(s) {
	case "confirmed", "confirm", "konfirmasi":
		return "confirmed"
	case "suspected", "suspect", "probable":
		return "suspected"
	case "deceased", "dead", "death", "died":
		return "deceased"
	case "asymptomatic":
		return "asymptomatic"
	default:
		return strings.ToLower(s)
	}
}

func NormalizeSex(v any) string {
	switch x := v.(type) {
	case float64:
		return sexFromInt(int(x))
	case int:
		return sexFromInt(x)
	case string:
		s := strings.ToLower(normalizeSpaces(x))
		switch s {
		case "m", "male", "man", "1":
			return "male"
		case "f", "female", "woman", "2":
			return "female"
		default:
			return s
		}
	default:
		return ""
	}
}

func ParseDateString(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	layouts := []string{time.RFC3339, "2006-01-02", "2006/01/02", "02 Jan 2006", "Jan 2 2006"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return &t
		}
	}
	return nil
}

func normalizeToken(s string) string {
	return strings.ToLower(normalizeSpaces(s))
}

func normalizeSpaces(s string) string {
	return strings.TrimSpace(whitespaceRE.ReplaceAllString(s, " "))
}

func intToken(v *int) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%d", *v)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return normalizeSpaces(value)
		}
	}
	return ""
}

func sexFromInt(v int) string {
	switch v {
	case 1:
		return "male"
	case 2:
		return "female"
	default:
		return ""
	}
}
