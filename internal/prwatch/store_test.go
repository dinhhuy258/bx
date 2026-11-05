package prwatch

import (
	"path/filepath"
	"testing"

	"github.com/dinhhuy258/bx/internal/ui"
)

func TestStoreRoundTripAndSent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p", "s.json")
	f, err := Load(path)
	if err != nil || len(f.PRs) != 0 {
		t.Fatalf("missing file: %+v %v", f, err)
	}
	f = File{Me: "huy", CheckedAt: 5, PRs: []ui.PRInfo{{Number: 1, State: ui.PROpen, Threads: []ui.PRThread{{ID: "T1"}, {ID: "T2"}}}}}
	f.MarkSent([]string{"T2"}, 99)
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	g, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d := g.Data()
	if d.Me != "huy" || d.PRs[0].Threads[1].SentAt != 99 || d.PRs[0].Threads[0].SentAt != 0 {
		t.Fatalf("data = %+v", d)
	}
}
