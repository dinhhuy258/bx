package review

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/dinhhuy258/bx/internal/api"
)

// MaxQuote is how many lines of a range's code the prompt quotes.
const MaxQuote = 12

// Prompt is the text pasted into Claude: the comments by file and line, each with its lines of
// code (a range's first MaxQuote, without the indent they share). Control characters (ESC above all) are dropped so the text can't end the bracketed
// paste or drive the terminal. It has no trailing newline: nothing is submitted.
func Prompt(story, base string, cs []api.Comment) string {
	if base == "" {
		base = "HEAD"
	}
	sorted := slices.Clone(cs)
	slices.SortStableFunc(sorted, func(a, b api.Comment) int {
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.First(), b.First()), cmp.Compare(a.Line, b.Line))
	})
	var b strings.Builder
	fmt.Fprintf(&b, "Review comments on your changes in %s (diff vs %s):\n", oneLine(story), oneLine(base))
	for i, c := range sorted {
		where := fmt.Sprintf("%s:%d", oneLine(c.File), c.Line)
		switch {
		case c.First() < c.Line && c.Side == "old":
			where = fmt.Sprintf("%s:%d-%d (lines you removed)", oneLine(c.File), c.First(), c.Line)
		case c.First() < c.Line:
			where = fmt.Sprintf("%s:%d-%d", oneLine(c.File), c.First(), c.Line)
		case c.Side == "old":
			where += " (a line you removed)"
		}
		fmt.Fprintf(&b, "\n%d. %s\n", i+1, where)
		for _, l := range c.Quote(MaxQuote) {
			fmt.Fprintf(&b, "   > %s\n", oneLine(l))
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

// MaxExplainQuote is how many lines of code an explain prompt quotes.
const MaxExplainQuote = 60

// ExplainPrompt asks Claude about e's lines: the code, the question (or what it does and
// why), and how to answer. withSession says Claude has the session's conversation.
func ExplainPrompt(e api.Explain, withSession bool) string {
	c := e.AsComment()
	where := fmt.Sprintf("%s line %d", oneLine(e.File), e.Line)
	if c.First() < c.Line {
		where = fmt.Sprintf("%s lines %d-%d", oneLine(e.File), c.First(), c.Line)
	}
	if e.Side == "old" {
		if c.First() < c.Line {
			where += " (lines the changes removed)"
		} else {
			where += " (a line the changes removed)"
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Explain %s of the changes on this branch:\n\n", where)
	for _, l := range c.Quote(MaxExplainQuote) {
		fmt.Fprintf(&b, "    %s\n", oneLine(l))
	}
	q := strings.TrimSpace(clean(e.Question))
	if q == "" {
		q = "What does this code do, and why is it written this way?"
	}
	fmt.Fprintf(&b, "\n%s\n\n", q)
	if withSession {
		b.WriteString("Use what you know from our conversation. ")
	} else {
		b.WriteString("Read the files you need. ")
	}
	b.WriteString("Answer in a few short paragraphs of plain text. Don't change any files.")
	return b.String()
}
