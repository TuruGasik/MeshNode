package main

import (
	"testing"
	"time"
)

func TestMatchMQTTTopic(t *testing.T) {
	cases := []struct {
		filter string
		topic  string
		match  bool
	}{
		{"msh/ID/#", "msh/ID/2/e/LongFast/!aabb", true},
		{"msh/ID/#", "msh/ID", true},
		{"msh/ID/#", "msh/SG/2/e/LongFast/!aabb", false},
		{"msh/+/2/e/#", "msh/ID/2/e/LongFast/!aabb", true},
		{"msh/+/2/e/#", "msh/ID/3/e/LongFast/!aabb", false},
		{"msh/+/+/e/#", "msh/ID/2/e/LongFast/!aabb", true},
		{"msh/ID/2/e/LongFast/!aabb", "msh/ID/2/e/LongFast/!aabb", true},
		{"msh/ID/2/e/LongFast/!aabb", "msh/ID/2/e/LongFast/!ccdd", false},
		{"msh/+", "msh/ID/2", false},
		{"msh/+", "msh/ID", true},
	}
	for _, tc := range cases {
		got := matchMQTTTopic(tc.filter, tc.topic)
		if got != tc.match {
			t.Errorf("matchMQTTTopic(%q, %q) = %v, want %v", tc.filter, tc.topic, got, tc.match)
		}
	}
}

func TestDedupStoreSize(t *testing.T) {
	d := NewDedupStore(60 * time.Second)
	if d.Size() != 0 {
		t.Fatalf("expected size 0, got %d", d.Size())
	}
	d.CheckAndStore("a", "local")
	d.CheckAndStore("b", "local")
	d.CheckAndStore("a", "local") // duplicate, no size change
	if d.Size() != 2 {
		t.Fatalf("expected size 2, got %d", d.Size())
	}
}
