package hantavirus

import (
	"testing"
)

func TestStableIDUsesSourceAndSourceID(t *testing.T) {
	c := Case{Source: SourceRailway, SourceID: " PUSSG01 "}
	if got := c.StableID(); got != "hantavirus.railway:pussg01" {
		t.Fatalf("StableID() = %q", got)
	}
}

func TestNormalizeStatusAndSex(t *testing.T) {
	if got := NormalizeStatus("DECEASED"); got != "deceased" {
		t.Fatalf("NormalizeStatus() = %q", got)
	}
	if got := NormalizeSex(float64(2)); got != "female" {
		t.Fatalf("NormalizeSex(float64(2)) = %q", got)
	}
	if got := NormalizeSex("M"); got != "male" {
		t.Fatalf("NormalizeSex(M) = %q", got)
	}
}

func TestAnnotateAssignsCategoryAndConfidence(t *testing.T) {
	cases := []Case{
		{Status: "confirmed"},
		{Status: "deceased"},
		{Status: "asymptomatic"},
		{Status: "monitoring"},
		{Status: "", Name: "Group A (5)"},
		{Status: ""},
	}
	got := Annotate(cases)
	want := []struct {
		category   string
		minScore   int
	}{
		{"confirmed_case", 100},
		{"deceased_case", 90},
		{"asymptomatic_case", 60},
		{"monitoring_contact", 30},
		{"aggregate_group", 20},
		{"unknown", 10},
	}
	if len(got) != len(want) {
		t.Fatalf("Annotate() length = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Category != w.category {
			t.Errorf("case %d category = %q, want %q", i, got[i].Category, w.category)
		}
		if got[i].Confidence < w.minScore {
			t.Errorf("case %d confidence = %d, want >= %d", i, got[i].Confidence, w.minScore)
		}
	}
}

func TestEventTimePrefersReportThenOnset(t *testing.T) {
	report := ParseDateString("2026-05-07")
	onset := ParseDateString("2026-05-01")
	c := Case{ReportDate: report, OnsetDate: onset}
	if got := c.EventTime(); got.Format("2006-01-02") != "2026-05-07" {
		t.Fatalf("EventTime() = %v, want report date", got)
	}
	c.ReportDate = nil
	if got := c.EventTime(); got.Format("2006-01-02") != "2026-05-01" {
		t.Fatalf("EventTime() = %v, want onset date when report missing", got)
	}
}
