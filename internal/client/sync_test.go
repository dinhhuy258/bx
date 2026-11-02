package client

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/dinhhuy258/bx/internal/api"
)

// macFile writes rel under the test's Claude config dir (a temp dir set by startIn).
func macFile(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func view(h *harness) string { return ansi.Strip(h.m.View().Content) }

// syncStart opens a client with no project open (so no stream starts) whose Mac has skills/a (new), agents/b.md (same) and agents/x.md
// (changed), and whose server has skills/gone (removed on the Mac) and rules/mine.md.
func syncStart(t *testing.T, root string) *harness {
	t.Helper()
	h := startIn(t, root, "", 120, 40)
	macFile(t, "skills/a/SKILL.md", "a")
	macFile(t, "agents/b.md", "b")
	macFile(t, "agents/x.md", "x2")
	h.be.plan = api.SyncPlan{Dir: "/home/bx/.claude", Items: []api.SyncItem{
		{Name: "agents/b.md", Status: "same"},
		{Name: "agents/x.md", Status: "changed", Update: []string{"agents/x.md"}},
		{Name: "rules/mine.md", Status: "server-only"},
		{Name: "skills/a", Status: "new", Add: []string{"skills/a/SKILL.md"}},
		{Name: "skills/gone", Status: "removed", Delete: []string{"skills/gone/SKILL.md"}},
	}}
	h.be.applyRes = api.SyncResult{Added: 1, Updated: 1, Deleted: 1}
	return h
}

func TestSyncListOnOpen(t *testing.T) {
	h := syncStart(t, t.TempDir())
	h.press("S")
	if len(h.be.planReqs) != 1 {
		t.Fatalf("plan requests = %d", len(h.be.planReqs))
	}
	var got []string
	for _, f := range h.be.planReqs[0].Files {
		got = append(got, f.Path)
	}
	if want := []string{"agents/b.md", "agents/x.md", "skills/a/SKILL.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest = %q", got)
	}
	v := view(h)
	for _, want := range []string{"Sync to bx serve", "[x] a", "+ new", "− removed on the Mac", "~ changed", "1 server-only item kept", "• 3 to sync"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q:\n%s", want, v)
		}
	}
	h.update(tickMsg(time.Now()))
	h.update(tickMsg(time.Now()))
	if len(h.be.planReqs) != 1 {
		t.Fatalf("a tick must not scan: %d plan requests", len(h.be.planReqs))
	}
	h.press("r")
	if len(h.be.planReqs) != 2 {
		t.Fatalf("r rescans: %d plan requests", len(h.be.planReqs))
	}
}

func TestSyncSelectionIsSaved(t *testing.T) {
	root := t.TempDir()
	h := syncStart(t, root)
	h.m.opened.Projects, h.m.opened.Current = []string{"lab"}, "lab" // kept as they are by the save
	h.press("S", "j", "j", "j")                                      // the skills folder
	h.press("j", " ")                                                // skills/a
	o := loadOpened(root)
	if !reflect.DeepEqual(o.SyncExclude, []string{"skills/a"}) || !reflect.DeepEqual(o.Projects, []string{"lab"}) || o.Current != "lab" {
		t.Fatalf("client.json = %+v", o)
	}
	if len(h.m.settings.Changes()) != 0 {
		t.Fatal("the selection is not an unsaved field change")
	}
	h.press("Esc")
	if h.m.settings != nil {
		t.Fatal("Esc closes without asking")
	}
	// a new client remembers it
	h2 := syncStart(t, root)
	h2.press("S")
	if v := view(h2); !strings.Contains(v, "[ ] a") || !strings.Contains(v, "not synced") || !strings.Contains(v, "• 2 to sync") {
		t.Fatalf("the saved selection:\n%s", v)
	}
}

func TestSyncConfirmAndApply(t *testing.T) {
	root := t.TempDir()
	if err := (opened{SyncExclude: []string{"agents/x.md"}}).save(root); err != nil {
		t.Fatal(err)
	}
	h := syncStart(t, root)
	h.press("S", "s")
	if v := view(h); !strings.Contains(v, "Sync 2 changes to bx serve?") || !strings.Contains(v, "− skills/gone  deleted on the server") || strings.Contains(v, "agents/x.md") {
		t.Fatalf("the box:\n%s", v)
	}
	h.press("Enter")
	if len(h.be.applies) != 0 || len(h.be.saves) != 0 || !strings.Contains(view(h), "Sync 2 changes") {
		t.Fatal("⏎ in the box does nothing")
	}
	h.press("y")
	if len(h.be.applies) != 1 {
		t.Fatalf("applies = %d", len(h.be.applies))
	}
	req := h.be.applies[0]
	want := api.SyncApplyRequest{
		Items:  []string{"agents/b.md", "skills/a", "skills/gone"},
		Paths:  []string{"agents/b.md", "skills/a/SKILL.md"},
		Files:  []api.SyncFile{{Path: "skills/a/SKILL.md", Content: []byte("a")}},
		Delete: []string{"skills/gone/SKILL.md"},
	}
	if !reflect.DeepEqual(req, want) {
		t.Fatalf("apply = %+v\nwant    %+v", req, want)
	}
	v := view(h)
	if !strings.Contains(v, "✓ synced: 1 added, 1 updated, 1 removed") || !strings.Contains(v, "New Claude sessions see the changes.") {
		t.Fatalf("after the sync:\n%s", v)
	}
	if len(h.be.planReqs) != 2 {
		t.Fatalf("a sync rescans: %d plan requests", len(h.be.planReqs))
	}
}

func TestSyncUpToDate(t *testing.T) {
	h := syncStart(t, t.TempDir())
	h.be.plan = api.SyncPlan{Dir: "/srv", Items: []api.SyncItem{{Name: "agents/b.md", Status: "same"}}}
	h.press("S", "s")
	if v := view(h); !strings.Contains(v, "Up to date") || strings.Contains(v, "to bx serve?") || len(h.be.applies) != 0 {
		t.Fatalf("s with no changes:\n%s", v)
	}
}

func TestSyncDropsAStaleReply(t *testing.T) {
	h := syncStart(t, t.TempDir())
	h.press("S")
	old := h.m.syncSeq
	h.press("r")
	h.update(syncPlanMsg{seq: old, err: errors.New("an old failure")})
	if v := view(h); strings.Contains(v, "an old failure") || !strings.Contains(v, "+ new") {
		t.Fatalf("a stale reply must be ignored:\n%s", v)
	}
	h.press("Esc")
	h.update(syncPlanMsg{seq: h.m.syncSeq, err: errors.New("late")}) // after the screen closed
	if h.m.settings != nil {
		t.Fatal("a late reply must not reopen the screen")
	}
}

func TestSyncErrors(t *testing.T) {
	h := syncStart(t, t.TempDir())
	h.be.planErr = &StatusError{Code: 405, Text: "405 Method Not Allowed"}
	h.press("S")
	if v := view(h); !strings.Contains(v, "bx serve doesn't support sync yet. Update the server: make deploy") || !strings.Contains(v, "r to try again") {
		t.Fatalf("an old server:\n%s", v)
	}
	h.be.planErr = errors.New("bx serve unreachable: dial tcp: connection refused")
	h.press("r")
	if v := view(h); !strings.Contains(v, "bx serve unreachable: dial tcp: connection refused") {
		t.Fatalf("unreachable:\n%s", v)
	}
	h.be.planErr = nil
	h.press("r")
	if v := view(h); !strings.Contains(v, "+ new") || strings.Contains(v, "unreachable") {
		t.Fatalf("r retries:\n%s", v)
	}
	// a refused sync shows the server's text in place of the list
	h.be.applyErr = &StatusError{Code: 413, Text: "skills/a/SKILL.md: 2000000 bytes, over 1 MB: too large"}
	h.press("s", "y")
	if v := view(h); !strings.Contains(v, "skills/a/SKILL.md: 2000000 bytes, over 1 MB") {
		t.Fatalf("a failed sync:\n%s", v)
	}
}

// A Mac file over 1 MB makes its item an error that can't be selected.
func TestSyncBigFileIsAnItemError(t *testing.T) {
	h := syncStart(t, t.TempDir())
	macFile(t, "skills/a/big.bin", strings.Repeat("x", 1<<20+1))
	h.press("S")
	if v := view(h); !strings.Contains(v, "skills/a/big.bin: 1.0 MB, over the 1 MB limit") || !strings.Contains(v, "• 2 to sync") {
		t.Fatalf("big file:\n%s", v)
	}
}

func TestOpenedRoundTrip(t *testing.T) {
	root := t.TempDir()
	o := opened{Projects: []string{"a", "b"}, Current: "b", SyncExclude: []string{"rules/x.md", "skills/a"}}
	if err := o.save(root); err != nil {
		t.Fatal(err)
	}
	if got := loadOpened(root); !reflect.DeepEqual(got, o) {
		t.Fatalf("got %+v", got)
	}
	// a file from before sync_exclude: nothing excluded, the rest intact
	if err := os.WriteFile(openedFile(root), []byte(`{"projects":["a"],"current":"a"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadOpened(root); !reflect.DeepEqual(got, opened{Projects: []string{"a"}, Current: "a"}) {
		t.Fatalf("old file: %+v", got)
	}
	// no exclusions: the field is left out
	if err := (opened{Projects: []string{"a"}}).save(root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(openedFile(root)); strings.Contains(string(b), "sync_exclude") {
		t.Fatalf("%s", b)
	}
}
