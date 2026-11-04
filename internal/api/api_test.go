package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dinhhuy258/bx/internal/claude"
	"github.com/dinhhuy258/bx/internal/state"
	"github.com/dinhhuy258/bx/internal/ui"
)

func TestSnapshotRoundTrip(t *testing.T) {
	p := state.New("home-network", "/p")
	p.Stories["s1"] = &state.Story{Branch: "feat/s1"}
	p.AddSession(&state.Session{Story: "s1", UUID: "u1", Title: "t", Created: time.Unix(1, 0).UTC()})
	in := Snapshot{Project: p, Live: ui.Live{Now: time.Unix(2, 0).UTC(), Agents: map[string]claude.Agent{"u1": {SessionID: "u1", Status: "waiting"}}, Diff: map[string]ui.Diff{"s1": {Add: 3}}, Branch: "main"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Snapshot
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	s := out.Project.Sessions[1]
	if s == nil || s.Story != "s1" {
		t.Fatalf("project = %+v", out.Project)
	}
	if out.Live.Agents["u1"].State() != claude.Waiting || out.Live.Diff["s1"].Add != 3 || out.Live.Branch != "main" {
		t.Fatalf("live = %+v", out.Live)
	}
}

func TestSyncFileContentIsBase64(t *testing.T) {
	b, err := json.Marshal(SyncFile{Path: "skills/a/SKILL.md", Content: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"content":"aGk="`) {
		t.Fatalf("%s", b)
	}
	var out SyncFile
	if err := json.Unmarshal(b, &out); err != nil || string(out.Content) != "hi" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestReviewJSON(t *testing.T) {
	r := Review{Base: "main", Total: 1, Files: []DiffFile{{Path: "a.go", Status: "modified", Add: 1,
		Hunks: []Hunk{{Header: "@@ -1 +1 @@", Rows: []DiffRow{{Kind: "add", New: 1, Text: "x"}}}}}},
		ReviewComments: ReviewComments{Version: 2, Comments: []Comment{{ID: "c1", File: "a.go", Side: "new", Line: 1, Body: "why", SentTo: 3, SentAt: 9}}},
		Explains:       []Explain{{ID: "e1", File: "a.go", Side: "new", Line: 1, State: ExplainDone, Answer: "ok", Started: 5}}, ExplainWith: ExplainCodeOnly}
	b, _ := json.Marshal(r)
	want := `{"base":"main","total":1,"files":[{"path":"a.go","status":"modified","add":1,"del":0,"hunks":[{"header":"@@ -1 +1 @@","rows":[{"kind":"add","old":0,"new":1,"text":"x"}]}]}],"version":2,"comments":[{"id":"c1","file":"a.go","side":"new","line":1,"code":"","body":"why","sent_to":3,"sent_at":9}],"explains":[{"id":"e1","file":"a.go","side":"new","line":1,"code":"","state":"done","answer":"ok","started":5}],"explain_with":-1}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}
