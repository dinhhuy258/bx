package client

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
// (changed), and whose server has skills/gone (removed on the Mac) and rules/mine.md. b, x and gone were synced before.
func syncStart(t *testing.T, root string) *harness {
	t.Helper()
	h := startIn(t, root, "", 120, 40)
	macFile(t, "skills/a/SKILL.md", "a")
	macFile(t, "agents/b.md", "b")
	macFile(t, "agents/x.md", "x2")
	h.be.plan = api.SyncPlan{Dir: "/home/bx/.claude", Items: []api.SyncItem{
		{Name: "agents/b.md", Status: "same", Synced: true},
		{Name: "agents/x.md", Status: "changed", Update: []string{"agents/x.md"}, Synced: true},
		{Name: "rules/mine.md", Status: "server-only"},
		{Name: "skills/a", Status: "new", Add: []string{"skills/a/SKILL.md"}},
		{Name: "skills/gone", Status: "removed", Delete: []string{"skills/gone/SKILL.md"}, Synced: true},
	}}
	h.be.applyRes = api.SyncResult{Added: 1, Updated: 1, Deleted: 1}
	return h
}

func TestSyncListOnOpen(t *testing.T) {
	root := t.TempDir()
	h := syncStart(t, root)
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
	// only what bx synced before is checked; a new Mac item is not, a removed one is
	for _, want := range []string{"Sync to bx serve", "[ ] a", "not synced", "[x] gone", "− removed on the Mac", "~ changed", "1 server-only item kept", "• 2 to sync"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q:\n%s", want, v)
		}
	}
	if got, want := loadOpened(root).SyncInclude, []string{"agents/b.md", "agents/x.md", "skills/gone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("seeded include = %q, want %q", got, want)
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
	if want := []string{"agents/b.md", "agents/x.md", "skills/a", "skills/gone"}; !reflect.DeepEqual(o.SyncInclude, want) || !reflect.DeepEqual(o.Projects, []string{"lab"}) || o.Current != "lab" {
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
	if v := view(h2); !strings.Contains(v, "[x] a") || !strings.Contains(v, "+ new") || !strings.Contains(v, "• 3 to sync") {
		t.Fatalf("the saved selection:\n%s", v)
	}
}

// Once seeded, the include list is the selection: an empty one is not seeded again.
func TestSyncSeedsOnlyOnce(t *testing.T) {
	root := t.TempDir()
	h := syncStart(t, root)
	h.press("S", "j", "j", "j") // the skills folder: [-], a unchecked, gone checked
	h.press(" ", " ")           // all, then none
	h.press("j", "j", "j", " ") // the agents folder: [x] → none
	v := view(h)
	for _, want := range []string{"[ ] skills", "[ ] a", "[ ] gone", "[ ] agents", "[ ] b.md", "[ ] x.md"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q:\n%s", want, v)
		}
	}
	if b, _ := os.ReadFile(openedFile(root)); !strings.Contains(string(b), `"sync_include": []`) {
		t.Fatalf("client.json:\n%s", b)
	}
	h.press("r")
	if v := view(h); !strings.Contains(v, "[ ] gone") || !strings.Contains(v, "[ ] x.md") {
		t.Fatalf("a rescan seeds again:\n%s", v)
	}
	h2 := syncStart(t, root)
	h2.press("S") // nothing selected: both folders start folded
	if v := view(h2); !strings.Contains(v, "▸ [ ] skills") || !strings.Contains(v, "▸ [ ] agents") || len(h2.m.settings.SyncScope()) != 0 {
		t.Fatalf("a new client seeds again:\n%s", v)
	}
	h2.press("s")
	if v := view(h2); !strings.Contains(v, "Nothing selected") || strings.Contains(v, "to bx serve?") || len(h2.be.applies) != 0 {
		t.Fatalf("s with nothing selected:\n%s", v)
	}
}

// An old client.json's unselected list is honored once, then dropped.
func TestSyncSeedHonorsOldExclude(t *testing.T) {
	root := t.TempDir() // the fake server has no projects, so lab is dropped on open
	if err := os.WriteFile(openedFile(root), []byte(`{"projects":["lab"],"current":"lab","sync_exclude":["agents/x.md","skills/a"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := syncStart(t, root)
	h.press("S")
	o := loadOpened(root)
	if want := []string{"agents/b.md", "skills/gone"}; !reflect.DeepEqual(o.SyncInclude, want) {
		t.Fatalf("client.json = %+v", o)
	}
	if b, _ := os.ReadFile(openedFile(root)); strings.Contains(string(b), "sync_exclude") {
		t.Fatalf("sync_exclude kept:\n%s", b)
	}
	if v := view(h); !strings.Contains(v, "▸ [-] agents") || !strings.Contains(v, "• 1 to sync") || slices.Contains(h.m.settings.SyncScope(), "agents/x.md") {
		t.Fatalf("the seeded selection:\n%s", v)
	}
}

// Selected items gone from both the Mac and the server are dropped from client.json.
func TestSyncPrunesGoneItems(t *testing.T) {
	root := t.TempDir()
	if err := (opened{SyncInclude: []string{"agents/b.md", "rules/never.md", "skills/old"}}).save(root); err != nil {
		t.Fatal(err)
	}
	h := syncStart(t, root)
	h.press("S")
	if got := loadOpened(root).SyncInclude; !reflect.DeepEqual(got, []string{"agents/b.md"}) {
		t.Fatalf("include = %q", got)
	}
}

func TestOpenedSeedAndKeepSync(t *testing.T) {
	o := opened{SyncExclude: []string{"skills/b"}}
	if !o.seedSync([]string{"skills/c", "skills/b", "agents/a.md"}) || !reflect.DeepEqual(o.SyncInclude, []string{"agents/a.md", "skills/c"}) || o.SyncExclude != nil {
		t.Fatalf("seed: %+v", o)
	}
	if o.seedSync([]string{"skills/d"}) || !reflect.DeepEqual(o.SyncInclude, []string{"agents/a.md", "skills/c"}) {
		t.Fatalf("seeded twice: %+v", o)
	}
	if o := (opened{}); !o.seedSync(nil) || o.SyncInclude == nil || len(o.SyncInclude) != 0 {
		t.Fatalf("seed with nothing synced: %+v", o)
	}
	// an item known only from a Mac warning still exists
	exists := func(it string) bool { return it == "skills/c" }
	if !o.keepSync(exists) || !reflect.DeepEqual(o.SyncInclude, []string{"skills/c"}) {
		t.Fatalf("keep: %+v", o)
	}
	if o.keepSync(exists) {
		t.Fatal("nothing to drop")
	}
}

func TestSyncConfirmAndApply(t *testing.T) {
	root := t.TempDir()
	if err := (opened{SyncInclude: []string{"agents/b.md", "skills/a", "skills/gone"}}).save(root); err != nil {
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
	if got := loadOpened(root).SyncInclude; !reflect.DeepEqual(got, []string{"agents/b.md", "skills/a", "skills/gone"}) {
		t.Fatalf("include after the sync = %q", got)
	}
}

func TestSyncUpToDate(t *testing.T) {
	h := syncStart(t, t.TempDir())
	h.be.plan = api.SyncPlan{Dir: "/srv", Items: []api.SyncItem{{Name: "agents/b.md", Status: "same", Synced: true}}}
	h.press("S", "s")
	if v := view(h); !strings.Contains(v, "Up to date") || strings.Contains(v, "to bx serve?") || len(h.be.applies) != 0 {
		t.Fatalf("s with no changes:\n%s", v)
	}
	h2 := syncStart(t, t.TempDir())
	h2.be.plan = api.SyncPlan{Dir: "/srv", Items: []api.SyncItem{{Name: "skills/a", Status: "new", Add: []string{"skills/a/SKILL.md"}}}}
	h2.press("S", "s")
	if v := view(h2); !strings.Contains(v, "Nothing selected") || strings.Contains(v, "to bx serve?") || len(h2.be.applies) != 0 {
		t.Fatalf("s with nothing selected:\n%s", v)
	}
}

func TestSyncDropsAStaleReply(t *testing.T) {
	h := syncStart(t, t.TempDir())
	h.press("S")
	old := h.m.syncSeq
	h.press("r")
	h.update(syncPlanMsg{seq: old, err: errors.New("an old failure")})
	if v := view(h); strings.Contains(v, "an old failure") || !strings.Contains(v, "~ changed") {
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
	if v := view(h); !strings.Contains(v, "~ changed") || strings.Contains(v, "unreachable") {
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

// A Mac file the scan can't read makes its item an amber warning that can't be selected, and
// an unreadable folder a warning over the list; the other items still sync.
func TestSyncUnreadableIsAWarning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	h := syncStart(t, t.TempDir())
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	macFile(t, "skills/a/secret.md", "s")
	macFile(t, "rules/r.md", "r")
	for _, rel := range []string{"skills/a/secret.md", "rules"} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
	}
	h.be.plan.Items = append(h.be.plan.Items, api.SyncItem{Name: "rules/old.md", Status: "removed", Delete: []string{"rules/old.md"}, Synced: true})
	h.press("S")
	var got []string
	for _, f := range h.be.planReqs[0].Files {
		got = append(got, f.Path)
	}
	if want := []string{"agents/b.md", "agents/x.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest = %q", got)
	}
	v := view(h)
	for _, want := range []string{"can't read skills/a/secret.md: permission denied", "can't read rules: permission denied", "1 server-only item kept", "• 2 to sync"} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q:\n%s", want, v)
		}
	}
	if got, want := h.m.settings.SyncScope(), []string{"agents/b.md", "agents/x.md", "skills/gone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scope = %q, want %q", got, want)
	}
}

func TestOpenedRoundTrip(t *testing.T) {
	root := t.TempDir()
	o := opened{Projects: []string{"a", "b"}, Current: "b", SyncInclude: []string{"rules/x.md", "skills/a"}}
	if err := o.save(root); err != nil {
		t.Fatal(err)
	}
	if got := loadOpened(root); !reflect.DeepEqual(got, o) {
		t.Fatalf("got %+v", got)
	}
	// a file from before sync_include: not seeded yet, the rest intact
	if err := os.WriteFile(openedFile(root), []byte(`{"projects":["a"],"current":"a"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadOpened(root); !reflect.DeepEqual(got, opened{Projects: []string{"a"}, Current: "a"}) || got.SyncInclude != nil {
		t.Fatalf("old file: %+v", got)
	}
	// an old unselected list is read, for the seed
	if err := os.WriteFile(openedFile(root), []byte(`{"projects":["a"],"sync_exclude":["skills/a"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadOpened(root); !reflect.DeepEqual(got.SyncExclude, []string{"skills/a"}) {
		t.Fatalf("old exclude: %+v", got)
	}
	// nothing selected is saved, and differs from not seeded
	if err := (opened{Projects: []string{"a"}, SyncInclude: []string{}}).save(root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(openedFile(root)); !strings.Contains(string(b), `"sync_include": []`) {
		t.Fatalf("%s", b)
	}
	if got := loadOpened(root).SyncInclude; got == nil || len(got) != 0 {
		t.Fatalf("empty include = %#v", got)
	}
	// not seeded and no old list: both fields are left out
	if err := (opened{Projects: []string{"a"}}).save(root); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(openedFile(root)); strings.Contains(string(b), "sync_include") || strings.Contains(string(b), "sync_exclude") {
		t.Fatalf("%s", b)
	}
}
