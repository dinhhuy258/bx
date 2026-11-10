package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// promptLines is how far into a transcript FirstPrompt and AITitle look. The first prompt is
// near the top, after a few queue and command lines, and Claude's title follows it.
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
func FirstPrompt(path string) string { return scan(path, userText) }

// AITitle returns the title Claude wrote for the session in the transcript at path (its
// "ai-title" line), or "" when there is none yet.
func AITitle(path string) string { return scan(path, aiTitle) }

// scan returns the first non-empty pick of the first promptLines lines of the file at path.
func scan(path string, pick func([]byte) string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	for range promptLines {
		line, err := r.ReadBytes('\n')
		if text := pick(line); text != "" {
			return text
		}
		if err != nil {
			return ""
		}
	}
	return ""
}

var aiTitleType = []byte(`"ai-title"`)

func aiTitle(line []byte) string {
	if !bytes.Contains(line, aiTitleType) { // most lines are large and not it: skip the decode
		return ""
	}
	var l struct {
		Type    string `json:"type"`
		AITitle string `json:"aiTitle"`
	}
	if json.Unmarshal(line, &l) != nil || l.Type != "ai-title" {
		return ""
	}
	return strings.Join(strings.Fields(l.AITitle), " ")
}

// userText is the prompt on line with its whitespace collapsed, as a title wants it.
func userText(line []byte) string { return strings.Join(strings.Fields(promptText(line)), " ") }

// promptText is what the user typed on transcript line, trimmed, or "" when the line is not a
// prompt (see FirstPrompt).
func promptText(line []byte) string {
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
		text = c[1] + " " + strings.TrimSpace(a[1])
	} else if strings.HasPrefix(text, "<") {
		return ""
	}
	return strings.TrimSpace(text)
}

const (
	tailChunk = 256 << 10 // Prompts.Last first reads this much of a transcript's end
	tailMax   = 16 << 20  // and doubles it up to this while it finds no prompt
	// promptMax is the most runes of a prompt Prompts.Last keeps: the clients show two lines of
	// it at most, and it goes out in every snapshot
	promptMax = 500
	// promptIdle is how long Prompts remembers a transcript nobody asked about, e.g. of a
	// session that ended
	promptIdle = time.Hour
)

// Prompts finds the last prompt of transcripts. It remembers how far it read each one, so the
// next call reads only what Claude added since, and forgets a transcript not asked about for
// promptIdle. The zero value is ready; it is safe for concurrent use.
type Prompts struct {
	mu    sync.Mutex
	seen  map[string]promptSeen
	swept time.Time // the last time idle transcripts were forgotten
}

type promptSeen struct {
	end  int64     // bytes read, up to the end of the last whole line
	text string    // the last prompt in them
	used time.Time // the last call for it
}

// Last returns the last thing the user typed in the transcript at path, trimmed but with its
// lines kept and cut to promptMax runes, or "" when there is none (or none in its last tailMax
// bytes).
func (p *Prompts) Last(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	size := fi.Size()
	p.mu.Lock()
	s, ok := p.seen[path]
	p.mu.Unlock()
	switch {
	case !ok || size < s.end || size-s.end > tailMax: // new, rewritten, or too much added to read it all
		s = lastFromEnd(f, size)
	case size > s.end:
		buf := make([]byte, size-s.end)
		if _, err := f.ReadAt(buf, s.end); err != nil {
			return s.text
		}
		text, n := lastPrompt(buf, false)
		s.end += int64(n)
		if text != "" {
			s.text = text
		}
	}
	p.remember(path, s, time.Now())
	return s.text
}

// remember stores what Last read of path at now, and forgets the transcripts idle for
// promptIdle, looking at most once per promptIdle.
func (p *Prompts) remember(path string, s promptSeen, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = map[string]promptSeen{}
	}
	s.used = now
	p.seen[path] = s
	if now.Sub(p.swept) < promptIdle {
		return
	}
	p.swept = now
	for k, v := range p.seen {
		if now.Sub(v.used) >= promptIdle {
			delete(p.seen, k)
		}
	}
}

// lastFromEnd reads the end of f (size bytes) for its last prompt, from tailChunk bytes and
// doubling while there is none.
func lastFromEnd(f *os.File, size int64) promptSeen {
	for n := int64(tailChunk); ; n *= 2 {
		start := max(0, size-n)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			return promptSeen{}
		}
		text, used := lastPrompt(buf, start > 0)
		if text != "" || start == 0 || n >= tailMax { // tailMax: give up rather than read it all
			return promptSeen{end: start + int64(used), text: text}
		}
	}
}

// lastPrompt returns the last prompt among the whole lines of buf, and the length of buf up to
// the end of its last whole line. partial skips the first line, which starts before buf.
func lastPrompt(buf []byte, partial bool) (string, int) {
	used := bytes.LastIndexByte(buf, '\n') + 1
	lines := buf[:used]
	if partial {
		i := bytes.IndexByte(lines, '\n')
		lines = lines[i+1:]
	}
	for len(lines) > 0 {
		i := bytes.LastIndexByte(lines[:len(lines)-1], '\n')
		if text := promptText(lines[i+1:]); text != "" {
			if r := []rune(text); len(r) > promptMax {
				text = strings.TrimSpace(string(r[:promptMax]))
			}
			return text, used
		}
		lines = lines[:i+1]
	}
	return "", used
}
