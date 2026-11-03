package commands

import (
	"strings"
	"testing"
)

func TestStatusline(t *testing.T) {
	for in, want := range map[string]string{
		`{"model":{"display_name":"Opus 5.5"},"context_window":{"used_percentage":34},
		  "rate_limits":{"five_hour":{"used_percentage":23.4},"seven_day":{"used_percentage":40.6,"resets_at":1794225600}}}`: "Opus 5.5 · ctx 34% · 5h 23% · 7d 41%",
		`{"model":{"display_name":"Fable 5.1"},"context_window":{"used_percentage":4},"rate_limits":{"seven_day":{"used_percentage":6}}}`: "Fable 5.1 · ctx 4% · 7d 6%",
		`{"model":{"display_name":"Sonnet 5.5"},"context_window":{"used_percentage":0}}`:                                                  "Sonnet 5.5 · ctx 0%",
		`{"model":{"display_name":"Haiku 5.5"},"context_window":{}}`:                                                                      "Haiku 5.5",
		`{}`:       "",
		`not json`: "",
		``:         "",
	} {
		if got := statusline(strings.NewReader(in)); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}
