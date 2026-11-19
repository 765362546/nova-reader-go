package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/765362546/novel-reader-go/internal/model"
)

func (s *Server) loadArray(ns, name string) []json.RawMessage {
	b, err := s.Store.Get("data", ns, name)
	if err != nil {
		return nil
	}
	var arr []json.RawMessage
	if json.Unmarshal(b, &arr) != nil {
		return nil
	}
	return arr
}

func (s *Server) saveArray(ns, name string, arr []json.RawMessage) error {
	if arr == nil {
		arr = []json.RawMessage{}
	}
	return s.Store.Save(arr, "data", ns, name)
}

func (s *Server) loadBookSources(ns string) []json.RawMessage {
	arr := s.loadArray(ns, "bookSource")
	if arr == nil && ns != "default" {
		if def := s.loadArray("default", "bookSource"); def != nil {
			_ = s.saveArray(ns, "bookSource", def)
			return def
		}
	}
	return arr
}

func unmarshalRaw(raw json.RawMessage, v any) bool {
	return json.Unmarshal(raw, v) == nil
}

func containsSource(list []json.RawMessage, url string) int {
	for i, raw := range list {
		var src model.BookSource
		if json.Unmarshal(raw, &src) == nil && src.BookSourceUrl == url {
			return i
		}
	}
	return -1
}

func containsRss(list []json.RawMessage, url string) int {
	for i, raw := range list {
		var src model.RssSource
		if json.Unmarshal(raw, &src) == nil && src.SourceUrl == url {
			return i
		}
	}
	return -1
}

func removeAt(list []json.RawMessage, i int) []json.RawMessage {
	if i < 0 || i >= len(list) {
		return list
	}
	return append(list[:i], list[i+1:]...)
}

func replaceAt(list []json.RawMessage, i int, raw json.RawMessage) []json.RawMessage {
	if i < 0 || i >= len(list) {
		return list
	}
	out := make([]json.RawMessage, len(list))
	copy(out, list)
	out[i] = raw
	return out
}

func (s *Server) userBookSourcePath(ns string) string {
	return s.Store.FilePath("data", ns, "bookSource")
}

func (s *Server) deleteUserBookSourceFile(ns string) {
	_ = os.Remove(s.userBookSourcePath(ns))
	_ = os.RemoveAll(filepath.Join(s.Store.Root, "data", ns, "bookSource.json"))
}

func isSvgOrImage(b []byte) bool {
	return len(b) > 0
}
