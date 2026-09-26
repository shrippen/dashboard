package files_test

import (
	"errors"
	"testing"

	"andon/internal/repos/files"
)

// Names never leave their key's directory; a round trip keeps the bytes.
func TestStoreStaysInside(t *testing.T) {
	s := files.New(t.TempDir())

	for _, bad := range [][2]string{{"..", "x"}, {"7", "../x"}, {"7", ".hidden"}, {"", "x"}} {
		if err := s.Put(bad[0], bad[1], []byte("x")); !errors.Is(err, files.ErrBadName) {
			t.Errorf("%q/%q: %v", bad[0], bad[1], err)
		}
	}

	if err := s.Put("7", "Inter-600.woff2", []byte("font")); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get("7", "Inter-600.woff2"); err != nil || string(got) != "font" {
		t.Fatalf("get: %q %v", got, err)
	}
	if names, _ := s.Names("7"); len(names) != 1 || !s.Exists("7", "Inter-600.woff2") {
		t.Fatalf("names: %v", names)
	}
	if err := s.RemoveAll("7"); err != nil || s.Exists("7", "Inter-600.woff2") {
		t.Fatalf("remove all: %v", err)
	}
	if names, err := s.Names("7"); err != nil || len(names) != 0 {
		t.Fatalf("names after remove: %v %v", names, err)
	}
}
