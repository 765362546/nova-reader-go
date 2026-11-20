package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/765362546/novel-reader-go/internal/localbook"
	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/util"
	"github.com/765362546/novel-reader-go/internal/webbook"
)

func sseWrite(w http.ResponseWriter, f http.Flusher, event string, data any) {
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", b)
	if f != nil {
		f.Flush()
	}
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func queryIntOrBody(r *http.Request, m map[string]any, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return intVal(m, key, def)
}

func removeAtIdx[T any](list []T, i int) []T {
	if i < 0 || i >= len(list) {
		return list
	}
	out := make([]T, 0, len(list)-1)
	out = append(out, list[:i]...)
	out = append(out, list[i+1:]...)
	return out
}

func bookFromAny(v any) *model.Book {
	b, _ := json.Marshal(v)
	var book model.Book
	if json.Unmarshal(b, &book) != nil {
		return nil
	}
	return &book
}

func (s *Server) getBookshelf(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	refresh := 0
	if r.Method == http.MethodPost {
		refresh = intVal(bodyMap(r), "refresh", 0)
	} else {
		refresh = queryInt(r, "refresh", 0)
	}
	books := s.loadBooks(ri.Namespace)
	if refresh > 0 {
		sem := make(chan struct{}, 16)
		var wg sync.WaitGroup
		sources := s.sourceStrings(ri.Namespace, "")
		for _, book := range books {
			if book.Origin == model.BookTypeLocal || !book.CanUpdate {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(b *model.Book) {
				defer wg.Done()
				defer func() { <-sem }()
				for _, src := range sources {
					bs := s.bookSourceByURL(ri.Namespace, src)
					if bs != nil && bs.BookSourceUrl == b.Origin {
						if list, err := s.chapterList(ri.Namespace, b, src, true); err == nil && len(list) > 0 {
							b.LatestChapterTitle = list[len(list)-1].Title
						}
						break
					}
				}
			}(book)
		}
		wg.Wait()
	}
	if books == nil {
		books = []*model.Book{}
	}
	return success(books)
}

func (s *Server) getShelfBook(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	url := queryOrBody(r, m, "url")
	if url == "" {
		return failure("书源链接不能为空")
	}
	book := s.shelfBookByURL(ri.Namespace, url)
	if book == nil {
		return failure("书籍不存在")
	}
	return success(book)
}

func (s *Server) saveBook(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	var book *model.Book
	if inner, ok := m["book"].(map[string]any); ok {
		book = bookFromAny(inner)
	} else {
		book = bookFromAny(m)
	}
	if book == nil {
		return failure("参数错误")
	}
	if book.Origin == "" {
		return failure("未找到书源信息")
	}
	if book.BookUrl == "" {
		return failure("书籍链接不能为空")
	}
	ns := ri.Namespace
	books := s.loadBooks(ns)
	existIndex := -1
	for i, b := range books {
		if b.Name == book.Name && b.Author == book.Author {
			existIndex = i
			break
		}
	}
	if existIndex < 0 && len(books) >= s.App.UserBookLimit {
		return failure("超过用户书籍数上限")
	}
	dir := util.BookDirName(book.Name, book.Author)
	if book.Origin == model.BookTypeLocal {
		if strings.HasPrefix(book.BookUrl, "/assets/") {
			asset := filepath.Join(s.Store.Root, "assets", strings.TrimPrefix(book.BookUrl, "/assets/"))
			if st, err := os.Stat(asset); err != nil || st == nil {
				return failure("上传书籍不存在")
			}
			rel := filepath.Join("storage", "data", ns, dir, filepath.Base(asset))
			if err := copyPath(asset, s.absPath(rel)); err != nil {
				return failure("导入本地书籍失败")
			}
			_ = os.RemoveAll(asset)
			book.BookUrl = rel
			book.OriginName = rel
		} else if strings.Contains(book.BookUrl, "storage/localStore") || strings.Contains(book.BookUrl, "webdav/") {
			if !s.localFileExists(book) {
				if strings.Contains(book.BookUrl, "storage/localStore") {
					return failure("本地书仓书籍不存在")
				}
				return failure("webdav书仓书籍不存在")
			}
			rel := filepath.Join("storage", "data", ns, dir, filepath.Base(s.absPath(book.BookUrl)))
			book.BookUrl = rel
		}
		if localbook.IsEpub(book) && !s.extractEpub(book, false) {
			return failure("导入本地Epub书籍失败")
		}
		if localbook.IsCbz(book) && !s.extractCbz(book, false) {
			return failure("导入本地CBZ书籍失败")
		}
	} else if book.TocUrl == "" {
		srcStr := s.sourceStringFromRequest(m, ns, book.Origin)
		if srcStr == "" {
			return failure("书源信息错误")
		}
		var bs model.BookSource
		if json.Unmarshal([]byte(srcStr), &bs) != nil {
			return failure("书源信息错误")
		}
		info, err := webbook.New(&bs).BookInfo(book, false)
		if err != nil {
			logger.Warn("save book: fetch book info failed",
				"namespace", ns, "book", book.Name, "bookUrl", book.BookUrl, "source", bs.BookSourceName, "error", err)
			return failure("获取书籍信息失败: " + err.Error())
		}
		if info == nil {
			return failure("获取书籍信息失败")
		}
		if book.Name == "" {
			book.Name = info.Name
		}
		if book.Author == "" {
			book.Author = info.Author
		}
		if book.CoverUrl == "" {
			book.CoverUrl = info.CoverUrl
		}
		if book.TocUrl == "" {
			book.TocUrl = info.TocUrl
		}
		if book.Intro == "" {
			book.Intro = info.Intro
		}
		if book.LatestChapterTitle == "" {
			book.LatestChapterTitle = info.LatestChapterTitle
		}
		if book.WordCount == "" {
			book.WordCount = info.WordCount
		}
	}
	s.mergeBookCacheInfo(book)
	if existIndex >= 0 {
		old := books[existIndex]
		book.DurChapterIndex = old.DurChapterIndex
		book.DurChapterTitle = old.DurChapterTitle
		book.DurChapterTime = old.DurChapterTime
		books[existIndex] = book
	} else {
		books = append(books, book)
	}
	_ = s.saveBooks(ns, books)
	s.saveBookSourcesForBook(ns, book, []*model.SearchBook{searchBookFromBook(book)}, false)
	action := "added"
	if existIndex >= 0 {
		action = "updated"
	}
	logger.Info("shelf book saved",
		"namespace", ns,
		"book", book.Name,
		"author", book.Author,
		"action", action,
		"origin", book.Origin,
	)
	return success(book)
}

func (s *Server) deleteBook(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	book := bookFromAny(m)
	if book == nil {
		return failure("参数错误")
	}
	ns := ri.Namespace
	books := s.loadBooks(ns)
	idx := -1
	for i, b := range books {
		if b.BookUrl != "" && b.BookUrl == book.BookUrl {
			idx = i
			book = b
			break
		}
		if b.Name == book.Name && b.Author == book.Author {
			idx = i
			book = b
			break
		}
	}
	if idx < 0 {
		return failure("书架书籍不存在")
	}
	books = removeAtIdx(books, idx)
	_ = s.saveBooks(ns, books)
	s.invalidateChapterList(ns, book)
	s.invalidateContentCache(ns, book)
	_ = os.RemoveAll(s.Store.Dir("data", ns, util.BookDirName(book.Name, book.Author)))
	return success("删除书籍成功")
}

func (s *Server) deleteBooks(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	arr := bodyArray(r)
	ns := ri.Namespace
	books := s.loadBooks(ns)
	for _, v := range arr {
		book := bookFromAny(v)
		if book == nil {
			continue
		}
		for i, b := range books {
			if (b.BookUrl != "" && b.BookUrl == book.BookUrl) || (b.Name == book.Name && b.Author == book.Author) {
				books = removeAtIdx(books, i)
				book = b
				break
			}
		}
		s.invalidateChapterList(ns, book)
		s.invalidateContentCache(ns, book)
		_ = os.RemoveAll(s.Store.Dir("data", ns, util.BookDirName(book.Name, book.Author)))
	}
	_ = s.saveBooks(ns, books)
	return success("")
}

func (s *Server) getInvalidBookSources(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	return success(s.invalidList(ri.Namespace))
}

func (s *Server) exploreBook(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	ruleFindURL := queryOrBody(r, m, "ruleFindUrl")
	page := queryIntOrBody(r, m, "page", 1)
	srcStr := s.sourceStringFromRequest(m, ri.Namespace, queryOrBody(r, m, "bookSourceUrl"))
	if srcStr == "" {
		return failure("未找到书源信息")
	}
	var bs model.BookSource
	if json.Unmarshal([]byte(srcStr), &bs) != nil {
		return failure("书源信息错误")
	}
	list, err := webbook.New(&bs).Explore(ruleFindURL, page)
	if err != nil {
		return failure("探索失败: " + err.Error())
	}
	return success(list)
}

func (s *Server) searchBook(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	key := queryOrBody(r, m, "key")
	page := queryIntOrBody(r, m, "page", 1)
	srcStr := s.sourceStringFromRequest(m, ri.Namespace, queryOrBody(r, m, "bookSourceUrl"))
	if key == "" {
		return failure("请输入搜索关键字")
	}
	if srcStr == "" {
		return failure("未配置书源")
	}
	var bs model.BookSource
	if json.Unmarshal([]byte(srcStr), &bs) != nil {
		return failure("书源信息错误")
	}
	start := time.Now()
	list, err := webbook.New(&bs).Search(key, page)
	if err != nil {
		logger.Warn("search book failed", "source", bs.BookSourceName, "key", key, "page", page, "error", err)
		return failure("搜索失败: " + err.Error())
	}
	logger.Debug("search book completed",
		"source", bs.BookSourceName,
		"key", key,
		"page", page,
		"count", len(list),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return success(list)
}

// runSearchBatches performs parallel source searches batch by batch.
func (s *Server) runSearchBatches(ns string, sources []string, key string, startIndex, concurrent, maxLoop int, onBatch func(loop, lastIndex int, list []*model.SearchBook) bool) int {
	if concurrent <= 0 {
		concurrent = 8
	}
	if maxLoop <= 0 {
		maxLoop = 8
	}
	lastIndex := startIndex - 1
	loop := 0
	for i := startIndex; i < len(sources); i += concurrent {
		if loop >= maxLoop {
			break
		}
		end := i + concurrent
		if end > len(sources) {
			end = len(sources)
		}
		results := make([][]*model.SearchBook, end-i)
		var wg sync.WaitGroup
		probeBook := &model.Book{Name: key}
		for j, idx := 0, i; idx < end; j, idx = j+1, idx+1 {
			wg.Add(1)
			go func(j, idx int) {
				defer wg.Done()
				results[j] = s.searchBookWithSource(ns, sources[idx], probeBook, false)
			}(j, idx)
		}
		wg.Wait()
		lastIndex = end - 1
		batch := []*model.SearchBook{}
		for _, rs := range results {
			batch = append(batch, rs...)
		}
		loop++
		if !onBatch(loop, lastIndex, batch) {
			break
		}
		if end >= len(sources) {
			break
		}
	}
	return lastIndex
}

func dedupSearchBooks(list []*model.SearchBook, seen map[string]bool, target *[]*model.SearchBook) {
	for _, b := range list {
		key := b.Name + "_" + b.Author
		if seen[key] {
			continue
		}
		seen[key] = true
		*target = append(*target, b)
	}
}

func (s *Server) searchBookMulti(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	sources := s.bookSourceStringsForRequest(r, m, ri.Namespace)
	if len(sources) == 0 {
		return failure("未配置书源")
	}
	key := queryOrBody(r, m, "key")
	if key == "" {
		return failure("请输入搜索关键字")
	}
	lastIndex := queryIntOrBody(r, m, "lastIndex", -1)
	searchSize := queryIntOrBody(r, m, "searchSize", 20)
	concurrent := queryIntOrBody(r, m, "concurrentCount", 36)
	if searchSize <= 0 {
		searchSize = 20
	}
	if lastIndex >= len(sources)-1 {
		return failure("没有更多了")
	}
	seen := map[string]bool{}
	result := []*model.SearchBook{}
	start := time.Now()
	next := s.runSearchBatches(ri.Namespace, sources, key, lastIndex+1, concurrent, 8, func(loop, li int, batch []*model.SearchBook) bool {
		dedupSearchBooks(batch, seen, &result)
		return len(result) < searchSize
	})
	logger.Debug("multi-source search completed",
		"namespace", ri.Namespace,
		"key", key,
		"lastIndex", next,
		"count", len(result),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return success(map[string]any{"lastIndex": next, "list": result})
}

func (s *Server) searchBookMultiSSE(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	f, _ := w.(http.Flusher)
	if ri.NeedLogin {
		sseWrite(w, f, "error", failureLogin())
		return nil
	}
	m := bodyMap(r)
	sources := s.bookSourceStringsForRequest(r, m, ri.Namespace)
	if len(sources) == 0 {
		sseWrite(w, f, "error", failure("未配置书源"))
		return nil
	}
	key := queryOrBody(r, m, "key")
	if key == "" {
		sseWrite(w, f, "error", failure("请输入搜索关键字"))
		return nil
	}
	lastIndex := queryIntOrBody(r, m, "lastIndex", -1)
	searchSize := queryIntOrBody(r, m, "searchSize", 50)
	concurrent := queryIntOrBody(r, m, "concurrentCount", 24)
	if searchSize <= 0 {
		searchSize = 50
	}
	if lastIndex >= len(sources)-1 {
		sseWrite(w, f, "error", failure("没有更多了"))
		return nil
	}
	seen := map[string]bool{}
	result := []*model.SearchBook{}
	start := time.Now()
	next := s.runSearchBatches(ri.Namespace, sources, key, lastIndex+1, concurrent, 8, func(loop, li int, batch []*model.SearchBook) bool {
		loopResult := []*model.SearchBook{}
		dedupSearchBooks(batch, seen, &loopResult)
		dedupSearchBooks(loopResult, map[string]bool{}, &result)
		sseWrite(w, f, "", map[string]any{"lastIndex": li, "data": loopResult})
		return len(result) < searchSize
	})
	logger.Debug("multi-source search stream completed",
		"namespace", ri.Namespace,
		"key", key,
		"lastIndex", next,
		"count", len(result),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	sseWrite(w, f, "end", map[string]any{"lastIndex": next})
	return nil
}

func (s *Server) bookSourceStringsForRequest(r *http.Request, m map[string]any, ns string) []string {
	srcURL := queryOrBody(r, m, "bookSourceUrl")
	if srcURL != "" {
		if src, _ := s.sourceByURL(ns, srcURL); src != "" {
			return []string{src}
		}
		// use an inline source supplied by the request
		if bs, ok := m["bookSource"].(map[string]any); ok {
			if b, err := json.Marshal(bs); err == nil {
				return []string{string(b)}
			}
		}
		return nil
	}
	group := queryOrBody(r, m, "bookSourceGroup")
	return s.sourceStrings(ns, group)
}

func (s *Server) getBookInfo(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	bookURL := ""
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
		if sb, ok := m["searchBook"].(map[string]any); ok {
			bookURL = str(sb, "bookUrl")
		}
		bookURL = queryOrBody(r, m, "url")
	} else {
		bookURL = r.URL.Query().Get("url")
	}
	if bookURL == "" {
		return failure("请输入书籍链接")
	}
	var book *model.Book
	if !ri.NeedLogin {
		book = s.shelfBookByURL(ri.Namespace, bookURL)
	}
	if book == nil {
		book = s.cachedBookInfo(bookURL)
	}
	if book == nil {
		sourceURL := ""
		if sb, ok := m["searchBook"].(map[string]any); ok {
			sourceURL = str(sb, "origin")
		}
		if sourceURL == "" {
			sourceURL = queryOrBody(r, m, "bookSourceUrl")
		}
		srcStr := s.sourceStringFromRequest(m, ri.Namespace, sourceURL)
		if srcStr == "" {
			return failure("未配置书源")
		}
		var bs model.BookSource
		if json.Unmarshal([]byte(srcStr), &bs) != nil {
			return failure("书源信息错误")
		}
		nb := model.NewBook()
		nb.BookUrl = bookURL
		info, err := webbook.New(&bs).BookInfo(nb, false)
		if err != nil {
			return failure("获取书籍信息失败: " + err.Error())
		}
		info = s.mergeBookCacheInfoPtr(info)
		book = info
	}
	s.saveBookInfoCache(book)
	return success(book)
}

func (s *Server) mergeBookCacheInfoPtr(book *model.Book) *model.Book {
	s.mergeBookCacheInfo(book)
	return book
}

func (s *Server) getChapterList(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	var book *model.Book
	if inner, ok := m["book"].(map[string]any); ok {
		book = bookFromAny(inner)
	} else if len(m) > 0 {
		if _, ok := m["bookUrl"]; ok {
			book = bookFromAny(m)
		}
	}
	if book == nil {
		url := queryOrBody(r, m, "url")
		if url == "" {
			return failure("请输入书籍链接")
		}
		book = s.shelfBookByURL(ri.Namespace, url)
		if book == nil {
			book = s.cachedBookInfo(url)
		}
	}
	if book == nil {
		return failure("书籍信息错误")
	}
	refresh := queryIntOrBody(r, m, "refresh", 0)
	srcStr := s.sourceStringFromRequest(m, ri.Namespace, book.Origin)
	chapters, err := s.chapterList(ri.Namespace, book, srcStr, refresh > 0)
	if err != nil {
		logger.Warn("get chapter list failed",
			"namespace", ri.Namespace, "book", book.Name, "bookUrl", book.BookUrl, "error", err)
		return failure(err.Error())
	}
	if len(chapters) == 0 {
		logger.Warn("get chapter list empty",
			"namespace", ri.Namespace, "book", book.Name, "bookUrl", book.BookUrl, "tocUrl", book.TocUrl)
		return failure("目录为空")
	}
	logger.Debug("chapter list served",
		"namespace", ri.Namespace, "book", book.Name, "count", len(chapters))
	return success(chapters)
}

func (s *Server) getBookContent(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	url := queryOrBody(r, m, "url")
	if sb, ok := m["searchBook"].(map[string]any); ok && url == "" {
		url = str(sb, "bookUrl")
	}
	chapterUrl := queryOrBody(r, m, "chapterUrl")
	if bc, ok := m["bookChapter"].(map[string]any); ok && chapterUrl == "" {
		chapterUrl = str(bc, "url")
	}
	if url == "" {
		return failure("请输入书籍链接")
	}
	index := queryIntOrBody(r, m, "index", -1)
	cache := queryIntOrBody(r, m, "cache", 0)
	refresh := queryIntOrBody(r, m, "refresh", 0)
	book := s.shelfBookByURL(ri.Namespace, url)
	inShelf := book != nil
	if book == nil {
		book = s.cachedBookInfo(url)
	}
	if book == nil {
		return failure("获取书籍信息失败")
	}
	srcStr := s.sourceStringFromRequest(m, ri.Namespace, book.Origin)
	chapters, err := s.chapterList(ri.Namespace, book, srcStr, false)
	if err != nil {
		return failure(err.Error())
	}
	if index < 0 && chapterUrl != "" {
		for i, c := range chapters {
			if c.URL == chapterUrl {
				index = i
				break
			}
		}
	}
	if index >= len(chapters) {
		return failure("章节不存在")
	}
	if index < 0 {
		return failure("获取章节链接失败")
	}
	chapter := chapters[index]
	var nextURL string
	if index+1 < len(chapters) {
		nextURL = chapters[index+1].URL
	}

	ns := ri.Namespace
	var content string
	if book.Origin == model.BookTypeLocal {
		if cached, ok := s.chapterCacheContent(ns, book, chapter.Index); ok {
			content = cached
		} else {
			if localbook.IsEpub(book) {
				if !s.extractEpub(book, false) {
					return failure("Epub书籍解压失败")
				}
			}
			if localbook.IsCbz(book) && !s.extractCbz(book, false) {
				return failure("CBZ书籍解压失败")
			}
			content, err = localbook.GetContent(book, chapter, s.App.WorkDir)
			if err != nil {
				return failure(err.Error())
			}
			if localbook.IsEpub(book) {
				_ = s.putChapterCacheContent(ns, book, chapter.Index, content)
			}
		}
	} else {
		if refresh <= 0 {
			content, _ = s.chapterCacheContent(ns, book, chapter.Index)
		}
		if content == "" {
			if srcStr == "" {
				return failure("书源信息错误")
			}
			var bs model.BookSource
			if json.Unmarshal([]byte(srcStr), &bs) != nil {
				return failure("书源信息错误")
			}
			content, err = webbook.New(&bs).Content(book, chapter, nextURL)
			if err != nil {
				logger.Warn("get chapter content failed",
					"namespace", ns, "book", book.Name, "chapter", chapter.Title,
					"chapterUrl", chapter.URL, "error", err)
				return failure(err.Error())
			}
			_ = s.putChapterCacheContent(ns, book, chapter.Index, content)
		}
		if book.UseReplaceRule {
			content = s.applyReplaceRules(ns, content)
		}
	}
	book.DurChapterIndex = chapter.Index
	book.DurChapterTitle = chapter.Title
	book.DurChapterTime = time.Now().UnixMilli()
	if inShelf && cache != 1 {
		s.saveShelfBookProgress(ns, book, chapter)
	}
	return success(content)
}

func (s *Server) saveBookProgress(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	url := queryOrBody(r, m, "url")
	if sb, ok := m["searchBook"].(map[string]any); ok && url == "" {
		url = str(sb, "bookUrl")
	}
	index := queryIntOrBody(r, m, "index", -1)
	if url == "" {
		return failure("请输入书籍链接")
	}
	book := s.shelfBookByURL(ri.Namespace, url)
	if book == nil || book.Origin == "" {
		return failure("书籍未加入书架")
	}
	srcStr := s.sourceStringFromRequest(m, ri.Namespace, book.Origin)
	chapters, err := s.chapterList(ri.Namespace, book, srcStr, false)
	if err != nil {
		return failure(err.Error())
	}
	if index >= len(chapters) {
		return failure("章节不存在")
	}
	if index >= 0 {
		s.saveShelfBookProgress(ri.Namespace, book, chapters[index])
	}
	return success("")
}

func (s *Server) getBookCover(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	coverURL := r.URL.Query().Get("path")
	if coverURL == "" {
		w.WriteHeader(http.StatusNotFound)
		return nil
	}
	ext := extOf(coverURL, "png")
	cacheFile := filepath.Join(s.Store.Root, "cache", util.MD5(coverURL)+"."+ext)
	if _, err := os.Stat(cacheFile); err == nil {
		w.Header().Set("Cache-Control", "86400")
		http.ServeFile(w, r, cacheFile)
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(coverURL)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusNotFound)
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(cacheFile), 0o755)
	_ = os.WriteFile(cacheFile, data, 0o644)
	w.Header().Set("Cache-Control", "86400")
	http.ServeFile(w, r, cacheFile)
	return nil
}

func extOf(path, def string) string {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "" {
		return def
	}
	return ext
}

func (s *Server) searchBookSource(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	url := queryOrBody(r, m, "url")
	if url == "" {
		return failure("请输入书籍链接")
	}
	book := s.shelfBookByURL(ri.Namespace, url)
	if book == nil {
		book = s.cachedBookInfo(url)
	}
	if book == nil {
		return failure("书籍信息错误")
	}
	sources := s.bookSourceStringsForRequest(r, m, ri.Namespace)
	if len(sources) == 0 {
		return failure("未配置书源")
	}
	lastIndex := queryIntOrBody(r, m, "lastIndex", -1)
	searchSize := queryIntOrBody(r, m, "searchSize", 50)
	concurrent := queryIntOrBody(r, m, "concurrentCount", 24)
	if lastIndex >= len(sources)-1 {
		return failure("没有更多了")
	}
	seen := map[string]bool{}
	result := []*model.SearchBook{}
	next := s.runSearchBatches(ri.Namespace, sources, book.Name, lastIndex+1, concurrent, 8, func(loop, li int, batch []*model.SearchBook) bool {
		dedupSearchBooks(batch, seen, &result)
		return len(result) < searchSize
	})
	for _, sb := range result {
		sb.LatestChapterTitle = book.LatestChapterTitle
	}
	return success(map[string]any{"lastIndex": next, "list": result})
}

func (s *Server) searchBookSourceSSE(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	f, _ := w.(http.Flusher)
	if ri.NeedLogin {
		sseWrite(w, f, "error", failureLogin())
		return nil
	}
	m := bodyMap(r)
	url := queryOrBody(r, m, "url")
	if url == "" {
		sseWrite(w, f, "error", failure("请输入书籍链接"))
		return nil
	}
	book := s.shelfBookByURL(ri.Namespace, url)
	if book == nil {
		book = s.cachedBookInfo(url)
	}
	if book == nil {
		sseWrite(w, f, "error", failure("书籍信息错误"))
		return nil
	}
	sources := s.bookSourceStringsForRequest(r, m, ri.Namespace)
	if len(sources) == 0 {
		sseWrite(w, f, "error", failure("未配置书源"))
		return nil
	}
	lastIndex := queryIntOrBody(r, m, "lastIndex", -1)
	searchSize := queryIntOrBody(r, m, "searchSize", 50)
	concurrent := queryIntOrBody(r, m, "concurrentCount", 24)
	if lastIndex >= len(sources)-1 {
		sseWrite(w, f, "error", failure("没有更多了"))
		return nil
	}
	seen := map[string]bool{}
	result := []*model.SearchBook{}
	next := s.runSearchBatches(ri.Namespace, sources, book.Name, lastIndex+1, concurrent, 8, func(loop, li int, batch []*model.SearchBook) bool {
		loopResult := []*model.SearchBook{}
		dedupSearchBooks(batch, seen, &loopResult)
		dedupSearchBooks(loopResult, map[string]bool{}, &result)
		for _, sb := range loopResult {
			sb.LatestChapterTitle = book.LatestChapterTitle
		}
		sseWrite(w, f, "", map[string]any{"lastIndex": li, "data": loopResult})
		return len(result) < searchSize
	})
	sseWrite(w, f, "end", map[string]any{"lastIndex": next})
	return nil
}

func (s *Server) getAvailableBookSource(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	url := queryOrBody(r, m, "url")
	if url == "" {
		return failure("请输入书籍链接")
	}
	refresh := queryIntOrBody(r, m, "refresh", 0)
	book := s.shelfBookByURL(ri.Namespace, url)
	if book == nil {
		book = s.cachedBookInfo(url)
	}
	if book == nil {
		return failure("书籍信息错误")
	}
	list := s.bookSourcesForBook(ri.Namespace, book)
	if len(list) == 0 {
		return success([]int{})
	}
	if refresh <= 0 {
		return success(list)
	}
	existing := map[string]bool{}
	for _, sb := range list {
		existing[sb.BookUrl] = true
	}
	var result []*model.SearchBook
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	sources := s.sourceStrings(ri.Namespace, "")
	for _, sb := range list {
		if sb.Origin == model.BookTypeLocal {
			result = append(result, sb)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(sb *model.SearchBook) {
			defer wg.Done()
			defer func() { <-sem }()
			for _, src := range sources {
				if bs := s.bookSourceByURL(ri.Namespace, src); bs != nil && bs.BookSourceUrl == sb.Origin {
					for _, found := range s.searchBookWithSource(ri.Namespace, src, book, false) {
						mu.Lock()
						if !existing[found.BookUrl] {
							existing[found.BookUrl] = true
							result = append(result, found)
						}
						mu.Unlock()
					}
					return
				}
			}
		}(sb)
	}
	wg.Wait()
	s.saveBookSourcesForBook(ri.Namespace, book, result, true)
	return success(result)
}

func (s *Server) setBookSource(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	bookURL := queryOrBody(r, m, "bookUrl")
	newURL := queryOrBody(r, m, "newUrl")
	sourceURL := queryOrBody(r, m, "bookSourceUrl")
	if bookURL == "" {
		return failure("书籍链接不能为空")
	}
	if newURL == "" {
		return failure("新源书籍链接不能为空")
	}
	if sourceURL == "" {
		return failure("书源链接不能为空")
	}
	ns := ri.Namespace
	book := s.shelfBookByURL(ns, bookURL)
	if book == nil {
		return failure("书籍信息错误")
	}
	srcStr, _ := s.sourceByURL(ns, sourceURL)
	var newBook *model.Book
	if srcStr == "" {
		for _, sb := range s.bookSourcesForBook(ns, book) {
			if sb.BookUrl == newURL {
				newBook = s.bookFromSearchBook(sb)
				break
			}
		}
		if newBook == nil {
			return failure("书源信息错误")
		}
	} else {
		var bs model.BookSource
		if json.Unmarshal([]byte(srcStr), &bs) != nil {
			return failure("书源信息错误")
		}
		nb := model.NewBook()
		nb.BookUrl = newURL
		info, err := webbook.New(&bs).BookInfo(nb, false)
		if err != nil {
			return failure("获取书籍信息失败: " + err.Error())
		}
		newBook = info
	}
	s.editShelfBook(ns, book, func(exist *model.Book) {
		exist.Origin = newBook.Origin
		exist.OriginName = newBook.OriginName
		exist.BookUrl = newBook.BookUrl
		exist.TocUrl = newBook.TocUrl
		if exist.CoverUrl == "" && newBook.CoverUrl != "" {
			exist.CoverUrl = newBook.CoverUrl
		}
	})
	s.invalidateChapterList(ns, book)
	s.invalidateContentCache(ns, book)
	updated := s.shelfBookByURL(ns, newURL)
	if updated == nil {
		updated = newBook
	}
	if len(s.bookSourcesForBook(ns, updated)) > 0 {
		s.saveBookSourcesForBook(ns, updated, []*model.SearchBook{searchBookFromBook(updated)}, false)
	}
	_, _ = s.chapterList(ns, updated, srcStr, true)
	return success(updated)
}

func (s *Server) saveBookGroupId(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	bookURL := queryOrBody(r, m, "bookUrl")
	groupId := queryIntOrBody(r, m, "groupId", 0)
	if bookURL == "" {
		return failure("书籍链接不能为空")
	}
	book := s.shelfBookByURL(ri.Namespace, bookURL)
	if book == nil {
		return failure("书籍信息错误")
	}
	if groupId <= 0 {
		return failure("分组信息错误")
	}
	book.Group = groupId
	s.editShelfBook(ri.Namespace, book, func(exist *model.Book) { exist.Group = groupId })
	return success(book)
}

func (s *Server) addBookGroupMulti(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	return s.bookGroupMulti(r, ri, true)
}

func (s *Server) removeBookGroupMulti(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	return s.bookGroupMulti(r, ri, false)
}

func (s *Server) bookGroupMulti(r *http.Request, ri *reqInfo, add bool) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	groupId := intVal(m, "groupId", 0)
	if groupId <= 0 {
		return failure("分组信息错误")
	}
	arr, _ := m["bookList"].([]any)
	for _, v := range arr {
		book := bookFromAny(v)
		if book == nil {
			continue
		}
		s.editShelfBook(ri.Namespace, book, func(exist *model.Book) {
			if add {
				exist.Group = exist.Group | groupId
			} else {
				exist.Group = exist.Group ^ groupId
			}
		})
	}
	return success("")
}

func (s *Server) getBookGroups(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	groups := s.loadBookGroups(ri.Namespace)
	return success(groups)
}

func (s *Server) loadBookGroups(ns string) []*model.BookGroup {
	raw, err := s.Store.Get("data", ns, "bookGroup")
	if err != nil {
		groups := []*model.BookGroup{
			{GroupId: -1, GroupName: "全部", Order: -10, Show: true},
			{GroupId: -2, GroupName: "本地", Order: -9, Show: true},
			{GroupId: -3, GroupName: "音频", Order: -8, Show: true},
			{GroupId: -4, GroupName: "未分组", Order: -7, Show: true},
		}
		_ = s.Store.Save(groups, "data", ns, "bookGroup")
		return groups
	}
	var groups []*model.BookGroup
	_ = json.Unmarshal(raw, &groups)
	return groups
}

func (s *Server) saveBookGroup(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	var group model.BookGroup
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &group)
	if group.GroupName == "" {
		return failure("分组名称不能为空")
	}
	groups := s.loadBookGroups(ri.Namespace)
	idx := -1
	for i, g := range groups {
		if g.GroupId == group.GroupId {
			idx = i
			break
		}
	}
	if idx >= 0 {
		groups[idx] = &group
	} else {
		if group.GroupId >= 0 {
			idsSum := int64(0)
			maxOrder := 0
			for _, g := range groups {
				if g.GroupId > 0 {
					idsSum |= g.GroupId
				}
				if g.Order > maxOrder {
					maxOrder = g.Order
				}
			}
			id := int64(1)
			for id&idsSum != 0 {
				id <<= 1
			}
			group.GroupId = id
			group.Order = maxOrder + 1
		}
		groups = append(groups, &group)
	}
	_ = s.Store.Save(groups, "data", ri.Namespace, "bookGroup")
	return success("")
}

func (s *Server) deleteBookGroup(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	groupId := intVal(m, "groupId", 0)
	groups := s.loadBookGroups(ri.Namespace)
	for i, g := range groups {
		if g.GroupId == int64(groupId) {
			groups = removeAtIdx(groups, i)
			break
		}
	}
	_ = s.Store.Save(groups, "data", ri.Namespace, "bookGroup")
	books := s.loadBooks(ri.Namespace)
	for _, b := range books {
		if b.Group&groupId != 0 {
			b.Group ^= groupId
		}
	}
	_ = s.saveBooks(ri.Namespace, books)
	return success("")
}

func (s *Server) saveBookGroupOrder(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	arr, ok := m["order"].([]any)
	if !ok {
		return failure("参数错误")
	}
	orderMap := map[int64]int{}
	for _, v := range arr {
		if om, ok := v.(map[string]any); ok {
			orderMap[int64(intVal(om, "groupId", 0))] = intVal(om, "order", 0)
		}
	}
	groups := s.loadBookGroups(ri.Namespace)
	for _, g := range groups {
		if o, ok := orderMap[g.GroupId]; ok {
			g.Order = o
		}
	}
	_ = s.Store.Save(groups, "data", ri.Namespace, "bookGroup")
	return success("")
}

func (s *Server) getTxtTocRules(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	return success(localbook.DefaultTxtTocRules())
}

func (s *Server) refreshLocalBook(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	var book *model.Book
	if inner, ok := m["book"].(map[string]any); ok {
		book = bookFromAny(inner)
	} else if bookObj, ok := m["book"].(any); ok {
		book = bookFromAny(bookObj)
	} else {
		book = bookFromAny(m)
	}
	if book == nil {
		return failure("参数错误")
	}
	if !s.localFileExists(book) {
		return failure("书籍文件不存在")
	}
	srcStr := s.sourceStringFromRequest(m, ri.Namespace, book.Origin)
	if _, err := s.chapterList(ri.Namespace, book, srcStr, true); err != nil {
		return failure(err.Error())
	}
	return success(book)
}

func (s *Server) getChapterListByRule(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	var book *model.Book
	if inner, ok := m["book"].(map[string]any); ok {
		book = bookFromAny(inner)
	} else {
		book = bookFromAny(m)
	}
	if book == nil {
		return failure("参数错误")
	}
	if book.Origin == "" {
		book.Origin = model.BookTypeLocal
	}
	if !s.localFileExists(book) {
		return failure("文件不存在")
	}
	if localbook.IsEpub(book) {
		s.extractEpub(book, true)
	}
	if localbook.IsCbz(book) {
		s.extractCbz(book, true)
	}
	chapters, err := s.chapterList(ri.Namespace, book, "", true)
	if err != nil {
		return failure(err.Error())
	}
	return success(map[string]any{"book": book, "chapters": chapters})
}

func (s *Server) getShelfBookWithCacheInfo(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	url := r.URL.Query().Get("url")
	if url == "" {
		return failure("书源链接不能为空")
	}
	book := s.shelfBookByURL(ri.Namespace, url)
	if book == nil {
		return failure("书籍不存在")
	}
	cached := s.cachedChapterSet(ri.Namespace, book)
	return success(map[string]any{"book": book, "cachedChapterCount": len(cached)})
}

func (s *Server) deleteBookCache(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	url := queryOrBody(r, m, "url")
	book := s.shelfBookByURL(ri.Namespace, url)
	if book == nil && url != "" {
		book = s.cachedBookInfo(url)
	}
	if book == nil {
		return failure("书籍信息错误")
	}
	_ = os.RemoveAll(s.chapterCacheDir(ri.Namespace, book))
	s.invalidateContentCache(ri.Namespace, book)
	return success("")
}

// helpers used across handlers

func (s *Server) chapterCacheCount(ns string, book *model.Book) int {
	return len(s.cachedChapterSet(ns, book))
}

func sortedKeys(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func itoa(i int) string { return strconv.Itoa(i) }
