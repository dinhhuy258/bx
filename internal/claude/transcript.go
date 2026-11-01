package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// promptLines is how far into a transcript FirstPrompt looks. The first prompt is near the
// top, after a few queue and command lines.
const promptLines = 100

var unsafeRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// TranscriptPath is where Claude keeps the transcript of session uuid run in cwd: under
// projects (~/.claude/projects), in a folder named after cwd with every other character a "-".
func TranscriptPath(projects, cwd, uuid string) string {
	return filepath.Join(projects, unsafeRe.ReplaceAllString(cwd, "-"), uuid+".jsonl")
}

var (
	commandRe = regexp.MustCompile(`<command-name>([^<]*)</command-name>`)
	argsRe    = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

// FirstPrompt returns the first thing the user typed in the transcript at path, or "" when
// there is none yet. A slash command counts only with arguments; Claude's own lines (meta,
// command output, tool results) are skipped.
func FirstPrompt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	for range promptLines {
		line, err := r.ReadBytes('\n')
		if text := userText(line); text != "" {
			return text
		}
		if err != nil {
			return ""
		}
	}
	return ""
}

func userText(line []byte) string {
	var l struct {
		Type    string `json:"type"`
		IsMeta  bool   `json:"isMeta"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &l) != nil || l.Type != "user" || l.IsMeta {
		return ""
	}
	var text string
	if json.Unmarshal(l.Message.Content, &text) != nil {
		var blocks []struct{ Type, Text string }
		if json.Unmarshal(l.Message.Content, &blocks) != nil {
			return ""
		}
		for _, b := range blocks {
			if b.Type == "text" && !strings.HasPrefix(b.Text, "<") {
				text = b.Text
				break
			}
		}
	}
	if c := commandRe.FindStringSubmatch(text); c != nil {
		a := argsRe.FindStringSubmatch(text)
		if a == nil || strings.TrimSpace(a[1]) == "" {
			return "" // /model, /clear: no prompt yet
		}
		text = c[1] + " " + a[1]
	} else if strings.HasPrefix(text, "<") {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}
