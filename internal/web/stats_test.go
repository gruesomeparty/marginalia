package web

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSummarizeAndSpan(t *testing.T) {
	d := Summarize([]time.Duration{3 * time.Hour, time.Minute, 50 * time.Hour, 20 * time.Second})
	if d != (Durations{Count: 4, Median: 3 * 3600, P90: 50 * 3600}) {
		t.Errorf("Summarize = %+v", d)
	}
	if (Summarize(nil) != Durations{}) {
		t.Error("an empty summary is not zero")
	}
	for sec, want := range map[int64]string{45: "45s", 600: "10m", 3*3600 + 720: "3h 12m", 50 * 3600: "2d 2h"} {
		if got := span(sec, 1); got != want {
			t.Errorf("span(%d) = %q, want %q", sec, got, want)
		}
	}
	if span(0, 0) != "–" {
		t.Error("nothing measured should read as a dash")
	}
}

func TestStatsPageSaysWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderStats(&buf, Stats{Period: "7d", Agents: []AgentStats{}}); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	if !strings.Contains(page, "No reviews in this period.") || !strings.Contains(page, `<option value="7d" selected>`) || !strings.Contains(page, "Clear filters") {
		t.Errorf("empty stats page is missing its parts")
	}
}
