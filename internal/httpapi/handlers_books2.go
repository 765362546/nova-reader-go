package httpapi

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/765362546/novel-reader-go/internal/localbook"
	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/util"
	"github.com/765362546/novel-reader-go/internal/webbook"
)

func safeJoin(base, rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	clean := path.Clean("/" + rel)
	return filepath.Join(base, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
}

func (s *Server) checkLocalStore(w http.ResponseWriter, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if s.App.Secure && ri.User != nil && !ri.User.EnableLocalStore {
		return failure("未开启本地书仓功能")
	}
	return nil
}

func (s *Server) importBookPreview(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if err := r.ParseMultipartForm(200 << 20); err != nil || r.MultipartForm == nil {
		return failure("请上传书籍文件")
	}
	ns := ri.Namespace
	keys := make([]string, 0, len(r.MultipartForm.File))
	for k := range r.MultipartForm.File {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var fileList []map[string]any
	for _, k := range keys {
		for _, fh := range r.MultipartForm.File[k] {
			ext := strings.ToLower(extOf(fh.Filename, ""))
			if ext != "txt" && ext != "epub" && ext != "umd" && ext != "cbz" {
				return failure("不支持导入" + ext + "格式的书籍文件")
			}
			base := strings.TrimSuffix(filepath.Base(fh.Filename), filepath.Ext(fh.Filename))
			base = util.FormatFolderName(base)
			if len([]rune(base)) > 50 {
				base = string([]rune(base)[0:50])
			}
			fileName := base + "." + ext
			bookDir := filepath.Join(s.Store.Root, "assets", ns, "book")
			_ = os.MkdirAll(bookDir, 0o755)
			filePath := filepath.Join(bookDir, fileName)
			if ext == "epub" || ext == "cbz" {
				_ = os.MkdirAll(filePath, 0o755)
				filePath = filepath.Join(filePath, "index."+ext)
			}
			if err := saveUpload(fh, filePath); err != nil {
				return failure("保存上传文件失败")
			}
			localURL := "/assets/" + ns + "/book/" + fileName
			localRel := filepath.Join("storage", "assets", ns, "book", fileName)
			book, chapters := s.localPreviewBook(localURL, localRel, ext)
			fileList = append(fileList, map[string]any{"book": book, "chapters": chapters})
		}
	}
	if len(fileList) == 0 {
		return failure("请上传书籍文件")
	}
	return success(fileList)
}

func saveUpload(fh *multipart.FileHeader, dst string) error {
	in, err := fh.Open()
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

func (s *Server) localPreviewBook(bookURL, relPath, ext string) (*model.Book, []*model.BookChapter) {
	book := localbook.InitLocalBook(bookURL, relPath)
	book.OriginName = relPath
	chapters, err := localbook.GetChapterList(book, s.App.WorkDir, "")
	if err != nil {
		chapters = nil
	}
	if chapters == nil {
		chapters = []*model.BookChapter{}
	}
	return book, chapters
}

func (s *Server) importFromLocalPathPreview(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	pathValue := str(m, "path")
	var paths []string
	if arr, ok := m["path"].([]any); ok {
		for _, v := range arr {
			if sv, ok := v.(string); ok {
				paths = append(paths, sv)
			}
		}
	}
	if len(paths) == 0 && pathValue != "" {
		paths = append(paths, pathValue)
	}
	if len(paths) == 0 {
		return failure("参数错误")
	}
	var fileList []map[string]any
	for _, p := range paths {
		ext := strings.ToLower(extOf(p, ""))
		if ext != "txt" && ext != "epub" && ext != "umd" && ext != "cbz" {
			return failure("不支持导入" + ext + "格式的书籍文件")
		}
		var rel string
		if strings.Contains(filepath.ToSlash(p), "storage/localStore") {
			idx := strings.Index(filepath.ToSlash(p), "storage/localStore")
			rel = filepath.FromSlash(filepath.ToSlash(p)[idx:])
		} else {
			home := s.localStoreHome()
			full := safeJoin(home, p)
			rp, err := filepath.Rel(s.App.WorkDir, full)
			if err != nil {
				return failure("参数错误")
			}
			rel = rp
		}
		if _, err := os.Stat(s.absPath(rel)); err != nil {
			return failure("文件不存在")
		}
		book, chapters := s.localPreviewBook(rel, rel, ext)
		fileList = append(fileList, map[string]any{"book": book, "chapters": chapters})
	}
	if len(fileList) == 0 {
		return failure("参数错误")
	}
	return success(fileList)
}

func (s *Server) uploadFileToLocalStore(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.checkLocalStore(w, ri); rd != nil {
		return rd
	}
	if err := r.ParseMultipartForm(200 << 20); err != nil || r.MultipartForm == nil {
		return failure("请上传文件")
	}
	uploadPath := r.URL.Query().Get("path")
	if uploadPath == "" {
		uploadPath = r.FormValue("path")
	}
	home := s.localStoreHome()
	var fileList []map[string]any
	keys := make([]string, 0, len(r.MultipartForm.File))
	for k := range r.MultipartForm.File {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, fh := range r.MultipartForm.File[k] {
			fileName := filepath.Base(fh.Filename)
			dst := safeJoin(home, filepath.Join(uploadPath, fileName))
			if err := saveUpload(fh, dst); err != nil {
				return failure("上传失败")
			}
			fi := fileInfoMap(dst, home)
			if fi != nil {
				fileList = append(fileList, fi)
			}
		}
	}
	return success(fileList)
}

func (s *Server) getLocalStoreFileList(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.checkLocalStore(w, ri); rd != nil {
		return rd
	}
	p := r.URL.Query().Get("path")
	if p == "" && r.Method == http.MethodPost {
		p = str(bodyMap(r), "path")
	}
	if p == "" {
		p = "/"
	}
	home := s.localStoreHome()
	dir := safeJoin(home, p)
	st, err := os.Stat(dir)
	if err != nil {
		return failure("路径不存在")
	}
	if !st.IsDir() {
		return failure("路径不是目录")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return failure("读取目录失败")
	}
	var fileList []map[string]any
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if fi := fileInfoMap(filepath.Join(dir, e.Name()), home); fi != nil {
			fileList = append(fileList, fi)
		}
	}
	if fileList == nil {
		fileList = []map[string]any{}
	}
	return success(fileList)
}

func (s *Server) getLocalStoreFile(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.checkLocalStore(w, ri); rd != nil {
		return rd
	}
	p := r.URL.Query().Get("path")
	if r.Method == http.MethodPost {
		if m := bodyMap(r); p == "" {
			p = str(m, "path")
		}
	}
	if p == "" {
		return failure("参数错误")
	}
	file := safeJoin(s.localStoreHome(), p)
	if _, err := os.Stat(file); err != nil {
		return failure("路径不存在")
	}
	w.Header().Set("Cache-Control", "86400")
	w.Header().Set("Content-Disposition", "attachment; filename="+url.QueryEscape(filepath.Base(file)))
	http.ServeFile(w, r, file)
	return nil
}

func (s *Server) deleteLocalStoreFile(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.checkLocalStore(w, ri); rd != nil {
		return rd
	}
	if !s.managerOK(r, ri) {
		return failureSecureKey()
	}
	p := r.URL.Query().Get("path")
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
		if p == "" {
			p = str(m, "path")
		}
	}
	if p == "" {
		return failure("参数错误")
	}
	file := safeJoin(s.localStoreHome(), p)
	if _, err := os.Stat(file); err != nil {
		return failure("路径不存在")
	}
	_ = os.RemoveAll(file)
	return success("")
}

func (s *Server) deleteLocalStoreFileList(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.checkLocalStore(w, ri); rd != nil {
		return rd
	}
	if !s.managerOK(r, ri) {
		return failureSecureKey()
	}
	m := bodyMap(r)
	arr, ok := m["path"].([]any)
	if !ok {
		return failure("参数错误")
	}
	home := s.localStoreHome()
	for _, v := range arr {
		if p, ok := v.(string); ok {
			file := safeJoin(home, p)
			_ = os.RemoveAll(file)
		}
	}
	return success("")
}

func (s *Server) cacheBookSSE(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	f, _ := w.(http.Flusher)
	if ri.NeedLogin {
		sseWrite(w, f, "error", failureLogin())
		return nil
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	bookURL := queryOrBody(r, m, "url")
	if bookURL == "" {
		sseWrite(w, f, "error", failure("请输入书籍链接"))
		return nil
	}
	refresh := queryIntOrBody(r, m, "refresh", 0)
	concurrent := queryIntOrBody(r, m, "concurrentCount", 24)
	if concurrent <= 0 {
		concurrent = 24
	}
	ns := ri.Namespace
	book := s.shelfBookByURL(ns, bookURL)
	if book == nil {
		sseWrite(w, f, "error", failure("请先加入书架"))
		return nil
	}
	if book.Origin == model.BookTypeLocal {
		sseWrite(w, f, "error", failure("本地书籍无需缓存"))
		return nil
	}
	srcStr := s.sourceStringFromRequest(m, ns, book.Origin)
	if srcStr == "" {
		sseWrite(w, f, "error", failure("未配置书源"))
		return nil
	}
	var bs model.BookSource
	if json.Unmarshal([]byte(srcStr), &bs) != nil {
		sseWrite(w, f, "error", failure("书源信息错误"))
		return nil
	}
	chapters, err := s.chapterList(ns, book, srcStr, false)
	if err != nil {
		sseWrite(w, f, "error", failure(err.Error()))
		return nil
	}
	cached := map[int]bool{}
	if refresh <= 0 {
		cached = s.cachedChapterSet(ns, book)
	}
	wb := webbook.New(&bs)
	successCount, failedCount := 0, 0
	for i := 0; i < len(chapters); i += concurrent {
		end := i + concurrent
		if end > len(chapters) {
			end = len(chapters)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		batch := map[int]string{}
		for j := i; j < end; j++ {
			if cached[j] {
				continue
			}
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				var nextURL string
				if idx+1 < len(chapters) {
					nextURL = chapters[idx+1].URL
				}
				content, err := wb.Content(book, chapters[idx], nextURL)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failedCount++
					return
				}
				batch[idx] = content
				cached[idx] = true
				successCount++
			}(j)
		}
		wg.Wait()
		if err := s.mergeChapterCache(ns, book, batch); err != nil {
			logger.Warn("save chapter cache failed", "namespace", ns, "book", book.Name, "error", err)
		}
		sseWrite(w, f, "", map[string]any{
			"cachedCount":  len(cached),
			"successCount": successCount,
			"failedCount":  failedCount,
		})
	}
	sseWrite(w, f, "end", map[string]any{
		"cachedCount":  len(cached),
		"successCount": successCount,
		"failedCount":  failedCount,
	})
	logger.Info("book cache completed",
		"namespace", ns,
		"book", book.Name,
		"total", len(chapters),
		"cached", len(cached),
		"success", successCount,
		"failed", failedCount,
	)
	return nil
}

func (s *Server) bookSourceDebugSSE(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	f, _ := w.(http.Flusher)
	if ri.NeedLogin {
		sseWrite(w, f, "error", failureLogin())
		return nil
	}
	sourceURL := r.URL.Query().Get("bookSourceUrl")
	keyword := r.URL.Query().Get("keyword")
	if sourceURL == "" {
		sseWrite(w, f, "error", failure("未配置书源"))
		return nil
	}
	if keyword == "" {
		sseWrite(w, f, "error", failure("请输入搜索关键词"))
		return nil
	}
	srcStr, _ := s.sourceByURL(ri.Namespace, sourceURL)
	if srcStr == "" {
		sseWrite(w, f, "error", failure("未配置书源"))
		return nil
	}
	var bs model.BookSource
	if json.Unmarshal([]byte(srcStr), &bs) != nil {
		sseWrite(w, f, "error", failure("书源信息错误"))
		return nil
	}
	send := func(msg string) { sseWrite(w, f, "", map[string]any{"msg": msg}) }
	start := time.Now()
	send("开始搜索: " + keyword)
	list, err := webbook.New(&bs).Search(keyword, 1)
	if err != nil {
		send("搜索出错: " + err.Error())
		sseWrite(w, f, "end", map[string]any{"end": true})
		return nil
	}
	send(fmt.Sprintf("搜索成功, 共%d条结果, 耗时%dms", len(list), time.Since(start).Milliseconds()))
	if len(list) > 0 {
		book := s.bookFromSearchBook(list[0])
		send("获取书籍信息: " + book.Name)
		info, err := webbook.New(&bs).BookInfo(book, true)
		if err != nil {
			send("获取书籍信息失败: " + err.Error())
		} else {
			send(fmt.Sprintf("书籍信息获取成功: %s / %s", info.Name, info.Author))
			chapters, err := webbook.New(&bs).ChapterList(info)
			if err != nil {
				send("获取目录失败: " + err.Error())
			} else {
				send(fmt.Sprintf("目录获取成功: 共%d章", len(chapters)))
			}
		}
	}
	sseWrite(w, f, "end", map[string]any{"end": true})
	return nil
}

func (s *Server) exportBook(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	bookURL := queryOrBody(r, m, "url")
	bookURL = firstNonEmpty(bookURL, queryOrBody(r, m, "bookUrl"))
	isEpub := queryIntOrBody(r, m, "isEpub", 0)
	if bookURL == "" {
		return failure("请输入书籍链接")
	}
	ns := ri.Namespace
	book := s.shelfBookByURL(ns, bookURL)
	if book == nil {
		return failure("请先加入书架")
	}
	if book.Origin == model.BookTypeLocal {
		file := s.absPath(book.OriginName)
		if localbook.IsEpub(book) || localbook.IsCbz(book) {
			file = s.localArchivePath(book)
		}
		if _, err := os.Stat(file); err != nil {
			return failure("书籍文件不存在")
		}
		w.Header().Set("Cache-Control", "300")
		w.Header().Set("Content-Disposition", "attachment; filename="+url.QueryEscape(filepath.Base(file)))
		http.ServeFile(w, r, file)
		return nil
	}
	srcStr := s.sourceStringFromRequest(m, ns, book.Origin)
	if srcStr == "" {
		return failure("未配置书源")
	}
	chapters, err := s.chapterList(ns, book, srcStr, false)
	if err != nil {
		return failure(err.Error())
	}
	dir := s.chapterCacheDir(ns, book)
	author := util.FormatAuthor(book.Author)
	cacheData := s.loadChapterCache(ns, book)
	if isEpub > 0 {
		titles := make([]string, 0, len(chapters))
		contents := make([]string, 0, len(chapters))
		for i, c := range chapters {
			titles = append(titles, c.Title)
			if content, ok := cacheData[i]; ok {
				contents = append(contents, content)
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.txt", i)))
			if err != nil {
				contents = append(contents, "暂无缓存内容。")
				continue
			}
			contents = append(contents, string(raw))
		}
		filename := fmt.Sprintf("《%s》作者：%s.epub", util.FormatBookName(book.Name), author)
		tmp := filepath.Join(s.Store.Root, "assets", ns, "export", filename)
		_ = os.MkdirAll(filepath.Dir(tmp), 0o755)
		if err := writeEpubFile(tmp, book, titles, contents); err != nil {
			return failure("导出失败: " + err.Error())
		}
		w.Header().Set("Content-Disposition", "attachment; filename="+url.QueryEscape(filename))
		http.ServeFile(w, r, tmp)
		return nil
	}
	filename := fmt.Sprintf("《%s》作者：%s.txt", util.FormatBookName(book.Name), author)
	tmp := filepath.Join(s.Store.Root, "assets", ns, "export", filename)
	_ = os.MkdirAll(filepath.Dir(tmp), 0o755)
	f, err := os.Create(tmp)
	if err != nil {
		return failure("导出失败: " + err.Error())
	}
	// Stream chapters straight to disk instead of building the whole book in
	// memory, so large exports do not spike the process RSS.
	bw := bufio.NewWriterSize(f, 64<<10)
	_, _ = bw.WriteString(book.Name + "\n作者：" + author + "\n简介：" + util.HTMLFormat(book.Intro) + "\n")
	for i, c := range chapters {
		_, _ = bw.WriteString("\n\n" + c.Title + "\n")
		if content, ok := cacheData[i]; ok {
			_, _ = bw.WriteString(content)
		} else if raw, rerr := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.txt", i))); rerr == nil {
			_, _ = bw.Write(raw)
		} else {
			_, _ = bw.WriteString("暂无缓存内容。")
		}
		_, _ = bw.WriteString("\n")
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		return failure("导出失败: " + err.Error())
	}
	if err := f.Close(); err != nil {
		return failure("导出失败: " + err.Error())
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+url.QueryEscape(filename))
	http.ServeFile(w, r, tmp)
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func writeEpubFile(dst string, book *model.Book, titles, contents []string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	addEntry := func(name string, store bool, data []byte) error {
		header := &zip.FileHeader{Name: name}
		if store {
			header.Method = zip.Store
		} else {
			header.Method = zip.Deflate
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := addEntry("mimetype", true, []byte("application/epub+zip")); err != nil {
		return err
	}
	container := `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`
	if err := addEntry("META-INF/container.xml", false, []byte(container)); err != nil {
		return err
	}
	title := html.EscapeString(util.FormatBookName(book.Name))
	author := html.EscapeString(util.FormatAuthor(book.Author))
	uuid := "urn:uuid:" + util.MD5(book.BookUrl)
	var manifest, spine, navPoints strings.Builder
	navPoints.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head>
<meta name="dtb:uid" content="` + uuid + `"/></head><docTitle><text>` + title + `</text></docTitle><navMap>`)
	for i := range titles {
		chTitle := html.EscapeString(titles[i])
		file := fmt.Sprintf("chapter_%d.xhtml", i)
		body := strings.Builder{}
		body.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + chTitle + `</title></head><body><h2>` + chTitle + `</h2>`)
		for _, line := range strings.Split(contents[i], "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			body.WriteString("<p>" + html.EscapeString(line) + "</p>")
		}
		body.WriteString("</body></html>")
		if err := addEntry("OEBPS/Text/"+file, false, []byte(body.String())); err != nil {
			return err
		}
		manifest.WriteString(`<item id="c` + itoa(i) + `" href="Text/` + file + `" media-type="application/xhtml+xml"/>`)
		spine.WriteString(`<itemref idref="c` + itoa(i) + `"/>`)
		navPoints.WriteString(`<navPoint id="n` + itoa(i) + `" playOrder="` + itoa(i+1) + `"><navLabel><text>` + chTitle + `</text></navLabel><content src="Text/` + file + `"/></navPoint>`)
	}
	navPoints.WriteString(`</navMap></ncx>`)
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="bookid">
 <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
  <dc:title>` + title + `</dc:title><dc:creator>` + author + `</dc:creator>
  <dc:language>zh-CN</dc:language><dc:identifier id="bookid">` + uuid + `</dc:identifier>
 </metadata>
 <manifest>` + manifest.String() + `<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest>
 <spine toc="ncx">` + spine.String() + `</spine>
</package>`
	if err := addEntry("OEBPS/content.opf", false, []byte(opf)); err != nil {
		return err
	}
	return addEntry("OEBPS/toc.ncx", false, []byte(navPoints.String()))
}

func (s *Server) searchBookContent(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == http.MethodPost {
		m = bodyMap(r)
	}
	bookURL := queryOrBody(r, m, "url")
	query := firstNonEmpty(queryOrBody(r, m, "query"), queryOrBody(r, m, "key"))
	page := queryIntOrBody(r, m, "page", 1)
	if page <= 0 {
		page = 1
	}
	if bookURL == "" || query == "" {
		return failure("参数错误")
	}
	ns := ri.Namespace
	book := s.shelfBookByURL(ns, bookURL)
	if book == nil {
		return failure("请先加入书架")
	}
	srcStr := s.sourceStringFromRequest(m, ns, book.Origin)
	chapters, err := s.chapterList(ns, book, srcStr, false)
	if err != nil {
		return failure(err.Error())
	}
	dir := s.chapterCacheDir(ns, book)
	cacheData := s.loadChapterCache(ns, book)
	var results []*model.SearchResult
	for i, c := range chapters {
		content, ok := cacheData[i]
		if !ok {
			raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.txt", i)))
			if err != nil {
				continue
			}
			content = string(raw)
		}
		lower := strings.ToLower(content)
		q := strings.ToLower(query)
		from := 0
		for {
			pos := strings.Index(lower[from:], q)
			if pos < 0 {
				break
			}
			pos += from
			start := pos - 40
			if start < 0 {
				start = 0
			}
			end := pos + len(query) + 40
			if end > len(content) {
				end = len(content)
			}
			results = append(results, &model.SearchResult{
				ResultCount:              1,
				ResultCountWithinChapter: 1,
				ResultText:               content[start:end],
				ChapterTitle:             c.Title,
				Query:                    query,
				PageSize:                 10,
				ChapterIndex:             i,
				PageIndex:                page,
				QueryIndexInResult:       pos - start,
				QueryIndexInChapter:      pos,
			})
			from = pos + len(query)
			if from >= len(content) {
				break
			}
		}
	}
	if results == nil {
		results = []*model.SearchResult{}
	}
	return success(results)
}
