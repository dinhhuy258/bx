package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
	"github.com/dinhhuy258/bx/internal/git"
)

const sample = `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@ func main() {
 one
-two
+TWO
 three
\ No newline at end of file
diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+--- not a header
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 4444444..0000000
--- a/gone.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/old name.txt b/new name.txt
similarity 100%
rename from old name.txt
rename to new name.txt
diff --git a/img.png b/img.png
index 5555555..6666666 100644
Binary files a/img.png and b/img.png differ
diff --git "a/tab\there.txt" "b/tab\there.txt"
index 7777777..8888888 100644
--- "a/tab\there.txt"
+++ "b/tab\there.txt"
@@ -1 +1 @@
-a
+b
`

func TestParse(t *testing.T) {
	fs := Parse([]byte(sample))
	if len(fs) != 6 {
		t.Fatalf("%d files: %+v", len(fs), fs)
	}
	a := fs[0]
	if a.Path != "a.go" || a.Status != "modified" || a.Add != 1 || a.Del != 1 || len(a.Hunks) != 1 || a.Old != "" {
		t.Fatalf("a.go: %+v", a)
	}
	rows := a.Hunks[0].Rows
	want := []api.DiffRow{
		{Kind: "ctx", Old: 1, New: 1, Text: "one"}, {Kind: "del", Old: 2, Text: "two"},
		{Kind: "add", New: 2, Text: "TWO"}, {Kind: "ctx", Old: 3, New: 3, Text: "three"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if fs[1].Path != "new.txt" || fs[1].Status != "added" || fs[1].Hunks[0].Rows[0].Text != "--- not a header" {
		t.Errorf("new.txt: %+v", fs[1])
	}
	if fs[2].Path != "gone.txt" || fs[2].Status != "deleted" || fs[2].Del != 1 {
		t.Errorf("gone.txt: %+v", fs[2])
	}
	if fs[3].Path != "new name.txt" || fs[3].Old != "old name.txt" || fs[3].Status != "renamed" {
		t.Errorf("rename: %+v", fs[3])
	}
	if fs[4].Path != "img.png" || fs[4].Folded != "binary file" {
		t.Errorf("binary: %+v", fs[4])
	}
	if fs[5].Path != "tab\there.txt" || fs[5].Add != 1 {
		t.Errorf("quoted: %+v", fs[5])
	}
}

func TestParseFoldsALongFile(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/big b/big\n--- a/big\n+++ b/big\n@@ -0,0 +1,2001 @@\n")
	for i := 0; i <= MaxRows; i++ {
		b.WriteString("+x\n")
	}
	f := Parse([]byte(b.String()))[0]
	if f.Hunks != nil || f.Folded != "2001 lines changed · too long to show" || f.Add != 2001 {
		t.Fatalf("%+v", f.Folded)
	}
}

// storyRepo is a repo on main with a.txt, and a branch feat/x that changed a.txt in a commit,
// has an unstaged change to b.txt and an untracked c.txt.
func storyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, s string) { os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644) }
	run("init", "-q", "-b", "main")
	write("a.txt", "one\n")
	write("b.txt", "bee\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	run("checkout", "-q", "-b", "feat/x")
	write("a.txt", "one\ntwo\n")
	run("commit", "-qam", "two")
	write("b.txt", "BEE\n")
	write("c.txt", "new\n")
	return dir
}

func TestDiffSeesCommitsChangesAndUntrackedFiles(t *testing.T) {
	dir := storyRepo(t)
	g := git.New()
	r, err := Diff(context.Background(), g, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range r.Files {
		got = append(got, f.Status+" "+f.Path)
	}
	if strings.Join(got, ",") != "modified a.txt,modified b.txt,added c.txt" || r.Total != 3 || r.Note != "" {
		t.Fatalf("files %v total %d note %q", got, r.Total, r.Note)
	}
	// the real index is untouched: c.txt is still untracked
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if !strings.Contains(string(out), "?? c.txt") {
		t.Fatalf("status:\n%s", out)
	}
}

func TestDiffFallsBackToHEADWithoutAMergeBase(t *testing.T) {
	dir := storyRepo(t)
	r, err := Diff(context.Background(), git.New(), dir, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Note, "no merge-base with nope") || len(r.Files) != 2 {
		t.Fatalf("%+v", r)
	}
}
