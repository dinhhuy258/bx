package review

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/dinhhuy258/bx/internal/api"
)

// Prompt is the text pasted into Claude: the comments by file and line, each with its line of
// code. Control characters (ESC above all) are dropped so the text can't end the bracketed
// paste or drive the terminal. It has no trailing newline: nothing is submitted.
func Prompt(story, base string, cs []api.Comment) string {
	if base == "" {
		base = "HEAD"
	}
	sorted := slices.Clone(cs)
	slices.SortStableFunc(sorted, func(a, b api.Comment) int {
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	var b strings.Builder
	fmt.Fprintf(&b, "Review comments on your changes in %s (diff vs %s):\n", oneLine(story), oneLine(base))
	for i, c := range sorted {
		where := fmt.Sprintf("%s:%d", oneLine(c.File), c.Line)
		if c.Side == "old" {
			where += " (a line you removed)"
		}
		fmt.Fprintf(&b, "\n%d. %s\n", i+1, where)
		if code := strings.TrimSpace(oneLine(c.Code)); code != "" {
			fmt.Fprintf(&b, "   > %s\n", code)
		}
		for _, l := range strings.Split(clean(c.Body), "\n") {
			fmt.Fprintf(&b, "   %s\n", l)
		}
	}
	b.WriteString("\nAddress each one, then tell me what you changed.")
	return b.String()
}

// clean drops control characters but newlines and tabs, and turns CRLF into LF.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\r\n", "\n"))
}

// oneLine is clean with newlines dropped too.
func oneLine(s string) string { return strings.ReplaceAll(clean(s), "\n", " ") }
