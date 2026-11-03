package client

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dinhhuy258/bx/internal/uploads"
)

// clipFile is what attach found on this machine's clipboard: a file copied in Finder (⌘C), or
// an image such as a screenshot taken with ⌃⇧⌘4.
type clipFile struct {
	name string
	data []byte
}

// errNoClip means the clipboard holds neither a file nor an image (text, or nothing).
var errNoClip = errors.New("no image or file on the clipboard")

// readClipboard reads the clipboard of the machine running bx, not the server's: Claude Code
// on the server can only see the server's clipboard, which is why attach uploads it.
func readClipboard() (clipFile, error) {
	if runtime.GOOS == "darwin" {
		return readMacClipboard()
	}
	return readLinuxClipboard()
}

// readMacClipboard tries a copied file first, since Finder also puts the file's icon on the
// clipboard as an image, then a PNG image.
func readMacClipboard() (clipFile, error) {
	if out, err := exec.Command("osascript", "-e", "POSIX path of (the clipboard as «class furl»)").Output(); err == nil {
		return readClipPath(strings.TrimSpace(string(out)))
	}
	out, err := exec.Command("osascript", "-e", "the clipboard as «class PNGf»").Output()
	if err != nil {
		return clipFile{}, errNoClip
	}
	data, err := parseAppleData(string(out))
	if err != nil {
		return clipFile{}, err
	}
	return clipFile{name: "clipboard.png", data: data}, nil
}

// parseAppleData decodes AppleScript's «data PNGf89504E47…» into the bytes it stands for.
func parseAppleData(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	const pre = "«data PNGf"
	if !strings.HasPrefix(s, pre) || !strings.HasSuffix(s, "»") {
		return nil, errNoClip
	}
	data, err := hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(s, pre), "»"))
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("clipboard image: %w", errNoClip)
	}
	return data, nil
}

// readLinuxClipboard reads a PNG image with wl-paste or xclip, whichever is installed.
func readLinuxClipboard() (clipFile, error) {
	for _, c := range [][]string{
		{"wl-paste", "--no-newline", "--type", "image/png"},
		{"xclip", "-selection", "clipboard", "-target", "image/png", "-out"},
	} {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		if out, err := exec.Command(c[0], c[1:]...).Output(); err == nil && len(out) > 0 {
			return clipFile{name: "clipboard.png", data: out}, nil
		}
	}
	return clipFile{}, errNoClip
}

// readClipPath reads a copied file. A folder cannot be attached.
func readClipPath(p string) (clipFile, error) {
	st, err := os.Stat(p)
	if err != nil {
		return clipFile{}, err
	}
	if st.IsDir() {
		return clipFile{}, fmt.Errorf("%s is a folder; copy a file", filepath.Base(p))
	}
	if st.Size() > uploads.MaxFile { // the server refuses it; do not read it all first
		return clipFile{}, fmt.Errorf("%s is over 20 MB", filepath.Base(p))
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return clipFile{}, err
	}
	return clipFile{name: filepath.Base(p), data: data}, nil
}
