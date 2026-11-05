package prwatch

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/dinhhuy258/bx/internal/ui"
)

// The instructions after the data when Settings → Claude leaves them empty.
const (
	DefaultFix     = "Find the cause, fix it, run the failing test locally, then commit and push."
	DefaultAddress = "Address each one, then commit and push. For each comment, tell me what you changed."
)

// LogLines is how many lines of a failed run's log go into the fix prompt.
const LogLines = 40

func head(story string, p ui.PRInfo) string {
	if p.Repo != "" {
		return p.Repo + " · " + story
	}
	return story
}

func tail(p ui.PRInfo, instr, def string) string {
	instr = clean(strings.TrimSpace(instr))
	if instr == "" {
		instr = def
	}
	if p.Repo != "" {
		instr += "\nWork in " + p.Repo + "/ only, and push that repo."
	}
	return instr
}

// FixPrompt asks Claude to fix the failed checks of p. logs are the failed-step logs by run id.
func FixPrompt(story, branch string, p ui.PRInfo, logs map[int64]string, instr string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CI failed on PR #%d (%s → %s).\n", p.Number, oneLine(branch), oneLine(p.Base))
	shown := map[int64]bool{}
	for _, c := range p.FailedChecks() {
		fmt.Fprintf(&b, "\nFailed check: %s\n", oneLine(c.Name))
		if c.URL != "" {
			fmt.Fprintf(&b, "Run: %s\n", oneLine(c.URL))
		}
		if c.RunID == 0 || shown[c.RunID] {
			continue
		}
		shown[c.RunID] = true
		if log := logTail(logs[c.RunID], LogLines); log != "" {
			fmt.Fprintf(&b, "Last %d lines of the failed step:\n", LogLines)
			for _, l := range strings.Split(log, "\n") {
				fmt.Fprintf(&b, "  %s\n", l)
			}
		}
		fmt.Fprintf(&b, "You can read the full log with `gh run view %d --log-failed`.\n", c.RunID)
	}
	b.WriteString("\n" + tail(p, instr, DefaultFix))
	return b.String()
}

// AddressPrompt asks Claude to address the threads of p whose ids are in ids, in file order,
// each with its code and whole conversation. In a workspace paths start with the repo folder.
func AddressPrompt(story string, p ui.PRInfo, ids []string, instr string) string {
	var ts []ui.PRThread
	for _, t := range p.Threads {
		if slices.Contains(ids, t.ID) && !t.Resolved {
			ts = append(ts, t)
		}
	}
	slices.SortStableFunc(ts, func(a, b ui.PRThread) int {
		return cmp.Or(strings.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
	})
	prefix := ""
	if p.Repo != "" {
		prefix = p.Repo + "/"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Review comments on PR #%d (%s):\n", p.Number, oneLine(head(story, p)))
	for i, t := range ts {
		who := ""
		if len(t.Comments) > 0 {
			who = " · " + oneLine(t.Comments[0].Author)
		}
		fmt.Fprintf(&b, "\n%d. %s%s%s\n", i+1, prefix, oneLine(t.Where()), who)
		if t.Code != "" {
			for _, l := range strings.Split(clean(t.Code), "\n") {
				fmt.Fprintf(&b, "   > %s\n", l)
			}
		}
		for j, c := range t.Comments {
			for k, l := range strings.Split(clean(strings.TrimSpace(c.Body)), "\n") {
				if j > 0 && k == 0 {
					l = oneLine(c.Author) + ": " + l
				}
				fmt.Fprintf(&b, "   %s\n", l)
			}
		}
	}
	b.WriteString("\n" + tail(p, instr, DefaultAddress))
	return b.String()
}

func logTail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(clean(s), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// clean drops control characters but newlines and tabs, turns CRLF into LF, and strips ANSI
// colour sequences (gh run view prints them): nothing may end the bracketed paste.
func clean(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' { // CSI … final byte
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, b.String())
}

func oneLine(s string) string { return strings.Join(strings.Fields(clean(s)), " ") }
