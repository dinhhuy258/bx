package review

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dinhhuy258/bx/internal/api"
)

const (
	MaxComments = 200  // comments in one story's review
	MaxBody     = 4096 // bytes of a comment's text or code
)

var (
	ErrTooMany     = fmt.Errorf("a review holds %d comments at most", MaxComments)
	ErrTooLong     = fmt.Errorf("a comment is over %d bytes", MaxBody)
	ErrUnknown     = errors.New("a comment was removed on another device")
	ErrSentChanged = errors.New("a sent comment can't be changed")
)

// Apply checks the comment list a client sends (PUT) against the stored one and returns what
// to store. A new comment (no id) gets one and can't be sent yet. A sent comment may only move
// to other lines (it follows the code); its text and its sent stamp stay. A range that starts
// on its last line is stored as one line. A comment left out
// is deleted. Outdated is the server's to say and is dropped.
func Apply(old, next []api.Comment) ([]api.Comment, error) {
	if len(next) > MaxComments {
		return nil, ErrTooMany
	}
	byID := make(map[string]api.Comment, len(old))
	for _, c := range old {
		byID[c.ID] = c
	}
	seen := map[string]bool{}
	out := make([]api.Comment, 0, len(next))
	for _, c := range next {
		c.Outdated = false
		c.Body = strings.TrimSpace(c.Body)
		if len(c.Body) > MaxBody || len(c.Code) > MaxBody {
			return nil, ErrTooLong
		}
		if c.File == "" || (c.Side != "new" && c.Side != "old") || c.Line < 1 || c.Body == "" {
			return nil, fmt.Errorf("a comment needs a file, a side (new or old), a line and a text (%s:%d)", c.File, c.Line)
		}
		if c.StartLine == c.Line {
			c.StartLine = 0
		}
		if c.StartLine < 0 || c.StartLine > c.Line {
			return nil, fmt.Errorf("a comment's lines run backwards (%s:%d-%d)", c.File, c.StartLine, c.Line)
		}
		switch o, ok := byID[c.ID]; {
		case c.ID == "":
			if c.SentTo != 0 || c.SentAt != 0 {
				return nil, ErrSentChanged
			}
			c.ID = NewID()
		case !ok:
			return nil, ErrUnknown
		case seen[c.ID]:
			return nil, fmt.Errorf("comment %s is listed twice", c.ID)
		case o.SentAt != 0:
			o.StartLine, o.Line, o.Outdated = c.StartLine, c.Line, false
			if c != o {
				return nil, ErrSentChanged
			}
		case c.SentTo != 0 || c.SentAt != 0:
			return nil, ErrSentChanged
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, nil
}

// NewID is a random comment id: 16 hex digits.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Unsent is the comments not sent yet, in order.
func Unsent(cs []api.Comment) []api.Comment {
	return slices.DeleteFunc(slices.Clone(cs), func(c api.Comment) bool { return c.SentAt != 0 })
}

// MarkSent returns a copy of cs with the comments ids stamped as sent to session num at at.
func MarkSent(cs []api.Comment, ids []string, num int, at int64) []api.Comment {
	out := slices.Clone(cs)
	for i := range out {
		if slices.Contains(ids, out[i].ID) {
			out[i].SentTo, out[i].SentAt = num, at
		}
	}
	return out
}

// ClearSent drops the sent comments.
func ClearSent(cs []api.Comment) []api.Comment { return Unsent(cs) }

// Counts is how many comments are unsent and sent.
func Counts(cs []api.Comment) (unsent, sent int) {
	for _, c := range cs {
		if c.SentAt != 0 {
			sent++
		} else {
			unsent++
		}
	}
	return unsent, sent
}
