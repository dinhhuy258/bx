package client

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAppleData(t *testing.T) {
	got, err := parseAppleData("«data PNGf89504E47»\n")
	if err != nil || string(got) != "\x89PNG" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", "hello", "«data TIFF00»", "«data PNGf»", "«data PNGfZZ»"} {
		if _, err := parseAppleData(bad); !errors.Is(err, errNoClip) {
			t.Errorf("%q: want errNoClip, got %v", bad, err)
		}
	}
}

func TestReadClipPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notes.pdf")
	if err := os.WriteFile(p, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := readClipPath(p)
	if err != nil || f.name != "notes.pdf" || string(f.data) != "pdf" {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := readClipPath(dir); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("a folder must be refused, got %v", err)
	}
}
