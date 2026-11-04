package review

import (
	"strings"
	"testing"

	"github.com/dinhhuy258/bx/internal/api"
)

func TestPrompt(t *testing.T) {
	cs := []api.Comment{
		{File: "internal/git/git.go", Side: "new", Line: 88, Code: "\tmb, err := g.mergeBase(dir, base)", Body: "Wrap this error so it says which base had no merge-base."},
		{File: "cmd/bx/main.go", Side: "old", Line: 33, Code: "usage()", Body: "Keep the usage() hint\x1b[31m when bx runs with no arguments.\r\nAnd say why."},
	}
	want := "Review comments on your changes in login-timeout (diff vs develop):\n" +
		"\n" +
		"1. cmd/bx/main.go:33 (a line you removed)\n" +
		"   > usage()\n" +
		"   Keep the usage() hint[31m when bx runs with no arguments.\n" +
		"   And say why.\n" +
		"\n" +
		"2. internal/git/git.go:88\n" +
		"   > mb, err := g.mergeBase(dir, base)\n" +
		"   Wrap this error so it says which base had no merge-base.\n" +
		"\n" +
		"Address each one, then tell me what you changed."
	if got := Prompt("login-timeout", "develop", cs); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	ranges := []api.Comment{
		{File: "a.go", Side: "new", StartLine: 89, Line: 92, Code: "\tmb, err := g.mergeBase(dir, base)\n\tif err != nil {\n\t\treturn err\n\t}", Body: "Wrap it"},
		{File: "a.go", Side: "new", Line: 10, Code: "x", Body: "first"},
		{File: "a.go", Side: "old", StartLine: 3, Line: 4, Code: "a\nb", Body: "why"},
	}
	want = "Review comments on your changes in s (diff vs HEAD):\n" +
		"\n" +
		"1. a.go:3-4 (lines you removed)\n" +
		"   > a\n" +
		"   > b\n" +
		"   why\n" +
		"\n" +
		"2. a.go:10\n" +
		"   > x\n" +
		"   first\n" +
		"\n" +
		"3. a.go:89-92\n" +
		"   > mb, err := g.mergeBase(dir, base)\n" +
		"   > if err != nil {\n" +
		"   > \treturn err\n" +
		"   > }\n" +
		"   Wrap it\n" +
		"\n" +
		"Address each one, then tell me what you changed."
	if got := Prompt("s", "", ranges); got != want {
		t.Fatalf("ranges got:\n%s\nwant:\n%s", got, want)
	}
	if got := Prompt("s", "", cs[:1]); got[:52] != "Review comments on your changes in s (diff vs HEAD):" {
		t.Fatalf("empty base: %s", got)
	}
}

func TestExplainPrompt(t *testing.T) {
	e := api.Explain{File: "internal/git/git.go", Side: "new", StartLine: 89, Line: 92,
		Code: "\tmb, err := g.mergeBase(dir, base)\n\tif err != nil {\n\t\treturn err\n\t}"}
	want := "Explain internal/git/git.go lines 89-92 of the changes on this branch:\n" +
		"\n" +
		"    mb, err := g.mergeBase(dir, base)\n" +
		"    if err != nil {\n" +
		"    \treturn err\n" +
		"    }\n" +
		"\n" +
		"What does this code do, and why is it written this way?\n" +
		"\n" +
		"Use what you know from our conversation. Answer in a few short paragraphs of plain text. Don't change any files."
	if got := ExplainPrompt(e, true); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	e = api.Explain{File: "a.go", Side: "old", Line: 4, Code: "x()", Question: "Why was this\x1b[31m removed?"}
	got := ExplainPrompt(e, false)
	if !strings.HasPrefix(got, "Explain a.go line 4 (a line the changes removed) of the changes on this branch:\n\n    x()\n\nWhy was this[31m removed?\n\n") ||
		!strings.HasSuffix(got, "Read the files you need. Answer in a few short paragraphs of plain text. Don't change any files.") {
		t.Fatalf("code only: %q", got)
	}
}
