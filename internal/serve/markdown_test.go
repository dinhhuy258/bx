package serve

import (
	"strings"
	"testing"
)

func TestRenderMarkdown(t *testing.T) {
	src := "# Lease `plan`\n\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n" +
		"- [x] done\n\n" +
		"<script>alert(1)</script>\n\n" +
		"[bad](javascript:alert(1))\n\n" +
		"```go\nfunc main() {}\n```\n\n" +
		"```mermaid\ngraph TD; A-->B\n```\n"
	b, err := renderMarkdown([]byte(src), "plan.md")
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, want := range []string{
		"<!doctype html>", `name="viewport"`, "<title>Lease plan</title>",
		"<table>", `type="checkbox"`, `class="chroma"`, `<pre class="mermaid">graph TD; A--&gt;B`,
		"prefers-color-scheme: dark", "mermaid/11.4.1/mermaid.min.js", `href="plan.md?raw"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"<script>alert", "javascript:"} {
		if strings.Contains(page, bad) {
			t.Errorf("page has %q", bad)
		}
	}
	b, _ = renderMarkdown([]byte("no heading\n"), "notes.md")
	if !strings.Contains(string(b), "<title>notes</title>") {
		t.Error("title falls back to the file name")
	}
}
