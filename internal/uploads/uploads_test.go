package uploads

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 2, 9, 30, 15, 0, time.Local)

func TestSafeName(t *testing.T) {
	long := strings.Repeat("a", 300) + ".jpeg"
	for _, c := range []struct{ in, want string }{
		{"photo.jpg", "photo.jpg"},
		{"../../etc/passwd", "passwd"},
		{`a\b.png`, "b.png"},
		{".bashrc", "bashrc"},
		{"...", "file"},
		{"", "file"},
		{"dir/", "file"},
		{"my photo (1).HEIC", "my_photo__1_.HEIC"},
		{"ảnh.jpg", "_nh.jpg"},
		{"a\x00b\nc.txt", "a_b_c.txt"},
		{long, strings.Repeat("a", 95) + ".jpeg"},
	} {
		if got := SafeName(c.in); got != c.want {
			t.Errorf("SafeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := SafeName(strings.Repeat("b", 300) + "." + strings.Repeat("x", 40)); len(got) != maxName {
		t.Errorf("long extension: %d bytes", len(got))
	}
}

func TestSaveWritesAtomically(t *testing.T) {
	base := filepath.Join(t.TempDir(), "uploads")
	path, err := Save(base, "home-network", 3, "photo.jpg", []byte("jpeg"), now)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "home-network", "3", "20261102-093015-photo.jpg")
	if path != want || !filepath.IsAbs(path) {
		t.Fatalf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "jpeg" {
		t.Fatalf("content %q %v", got, err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(Dir(base, "home-network", 3))
	if len(entries) != 1 {
		t.Fatalf("session dir holds %v", entries)
	}
}

func TestSaveNeverClobbers(t *testing.T) {
	base := t.TempDir()
	first, err := Save(base, "home-network", 3, "photo.jpg", []byte("one"), now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Save(base, "home-network", 3, "photo.jpg", []byte("two"), now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(second) != "20261102-093015-photo-2.jpg" {
		t.Fatalf("second = %q", second)
	}
	if b, _ := os.ReadFile(first); string(b) != "one" {
		t.Fatalf("first overwritten: %q", b)
	}
	if b, _ := os.ReadFile(second); string(b) != "two" {
		t.Fatalf("second: %q", b)
	}
}

func TestSaveRefusesSymlinks(t *testing.T) {
	t.Run("project", func(t *testing.T) {
		base, target := t.TempDir(), t.TempDir()
		if err := os.Symlink(target, filepath.Join(base, "home-network")); err != nil {
			t.Fatal(err)
		}
		if _, err := Save(base, "home-network", 3, "photo.jpg", []byte("x"), now); err == nil {
			t.Fatal("saved through a symlinked project dir")
		}
		if entries, _ := os.ReadDir(target); len(entries) != 0 {
			t.Fatalf("target got %v", entries)
		}
	})
	t.Run("session", func(t *testing.T) {
		base, target := t.TempDir(), t.TempDir()
		if err := os.Mkdir(filepath.Join(base, "home-network"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, Dir(base, "home-network", 3)); err != nil {
			t.Fatal(err)
		}
		_, err := Save(base, "home-network", 3, "photo.jpg", []byte("x"), now)
		if err == nil || !strings.Contains(err.Error(), "is not a directory, refusing") {
			t.Fatalf("err = %v", err)
		}
		if entries, _ := os.ReadDir(target); len(entries) != 0 {
			t.Fatalf("target got %v", entries)
		}
	})
}

func TestSaveLimitsAndNames(t *testing.T) {
	base := t.TempDir()
	if _, err := Save(base, "p", 1, "big.bin", make([]byte, MaxFile+1), now); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "p")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("something written for a refused file: %v", err)
	}
	path, err := Save(base, "p", 1, "max.bin", make([]byte, MaxFile), now)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Size() != MaxFile {
		t.Fatalf("size %d", fi.Size())
	}
	for _, p := range []string{"", "..", "a/b", `a\b`, ".x"} {
		if _, err := Save(base, p, 1, "f", nil, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("project %q: %v", p, err)
		}
	}
	if _, err := Save(base, "p", 0, "f", nil, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("session 0: %v", err)
	}
	if path, err := Save(base, "p", 2, "empty.txt", nil, now); err != nil {
		t.Fatal(err)
	} else if b, _ := os.ReadFile(path); !bytes.Equal(b, nil) {
		t.Fatalf("empty file holds %q", b)
	}
}

func TestRemove(t *testing.T) {
	base := t.TempDir()
	for _, n := range []int{1, 2} {
		if _, err := Save(base, "home-network", n, "photo.jpg", []byte("x"), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(base, "home-network", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Dir(base, "home-network", 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session 1 kept: %v", err)
	}
	if _, err := os.Stat(Dir(base, "home-network", 2)); err != nil {
		t.Fatalf("session 2 removed: %v", err)
	}
	if err := Remove(base, "home-network", 9); err != nil {
		t.Fatalf("missing session: %v", err)
	}
	if err := Remove(filepath.Join(base, "nope"), "home-network", 1); err != nil {
		t.Fatalf("missing base: %v", err)
	}
	if err := Remove(base, "..", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid project: %v", err)
	}

	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(target, "3"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(base, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(base, "linked", 3); err == nil {
		t.Fatal("removed through a symlinked project dir")
	}
	if _, err := os.Stat(filepath.Join(target, "3")); err != nil {
		t.Fatalf("symlink target's 3/ gone: %v", err)
	}
}

func TestMakeDirIsTheSessionFolder(t *testing.T) {
	base := filepath.Join(t.TempDir(), "artifacts")
	dir, err := MakeDir(base, "home-network", 4)
	if err != nil || dir != Dir(base, "home-network", 4) {
		t.Fatalf("%q %v", dir, err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("%v %v", fi, err)
	}
	if _, err := MakeDir(base, "../x", 4); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dotted project: %v", err)
	}
}
