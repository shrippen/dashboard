// Package files stores small binary assets on disk below one root
// (DATA_DIR): theme fonts, cached icons.
//
//	<root>/<area>/<key>/<name>     e.g. /data/themes/7/Inter-600.woff2
package files

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	dirMode  = 0o750
	fileMode = 0o640
)

// ErrBadName means a name tried to escape its directory.
var ErrBadName = errors.New("files: bad name")

// Store is one area on disk (e.g. the themes directory).
type Store struct{ root string }

// New returns a store rooted at dir.
func New(dir string) Store { return Store{root: dir} }

func (s Store) path(key, name string) (string, error) {
	for _, part := range []string{key, name} {
		if part == "" || part != filepath.Base(part) || strings.HasPrefix(part, ".") {
			return "", ErrBadName
		}
	}
	return filepath.Join(s.root, key, name), nil
}

// Put writes name under key, creating directories as needed.
func (s Store) Put(key, name string, data []byte) error {
	p, err := s.path(key, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		return err
	}
	return os.WriteFile(p, data, fileMode)
}

// Get reads name under key.
func (s Store) Get(key, name string) ([]byte, error) {
	p, err := s.path(key, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// Remove deletes name under key (missing is fine).
func (s Store) Remove(key, name string) error {
	p, err := s.path(key, name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RemoveAll deletes everything under key.
func (s Store) RemoveAll(key string) error {
	p, err := s.path(key, "x")
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(p))
}
