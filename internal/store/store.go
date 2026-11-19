package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

var ErrNotExist = errors.New("storage not exist")

// Store implements the JSON file storage used by the original server:
// storage/<name...>.json.
type Store struct {
	Root string
}

func New(root string) *Store {
	return &Store{Root: root}
}

func (s *Store) FilePath(names ...string) string {
	parts := make([]string, 0, len(names)+1)
	parts = append(parts, s.Root)
	if len(names) > 1 {
		parts = append(parts, names[:len(names)-1]...)
	}
	if len(names) > 0 {
		parts = append(parts, names[len(names)-1]+".json")
	}
	return filepath.Join(parts...)
}

func (s *Store) Get(names ...string) ([]byte, error) {
	b, err := os.ReadFile(s.FilePath(names...))
	if os.IsNotExist(err) {
		return nil, ErrNotExist
	}
	return b, err
}

func (s *Store) GetString(names ...string) (string, error) {
	b, err := s.Get(names...)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Store) Save(value any, names ...string) error {
	var b []byte
	var err error
	if raw, ok := value.(json.RawMessage); ok {
		b = raw
	} else if str, ok := value.(string); ok {
		b = []byte(str)
	} else if bytes, ok := value.([]byte); ok {
		b = bytes
	} else {
		b, err = json.Marshal(value)
		if err != nil {
			return err
		}
	}
	path := s.FilePath(names...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func (s *Store) SavePretty(value any, names ...string) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	path := s.FilePath(names...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func (s *Store) Delete(names ...string) error {
	err := os.RemoveAll(s.FilePath(names...))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) Dir(names ...string) string {
	parts := append([]string{s.Root}, names...)
	return filepath.Join(parts...)
}

func (s *Store) EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}
