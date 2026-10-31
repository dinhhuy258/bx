package ui

import (
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/config"
)

func TestNarrowThreshold(t *testing.T) {
	cases := map[int]bool{30: true, 99: true, 100: false, 160: false}
	for width, want := range cases {
		if got := IsNarrow(width); got != want {
			t.Errorf("IsNarrow(%d) = %v", width, got)
		}
	}
}

func TestKeyBarNarrowHint(t *testing.T) {
	m, _ := sampleModel(Options{AutoExpandWaiting: true})
	m.SelectSession(1)
	bar := KeyBar(m, config.Default("/x"), BarContext{Narrow: true})
	if !strings.Contains(bar, "⏎ Claude") {
		t.Fatal(bar)
	}
	if !strings.Contains(bar, "^l back") {
		t.Fatal(bar)
	}
	wide := KeyBar(m, config.Default("/x"), BarContext{})
	if strings.Contains(wide, "^l back") {
		t.Fatal(wide)
	}
}
