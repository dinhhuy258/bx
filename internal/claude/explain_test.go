package claude

import (
	"slices"
	"testing"
)

func TestExplainArgs(t *testing.T) {
	got := ExplainArgs("u1", "opus")
	want := []string{"claude", "-p", "--resume", "u1", "--fork-session", "--no-session-persistence",
		"--tools=Read,Grep,Glob", "--strict-mcp-config", "--output-format", "json", "--model", "opus"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
	got = ExplainArgs("", "")
	want = []string{"claude", "-p", "--no-session-persistence", "--tools=Read,Grep,Glob", "--strict-mcp-config", "--output-format", "json"}
	if !slices.Equal(got, want) {
		t.Fatalf("code only: %q", got)
	}
}

func TestExplainResult(t *testing.T) {
	if a, err := ExplainResult([]byte(`{"type":"result","is_error":false,"result":"  It merges.\n"}`)); err != nil || a != "It merges." {
		t.Fatalf("%q %v", a, err)
	}
	for _, out := range []string{`{"is_error":true,"result":"No conversation found"}`, `{"result":"  "}`, `not json`} {
		if _, err := ExplainResult([]byte(out)); err == nil {
			t.Errorf("%s: want an error", out)
		}
	}
}
