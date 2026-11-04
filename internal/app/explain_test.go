package app

import (
	"context"
	"slices"
	"testing"

	"github.com/dinhhuy258/bx/internal/state"
)

func TestExplainRunsAForkInTheSessionsFolder(t *testing.T) {
	a, _, p := newApp(t)
	p.AddSession(&state.Session{Story: "s1", UUID: "u1", Model: "opus"})
	var dir, stdin string
	var args []string
	a.Claude = func(_ context.Context, d string, _ []string, in string, as ...string) ([]byte, error) {
		dir, stdin, args = d, in, as
		return []byte(`{"result":"It merges."}`), nil
	}
	got, err := a.Explain(context.Background(), p, "s1", 1, "Explain a.go line 1")
	if err != nil || got != "It merges." {
		t.Fatalf("%q %v", got, err)
	}
	if dir != a.Dir(p, "s1") || stdin != "Explain a.go line 1" || !slices.Contains(args, "--fork-session") || !slices.Contains(args, "opus") {
		t.Fatalf("dir %s stdin %q args %q", dir, stdin, args)
	}
	if _, err := a.Explain(context.Background(), p, "s1", 0, "x"); err != nil || slices.Contains(args, "--resume") {
		t.Fatalf("code only: %v %q", err, args)
	}
	if _, err := a.Explain(context.Background(), p, "s1", 7, "x"); err == nil {
		t.Fatal("no session 7")
	}
}
