package web

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// Periods the stats page offers, in the order its menu lists them. The
// value is what the query string carries; "" is all time.
var statPeriods = []struct{ Value, Label string }{
	{"7d", "Last 7 days"},
	{"30d", "Last 30 days"},
	{"90d", "Last 90 days"},
	{"", "All time"},
}

// ParsePeriod reads a period from the query string. An unknown value is all
// time rather than an error: a stale link should still show something.
func ParsePeriod(v string) (string, time.Duration) {
	switch v {
	case "7d":
		return v, 7 * 24 * time.Hour
	case "30d":
		return v, 30 * 24 * time.Hour
	case "90d":
		return v, 90 * 24 * time.Hour
	}
	return "", 0
}

// Durations summarizes how long something took across reviews. Seconds,
// because JSON has no duration; the text fields are for the page.
type Durations struct {
	Count  int   `json:"count"`
	Median int64 `json:"median_seconds"`
	P90    int64 `json:"p90_seconds"`
}

// Summarize builds Durations from raw values.
func Summarize(ds []time.Duration) Durations {
	if len(ds) == 0 {
		return Durations{}
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(q float64) int64 {
		i := int(q*float64(len(s)-1) + 0.5)
		return int64(s[i] / time.Second)
	}
	return Durations{Count: len(s), Median: at(0.5), P90: at(0.9)}
}

// Claims is what became of the agent's `addressed` claims: the reviewer
// confirmed or reopened them, or has not looked yet (Pending). Unclaimed
// counts claims whose text had not changed when they were made.
type Claims struct {
	Addressed int `json:"addressed"`
	Confirmed int `json:"confirmed"`
	Reopened  int `json:"reopened"`
	Pending   int `json:"pending"`
	Unclaimed int `json:"unclaimed"`
}

// AgentStats is one agent's numbers, or the instance's total. Counts and
// timings only: never a word of a document or a note.
type AgentStats struct {
	Agent      string         `json:"agent"`
	Reviews    int            `json:"reviews"`
	Done       int            `json:"done"`
	FirstLook  Durations      `json:"first_look"`
	TimeToDone Durations      `json:"time_to_done"`
	Rounds     int            `json:"rounds"`
	Notes      map[string]int `json:"notes"`
	Claims     Claims         `json:"claims"`
}

// RoundsPerReview is rounds over finished reviews, for the page.
func (a AgentStats) RoundsPerReview() string {
	if a.Done == 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f", float64(a.Rounds)/float64(a.Done))
}

// Stats is the stats page and /api/stats.
type Stats struct {
	Period  string       `json:"period"`
	Since   *time.Time   `json:"since,omitempty"`
	Agent   string       `json:"agent,omitempty"`
	Framing string       `json:"framing,omitempty"`
	Total   AgentStats   `json:"total"`
	Agents  []AgentStats `json:"agents"`
	// Options for the filter menus; not part of the JSON.
	AllAgents   []string `json:"-"`
	AllFramings []string `json:"-"`
	Theme       Theme    `json:"-"`
}

type statsView struct {
	Stats
	Periods, AgentOpts, FramingOpts []option
	Rows                            []statsRow
	Types                           []string
	Filtered                        bool
}

type statsRow struct {
	AgentStats
	Label                   string
	Total                   bool
	FirstLook, FirstLook90  string
	TimeToDone, TimeToDone9 string
	ByType                  []int
}

// RenderStats writes the stats page.
func RenderStats(w io.Writer, s Stats) error {
	v := statsView{Stats: s, Filtered: s.Period != "" || s.Agent != "" || s.Framing != ""}
	for _, p := range statPeriods {
		v.Periods = append(v.Periods, option{Value: p.Value, Label: p.Label, Selected: p.Value == s.Period})
	}
	v.AgentOpts = append(v.AgentOpts, option{Value: "", Label: "All agents", Selected: s.Agent == ""})
	for _, a := range s.AllAgents {
		v.AgentOpts = append(v.AgentOpts, option{Value: a, Label: a, Selected: a == s.Agent})
	}
	v.FramingOpts = append(v.FramingOpts, option{Value: "", Label: "All framings", Selected: s.Framing == ""})
	for _, f := range s.AllFramings {
		v.FramingOpts = append(v.FramingOpts, option{Value: f, Label: framingLabel(f), Selected: f == s.Framing})
	}
	types := map[string]bool{}
	for t := range s.Total.Notes {
		types[t] = true
	}
	for t := range types {
		v.Types = append(v.Types, t)
	}
	sort.Strings(v.Types)
	row := func(a AgentStats, label string, total bool) statsRow {
		r := statsRow{
			AgentStats: a, Label: label, Total: total,
			FirstLook: span(a.FirstLook.Median, a.FirstLook.Count), FirstLook90: span(a.FirstLook.P90, a.FirstLook.Count),
			TimeToDone: span(a.TimeToDone.Median, a.TimeToDone.Count), TimeToDone9: span(a.TimeToDone.P90, a.TimeToDone.Count),
		}
		for _, t := range v.Types {
			r.ByType = append(r.ByType, a.Notes[t])
		}
		return r
	}
	for _, a := range s.Agents {
		label := a.Agent
		if label == "" {
			label = "(no agent)"
		}
		v.Rows = append(v.Rows, row(a, label, false))
	}
	if len(s.Agents) > 1 {
		v.Rows = append(v.Rows, row(s.Total, "All agents", true))
	}
	return tmpl.ExecuteTemplate(w, "stats.html.tmpl", v)
}

// span renders seconds as the page says them: "–" with nothing measured,
// else the two largest units ("3h 12m", "2d 4h", "45s").
func span(sec int64, n int) string {
	if n == 0 {
		return "–"
	}
	d := time.Duration(sec) * time.Second
	days := int64(d / (24 * time.Hour))
	h := int64(d/time.Hour) % 24
	m := int64(d/time.Minute) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", sec)
}
