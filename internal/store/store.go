package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrNotExist = errors.New("storage not exist")

// Store implements the JSON file storage used by the original server:
// storage/<name...>.json.
//
// Writes are atomic (temp file + rename), concurrent access to the same file is
// serialized with a per-path RWMutex, and reads are served from a small
// mtime/size-validated cache so repeated reads of the same JSON document do not
// hit the disk.
type Store struct {
	Root string

	mu    sync.RWMutex
	locks map[string]*sync.RWMutex

	cacheMu sync.RWMutex
	cache   map[string]cachedFile
}

type cachedFile struct {
	data    []byte
	modTime time.Time
	size    int64
}

func New(root string) *Store {
	return &Store{
		Root:  root,
		locks: map[string]*sync.RWMutex{},
		cache: map[string]cachedFile{},
	}
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
	path := s.FilePath(names...)
	lock := s.pathLock(path)
	lock.RLock()
	defer lock.RUnlock()

	st, err := os.Stat(path)
	if err != nil {
		s.dropCache(path)
		if os.IsNotExist(err) {
			return nil, ErrNotExist
		}
		return nil, err
	}
	if st.IsDir() {
		return nil, ErrNotExist
	}
	if b, ok := s.cached(path, st); ok {
		return b, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		s.dropCache(path)
		if os.IsNotExist(err) {
			return nil, ErrNotExist
		}
		return nil, err
	}
	s.remember(path, st, b)
	return append([]byte(nil), b...), nil
}

func (s *Store) GetString(names ...string) (string, error) {
	b, err := s.Get(names...)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Store) Save(value any, names ...string) error {
	b, err := encode(value)
	if err != nil {
		return err
	}
	return s.writeFile(s.FilePath(names...), b)
}

func (s *Store) SavePretty(value any, names ...string) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return s.writeFile(s.FilePath(names...), b)
}

func (s *Store) Delete(names ...string) error {
	path := s.FilePath(names...)
	lock := s.pathLock(path)
	lock.Lock()
	defer lock.Unlock()
	err := os.RemoveAll(path)
	s.dropCachePrefix(path)
	return err
}

func (s *Store) Dir(names ...string) string {
	parts := append([]string{s.Root}, names...)
	return filepath.Join(parts...)
}

func (s *Store) EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func encode(value any) ([]byte, error) {
	switch v := value.(type) {
	case json.RawMessage:
		return v, nil
	case string:
		return []byte(v), nil
	case []byte:
		return v, nil
	default:
		return json.Marshal(value)
	}
}

// writeFile atomically replaces path with b: the data is written to a temp
// file in the same directory and then renamed over the target.
func (s *Store) writeFile(path string, b []byte) error {
	lock := s.pathLock(path)
	lock.Lock()
	defer lock.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	cleanup := func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(b); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		_ = os.Remove(tmpName)
		return err
	}
	closed = true
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if st, err := os.Stat(path); err == nil {
		s.remember(path, st, b)
	}
	return nil
}

func (s *Store) pathLock(path string) *sync.RWMutex {
	s.mu.RLock()
	lock := s.locks[path]
	s.mu.RUnlock()
	if lock != nil {
		return lock
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if lock = s.locks[path]; lock == nil {
		lock = &sync.RWMutex{}
		s.locks[path] = lock
	}
	return lock
}

func (s *Store) cached(path string, st os.FileInfo) ([]byte, bool) {
	s.cacheMu.RLock()
	c, ok := s.cache[path]
	s.cacheMu.RUnlock()
	if !ok || c.size != st.Size() || !c.modTime.Equal(st.ModTime()) {
		return nil, false
	}
	return append([]byte(nil), c.data...), true
}

func (s *Store) remember(path string, st os.FileInfo, b []byte) {
	s.cacheMu.Lock()
	s.cache[path] = cachedFile{
		data:    append([]byte(nil), b...),
		modTime: st.ModTime(),
		size:    st.Size(),
	}
	s.cacheMu.Unlock()
}

func (s *Store) dropCache(path string) {
	s.cacheMu.Lock()
	delete(s.cache, path)
	s.cacheMu.Unlock()
}

func (s *Store) dropCachePrefix(prefix string) {
	dirPrefix := prefix + string(os.PathSeparator)
	s.cacheMu.Lock()
	for path := range s.cache {
		if path == prefix || strings.HasPrefix(path, dirPrefix) {
			delete(s.cache, path)
		}
	}
	s.cacheMu.Unlock()
}
