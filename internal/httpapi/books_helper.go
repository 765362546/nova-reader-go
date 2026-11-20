package httpapi

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/765362546/novel-reader-go/internal/cache"
	"github.com/765362546/novel-reader-go/internal/localbook"
	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/util"
	"github.com/765362546/novel-reader-go/internal/webbook"
)

func dstr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) absPath(rel string) string {
	if rel == "" {
		return ""
	}
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(s.App.WorkDir, rel)
}

func bookDirName(b *model.Book) string {
	return util.BookDirName(b.Name, b.Author)
}

// ---------------------------------------------------------------- bookshelf

func (s *Server) loadBooks(ns string) []*model.Book {
	raw, err := s.Store.Get("data", ns, "bookshelf")
	if err != nil {
		return nil
	}
	var books []*model.Book
	if json.Unmarshal(raw, &books) != nil {
		return nil
	}
	return books
}

func (s *Server) saveBooks(ns string, books []*model.Book) error {
	s.shelfMu.Lock()
	defer s.shelfMu.Unlock()
	if books == nil {
		books = []*model.Book{}
	}
	return s.Store.Save(books, "data", ns, "bookshelf")
}

func (s *Server) shelfBookByURL(ns, url string) *model.Book {
	if url == "" {
		return nil
	}
	for _, b := range s.loadBooks(ns) {
		if b.BookUrl == url {
			return b
		}
	}
	return nil
}

// editShelfBook finds a book by url or name+author and saves the edited value.
func (s *Server) editShelfBook(ns string, book *model.Book, fn func(*model.Book)) {
	if book == nil {
		return
	}
	s.shelfMu.Lock()
	defer s.shelfMu.Unlock()
	books := s.loadBooks(ns)
	idx := -1
	for i, b := range books {
		if book.BookUrl != "" && b.BookUrl == book.BookUrl {
			idx = i
			break
		}
		if book.Name != "" && b.Name == book.Name && book.Author != "" && b.Author == book.Author {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	fn(books[idx])
	_ = s.Store.Save(books, "data", ns, "bookshelf")
}

func (s *Server) saveShelfBookProgress(ns string, book *model.Book, chapter *model.BookChapter) {
	s.editShelfBook(ns, book, func(exist *model.Book) {
		exist.DurChapterIndex = chapter.Index
		exist.DurChapterTitle = chapter.Title
		exist.DurChapterTime = time.Now().UnixMilli()
	})
}

func (s *Server) saveShelfBookLatestChapter(ns string, book *model.Book, chapters []*model.BookChapter) {
	s.editShelfBook(ns, book, func(exist *model.Book) {
		if len(chapters) > 0 {
			exist.LatestChapterTitle = chapters[len(chapters)-1].Title
		}
		if len(chapters)-exist.TotalChapterNum > 0 {
			exist.LastCheckCount = len(chapters) - exist.TotalChapterNum
			exist.LastCheckTime = time.Now().UnixMilli()
		}
		exist.TotalChapterNum = len(chapters)
	})
}

// ---------------------------------------------------------------- book sources

// bookSourceIndex caches the parsed position of every book source url so
// lookups by url do not re-scan and re-unmarshal the whole source list.
type bookSourceIndex struct {
	raws  []json.RawMessage
	byURL map[string]int
}

func (s *Server) sourceIndex(ns string) *bookSourceIndex {
	s.sourceMu.RLock()
	idx := s.sourceIndexes[ns]
	s.sourceMu.RUnlock()
	if idx != nil {
		return idx
	}
	raws := s.loadBookSources(ns)
	idx = &bookSourceIndex{raws: raws, byURL: make(map[string]int, len(raws))}
	for i, raw := range raws {
		var bs model.BookSource
		if unmarshalRaw(raw, &bs) && bs.BookSourceUrl != "" {
			idx.byURL[bs.BookSourceUrl] = i
		}
	}
	s.sourceMu.Lock()
	s.sourceIndexes[ns] = idx
	s.sourceMu.Unlock()
	return idx
}

func (s *Server) invalidateSourceIndex(ns string) {
	s.sourceMu.Lock()
	delete(s.sourceIndexes, ns)
	s.sourceMu.Unlock()
}

func (s *Server) sourceStrings(ns, group string) []string {
	idx := s.sourceIndex(ns)
	out := make([]string, 0, len(idx.raws))
	for _, raw := range idx.raws {
		if group != "" {
			var bs model.BookSource
			if !unmarshalRaw(raw, &bs) || dstr(bs.BookSourceGroup) != group {
				continue
			}
		}
		out = append(out, string(raw))
	}
	return out
}

func (s *Server) sourceByURL(ns, url string) (string, int) {
	if url == "" {
		return "", -1
	}
	idx := s.sourceIndex(ns)
	i, ok := idx.byURL[url]
	if !ok {
		return "", -1
	}
	return string(idx.raws[i]), i
}

func (s *Server) bookSourceByURL(ns, url string) *model.BookSource {
	raw, _ := s.sourceByURL(ns, url)
	if raw == "" {
		return nil
	}
	var bs model.BookSource
	if json.Unmarshal([]byte(raw), &bs) != nil {
		return nil
	}
	return &bs
}

// sourceStringFromRequest mirrors BookController.getBookSourceString.
func (s *Server) sourceStringFromRequest(r map[string]any, ns, sourceURL string) string {
	if bs, ok := r["bookSource"].(map[string]any); ok {
		if raw, err := json.Marshal(bs); err == nil {
			return string(raw)
		}
	}
	if src, _ := s.sourceByURL(ns, str(r, "bookSourceUrl")); src != "" {
		return src
	}
	if sourceURL != "" {
		if src, _ := s.sourceByURL(ns, sourceURL); src != "" {
			return src
		}
	}
	// fall back to the first configured book source
	if list := s.sourceStrings(ns, ""); len(list) > 0 {
		return list[0]
	}
	return ""
}

// ---------------------------------------------------------------- per-book data

func (s *Server) bookSourcesForBook(ns string, book *model.Book) []*model.SearchBook {
	raw, err := s.Store.Get("data", ns, bookDirName(book), "bookSource")
	if err != nil {
		return nil
	}
	var list []*model.SearchBook
	_ = json.Unmarshal(raw, &list)
	return list
}

func (s *Server) saveBookSourcesForBook(ns string, book *model.Book, list []*model.SearchBook, replace bool) {
	if book == nil || book.Name == "" {
		return
	}
	var existing []*model.SearchBook
	if !replace {
		existing = s.bookSourcesForBook(ns, book)
	}
	for _, sb := range list {
		idx := -1
		for i, old := range existing {
			if old.BookUrl == sb.BookUrl {
				idx = i
				break
			}
		}
		if idx >= 0 {
			existing[idx] = sb
		} else {
			existing = append(existing, sb)
		}
	}
	if existing == nil {
		existing = []*model.SearchBook{}
	}
	_ = s.Store.SavePretty(existing, "data", ns, bookDirName(book), "bookSource")
}

func (s *Server) chapterCacheDir(ns string, book *model.Book) string {
	dir := s.Store.Dir("data", ns, bookDirName(book), util.MD5(book.BookUrl))
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// loadChapterCache reads the per-book chapter cache file (cache.json). The
// file maps chapter index to raw content and is written atomically by store.
func (s *Server) loadChapterCache(ns string, book *model.Book) map[int]string {
	out := map[int]string{}
	raw, err := s.Store.Get("data", ns, bookDirName(book), util.MD5(book.BookUrl), "cache")
	if err != nil {
		return out
	}
	var data map[string]string
	if json.Unmarshal(raw, &data) != nil {
		return out
	}
	for k, v := range data {
		if idx, err := parseInt(k); err == nil {
			out[idx] = v
		}
	}
	return out
}

func (s *Server) saveChapterCache(ns string, book *model.Book, data map[int]string) error {
	raw := make(map[string]string, len(data))
	for k, v := range data {
		raw[itoa(k)] = v
	}
	return s.Store.Save(raw, "data", ns, bookDirName(book), util.MD5(book.BookUrl), "cache")
}

func (s *Server) cachedChapterSet(ns string, book *model.Book) map[int]bool {
	out := map[int]bool{}
	for idx := range s.loadChapterCache(ns, book) {
		out[idx] = true
	}
	// Legacy caches stored one .txt file per chapter; count them too so an
	// existing cache is not re-downloaded after the format change.
	dir := s.chapterCacheDir(ns, book)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		if idx, err := parseInt(strings.TrimSuffix(e.Name(), ".txt")); err == nil {
			out[idx] = true
		}
	}
	return out
}

func (s *Server) chapterListKey(ns string, book *model.Book) string {
	return ns + "\x00" + book.BookUrl
}

func (s *Server) invalidateChapterList(ns string, book *model.Book) {
	if book != nil {
		s.chapterListCache.Delete(s.chapterListKey(ns, book))
	}
}

func (s *Server) chapterList(ns string, book *model.Book, sourceStr string, refresh bool) ([]*model.BookChapter, error) {
	if book == nil {
		return nil, errors.New("书籍信息错误")
	}
	key := s.chapterListKey(ns, book)
	if !refresh {
		if cached, ok := s.chapterListCache.Get(key); ok {
			return cached, nil
		}
	}
	md5 := util.MD5(book.BookUrl)
	var cached []*model.BookChapter
	if !refresh {
		if raw, err := s.Store.Get("data", ns, bookDirName(book), md5); err == nil {
			if json.Unmarshal(raw, &cached) == nil && len(cached) > 0 {
				s.chapterListCache.Put(key, cached)
				return cached, nil
			}
		}
	}
	var list []*model.BookChapter
	var err error
	if book.Origin == model.BookTypeLocal {
		if localbook.IsEpub(book) && !s.extractEpub(book, refresh) {
			return nil, errors.New("Epub书籍解压失败")
		}
		if localbook.IsCbz(book) && !s.extractCbz(book, refresh) {
			return nil, errors.New("CBZ书籍解压失败")
		}
		list, err = localbook.GetChapterList(book, s.App.WorkDir, book.TocUrl)
	} else {
		if sourceStr == "" {
			return nil, errors.New("未配置书源")
		}
		var bs model.BookSource
		if e := json.Unmarshal([]byte(sourceStr), &bs); e != nil {
			return nil, errors.New("书源信息错误")
		}
		list, err = webbook.New(&bs).ChapterList(book)
	}
	if err != nil {
		if sourceStr != "" {
			if bs := s.bookSourceByURL(ns, sourceStr); bs != nil {
				_ = bs
			}
		}
		return nil, err
	}
	if list == nil {
		list = []*model.BookChapter{}
	}
	// Only cache non-empty results so a transient empty response does not
	// pin the book to an empty chapter list.
	if len(list) > 0 {
		s.chapterListCache.Put(key, list)
	}
	_ = s.Store.SavePretty(list, "data", ns, bookDirName(book), md5)
	s.saveShelfBookLatestChapter(ns, book, list)
	return list, nil
}

func parseInt(v string) (int, error) {
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ---------------------------------------------------------------- archives

func copyPath(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if st.IsDir() {
		return copyDir(src, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		sp := filepath.Join(src, e.Name())
		dp := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(sp, dp); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(sp, dp); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func (s *Server) localArchivePath(book *model.Book) string {
	p := s.absPath(book.OriginName)
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		if localbook.IsEpub(book) {
			return filepath.Join(p, "index.epub")
		}
		return filepath.Join(p, "index.cbz")
	}
	return p
}

func (s *Server) extractEpub(book *model.Book, force bool) bool {
	dst := filepath.Join(s.absPath(book.BookUrl), "index")
	if !force {
		if st, err := os.Stat(dst); err == nil && st.IsDir() {
			return true
		}
	}
	_ = os.RemoveAll(dst)
	return unzipTo(s.localArchivePath(book), dst)
}

func (s *Server) extractCbz(book *model.Book, force bool) bool {
	dst := filepath.Join(s.absPath(book.BookUrl), "index")
	if !force {
		if st, err := os.Stat(dst); err == nil && st.IsDir() {
			return true
		}
	}
	_ = os.RemoveAll(dst)
	return unzipTo(s.localArchivePath(book), dst)
}

func unzipTo(src, dst string) bool {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return false
	}
	defer zr.Close()
	_ = os.MkdirAll(dst, 0o755)
	for _, f := range zr.File {
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") {
			continue
		}
		target := filepath.Join(dst, name)
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0o755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		rc, err := f.Open()
		if err != nil {
			continue
		}
		func() {
			defer rc.Close()
			out, err := os.Create(target)
			if err != nil {
				return
			}
			defer out.Close()
			_, _ = io.Copy(out, rc)
		}()
	}
	return true
}

func (s *Server) localFileExists(book *model.Book) bool {
	if book == nil {
		return false
	}
	p := s.absPath(book.OriginName)
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	if st.IsDir() {
		entries, _ := os.ReadDir(p)
		return len(entries) > 0
	}
	return true
}

// ---------------------------------------------------------------- caches

func (s *Server) mergeBookCacheInfo(book *model.Book) {
	if book == nil {
		return
	}
	cached, _ := s.bookInfoCache.Get(book.BookUrl)
	if cached == nil {
		return
	}
	if book.Name == "" {
		book.Name = cached.Name
	}
	if book.Author == "" {
		book.Author = cached.Author
	}
	if book.CoverUrl == "" {
		book.CoverUrl = cached.CoverUrl
	}
	if book.TocUrl == "" {
		book.TocUrl = cached.TocUrl
	}
	if book.Intro == "" {
		book.Intro = cached.Intro
	}
	if book.LatestChapterTitle == "" {
		book.LatestChapterTitle = cached.LatestChapterTitle
	}
	if book.WordCount == "" {
		book.WordCount = cached.WordCount
	}
}

func (s *Server) saveBookInfoCache(books ...*model.Book) {
	for _, b := range books {
		if b != nil && b.BookUrl != "" {
			s.bookInfoCache.Put(b.BookUrl, b)
		}
	}
}

func (s *Server) cachedBookInfo(url string) *model.Book {
	b, _ := s.bookInfoCache.Get(url)
	return b
}

func (s *Server) invalidList(ns string) []map[string]any {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	m := s.invalidCache[ns]
	out := make([]map[string]any, 0, len(m))
	for _, v := range m {
		out = append(out, map[string]any{"sourceUrl": v.SourceUrl, "time": v.Time, "error": v.Error})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["time"].(int64) > out[j]["time"].(int64)
	})
	return out
}

func (s *Server) addInvalidSource(ns, url string, err error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.invalidCache[ns] == nil {
		s.invalidCache[ns] = map[string]invalidBookSource{}
	}
	s.invalidCache[ns][url] = invalidBookSource{
		SourceUrl: url,
		Time:      time.Now().UnixMilli(),
		Error:     err.Error(),
	}
}

// ---------------------------------------------------------------- search

const (
	sourceFailureThreshold = 3
	sourceCooldown         = 10 * time.Minute
)

type sourceBreaker struct {
	failures int
	until    time.Time
}

// sourceBlocked reports whether a source is in its cooldown window after
// repeated failures, so multi-source searches can skip it quickly.
func (s *Server) sourceBlocked(ns, url string) bool {
	if url == "" {
		return false
	}
	key := ns + "\x00" + url
	s.breakerMu.Lock()
	defer s.breakerMu.Unlock()
	b := s.breakers[key]
	if b == nil || b.until.IsZero() {
		return false
	}
	if time.Now().Before(b.until) {
		return true
	}
	delete(s.breakers, key)
	return false
}

func (s *Server) sourceFailed(ns, url string) {
	if url == "" {
		return
	}
	key := ns + "\x00" + url
	s.breakerMu.Lock()
	defer s.breakerMu.Unlock()
	b := s.breakers[key]
	if b == nil {
		b = &sourceBreaker{}
		s.breakers[key] = b
	}
	b.failures++
	if b.failures >= sourceFailureThreshold {
		b.until = time.Now().Add(sourceCooldown)
	}
}

func (s *Server) sourceSucceeded(ns, url string) {
	if url == "" {
		return
	}
	key := ns + "\x00" + url
	s.breakerMu.Lock()
	delete(s.breakers, key)
	s.breakerMu.Unlock()
}

func (s *Server) searchBookWithSource(ns, sourceStr string, book *model.Book, accurate bool) []*model.SearchBook {
	var bs model.BookSource
	if json.Unmarshal([]byte(sourceStr), &bs) != nil {
		return nil
	}
	if s.sourceBlocked(ns, bs.BookSourceUrl) {
		return nil
	}
	start := time.Now()
	result, err := webbook.New(&bs).Search(book.Name, 1)
	if err != nil {
		s.sourceFailed(ns, bs.BookSourceUrl)
		s.addInvalidSource(ns, bs.BookSourceUrl, err)
		return nil
	}
	s.sourceSucceeded(ns, bs.BookSourceUrl)
	cost := time.Since(start).Milliseconds()
	out := make([]*model.SearchBook, 0, len(result))
	for _, sb := range result {
		if accurate {
			ok := sb.Name == book.Name && sb.Author == book.Author
			if !ok {
				continue
			}
		} else {
			ok := strings.Contains(strings.ToLower(sb.Name), strings.ToLower(book.Name)) ||
				strings.Contains(strings.ToLower(sb.Author), strings.ToLower(book.Name))
			if !ok {
				continue
			}
		}
		sb.Time = cost
		out = append(out, sb)
	}
	return out
}

func searchBookKey(b *model.SearchBook) string { return b.Name + "\x00" + b.Author }

func (s *Server) bookFromSearchBook(sb *model.SearchBook) *model.Book {
	b := model.NewBook()
	b.BookUrl = sb.BookUrl
	b.TocUrl = sb.TocUrl
	b.Origin = sb.Origin
	b.OriginName = sb.OriginName
	b.Type = sb.Type
	b.Name = sb.Name
	b.Author = sb.Author
	b.Kind = sb.Kind
	b.CoverUrl = sb.CoverUrl
	b.Intro = sb.Intro
	b.WordCount = sb.WordCount
	b.LatestChapterTitle = sb.LatestChapterTitle
	b.Variable = sb.Variable
	return b
}

func searchBookFromBook(b *model.Book) *model.SearchBook {
	sb := model.NewSearchBook()
	sb.BookUrl = b.BookUrl
	sb.TocUrl = b.TocUrl
	sb.Origin = b.Origin
	sb.OriginName = b.OriginName
	sb.Type = b.Type
	sb.Name = b.Name
	sb.Author = b.Author
	sb.Kind = b.Kind
	sb.CoverUrl = b.CoverUrl
	sb.Intro = b.Intro
	sb.WordCount = b.WordCount
	sb.LatestChapterTitle = b.LatestChapterTitle
	sb.Variable = b.Variable
	sb.Time = time.Now().UnixMilli()
	return sb
}

// ---------------------------------------------------------------- content

func contentCacheKey(ns string, book *model.Book, index int) string {
	return ns + "\x00" + util.MD5(book.BookUrl) + "\x00" + itoa(index)
}

// chapterCacheContent returns a chapter from the in-memory LRU, the per-book
// cache file, or a legacy per-chapter .txt file.
func (s *Server) chapterCacheContent(ns string, book *model.Book, index int) (string, bool) {
	key := contentCacheKey(ns, book, index)
	if v, ok := s.contentCache.Get(key); ok {
		return v, true
	}
	if v, ok := s.loadChapterCache(ns, book)[index]; ok {
		s.contentCache.Put(key, v)
		return v, true
	}
	if raw, err := os.ReadFile(filepath.Join(s.chapterCacheDir(ns, book), fmt.Sprintf("%d.txt", index))); err == nil {
		v := string(raw)
		s.contentCache.Put(key, v)
		return v, true
	}
	return "", false
}

// putChapterCacheContent stores one chapter, rewriting the per-book file.
func (s *Server) putChapterCacheContent(ns string, book *model.Book, index int, content string) error {
	s.contentCache.Put(contentCacheKey(ns, book, index), content)
	s.chapterCacheMu.Lock()
	defer s.chapterCacheMu.Unlock()
	data := s.loadChapterCache(ns, book)
	data[index] = content
	return s.saveChapterCache(ns, book, data)
}

// mergeChapterCache stores a batch of chapters with a single file rewrite,
// used by the bulk caching SSE job.
func (s *Server) mergeChapterCache(ns string, book *model.Book, items map[int]string) error {
	if len(items) == 0 {
		return nil
	}
	for idx, content := range items {
		s.contentCache.Put(contentCacheKey(ns, book, idx), content)
	}
	s.chapterCacheMu.Lock()
	defer s.chapterCacheMu.Unlock()
	data := s.loadChapterCache(ns, book)
	for idx, content := range items {
		data[idx] = content
	}
	return s.saveChapterCache(ns, book, data)
}

func (s *Server) invalidateContentCache(ns string, book *model.Book) {
	if book == nil {
		return
	}
	prefix := ns + "\x00" + util.MD5(book.BookUrl) + "\x00"
	s.contentCache.DeleteWhere(func(key string) bool {
		return strings.HasPrefix(key, prefix)
	})
}

func (s *Server) chapterContent(ns string, book *model.Book, chapter *model.BookChapter) (string, error) {
	if content, ok := s.chapterCacheContent(ns, book, chapter.Index); ok {
		return content, nil
	}
	if book.Origin == model.BookTypeLocal {
		if localbook.IsEpub(book) {
			if !s.extractEpub(book, false) {
				return "", errors.New("Epub书籍解压失败")
			}
		}
		if localbook.IsCbz(book) {
			if !s.extractCbz(book, false) {
				return "", errors.New("CBZ书籍解压失败")
			}
		}
		content, err := localbook.GetContent(book, chapter, s.App.WorkDir)
		if err == nil && localbook.IsEpub(book) {
			_ = s.putChapterCacheContent(ns, book, chapter.Index, content)
		}
		return content, err
	}
	return "", nil
}

var replaceRegexCache = cache.New[string, *regexp.Regexp](2048)

func cachedRegexp(pattern string) (*regexp.Regexp, error) {
	if re, ok := replaceRegexCache.Get(pattern); ok {
		return re, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	replaceRegexCache.Put(pattern, re)
	return re, nil
}

// replaceRulesFor returns the namespace's replace rules sorted by order. The
// parsed slice is cached and invalidated whenever rules are saved or deleted.
func (s *Server) replaceRulesFor(ns string) []*model.ReplaceRule {
	if rules, ok := s.replaceRuleCache.Get(ns); ok {
		return rules
	}
	raw, err := s.Store.Get("data", ns, "replaceRule")
	if err != nil {
		s.replaceRuleCache.Put(ns, nil)
		return nil
	}
	var rules []*model.ReplaceRule
	if json.Unmarshal(raw, &rules) != nil {
		return nil
	}
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Order < rules[j].Order })
	s.replaceRuleCache.Put(ns, rules)
	return rules
}

func (s *Server) invalidateReplaceRules(ns string) {
	s.replaceRuleCache.Delete(ns)
}

func (s *Server) applyReplaceRules(ns, content string) string {
	rules := s.replaceRulesFor(ns)
	for _, r := range rules {
		if r == nil || !r.IsEnabled || r.Pattern == "" {
			continue
		}
		if r.IsRegex {
			re, err := cachedRegexp(r.Pattern)
			if err != nil {
				continue
			}
			content = re.ReplaceAllString(content, r.Replacement)
		} else {
			content = strings.ReplaceAll(content, r.Pattern, r.Replacement)
		}
	}
	return content
}

// ---------------------------------------------------------------- misc

func (s *Server) localStoreHome() string {
	return filepath.Join(s.Store.Root, "localStore")
}

func (s *Server) webdavHome(ns string) string {
	dir := filepath.Join(s.Store.Root, "data", ns, "webdav")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func fileInfoMap(path, home string) map[string]any {
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return map[string]any{
		"name":         st.Name(),
		"size":         st.Size(),
		"path":         strings.ReplaceAll(path, home, ""),
		"lastModified": st.ModTime().UnixMilli(),
		"isDirectory":  st.IsDir(),
	}
}
