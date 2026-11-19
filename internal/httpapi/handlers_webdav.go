package httpapi

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/765362546/novel-reader-go/internal/model"
)

var backupZipPattern = regexp.MustCompile(`^backup[0-9-]+\.zip$`)

func (s *Server) webdavEnabled(ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if s.App.Secure && ri.User != nil && !ri.User.EnableWebdav {
		return failure("未开启webdav功能")
	}
	return nil
}

func (s *Server) getWebdavFileList(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.webdavEnabled(ri); rd != nil {
		return rd
	}
	p := r.URL.Query().Get("path")
	if r.Method == http.MethodPost {
		if m := bodyMap(r); p == "" {
			p = str(m, "path")
		}
	}
	if decoded, err := url.QueryUnescape(p); err == nil {
		p = decoded
	}
	if p == "" {
		p = "/"
	}
	home := s.webdavHome(ri.Namespace)
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
	fileList := []map[string]any{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if fi := fileInfoMap(filepath.Join(dir, e.Name()), home); fi != nil {
			fileList = append(fileList, fi)
		}
	}
	return success(fileList)
}

func (s *Server) getWebdavFile(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.webdavEnabled(ri); rd != nil {
		return rd
	}
	p := r.URL.Query().Get("path")
	if r.Method == http.MethodPost {
		if m := bodyMap(r); p == "" {
			p = str(m, "path")
		}
	}
	if decoded, err := url.QueryUnescape(p); err == nil {
		p = decoded
	}
	if p == "" {
		return failure("参数错误")
	}
	file := safeJoin(s.webdavHome(ri.Namespace), p)
	if _, err := os.Stat(file); err != nil {
		return failure("路径不存在")
	}
	w.Header().Set("Cache-Control", "86400")
	w.Header().Set("Content-Disposition", "attachment; filename="+url.QueryEscape(filepath.Base(file)))
	http.ServeFile(w, r, file)
	return nil
}

func (s *Server) uploadFileToWebdav(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.webdavEnabled(ri); rd != nil {
		return rd
	}
	if err := r.ParseMultipartForm(200 << 20); err != nil || r.MultipartForm == nil {
		return failure("请上传文件")
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
	}
	home := s.webdavHome(ri.Namespace)
	dir := safeJoin(home, p)
	fileList := []map[string]any{}
	for _, files := range r.MultipartForm.File {
		for _, fh := range files {
			dst := filepath.Join(dir, filepath.Base(fh.Filename))
			if err := saveUpload(fh, dst); err != nil {
				return failure("上传失败")
			}
			if fi := fileInfoMap(dst, home); fi != nil {
				fileList = append(fileList, fi)
			}
		}
	}
	return success(fileList)
}

func (s *Server) deleteWebdavFile(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.webdavEnabled(ri); rd != nil {
		return rd
	}
	p := r.URL.Query().Get("path")
	if r.Method == http.MethodPost {
		if m := bodyMap(r); p == "" {
			p = str(m, "path")
		}
	}
	if decoded, err := url.QueryUnescape(p); err == nil {
		p = decoded
	}
	if p == "" {
		return failure("参数错误")
	}
	file := safeJoin(s.webdavHome(ri.Namespace), p)
	if _, err := os.Stat(file); err != nil {
		return failure("路径不存在")
	}
	_ = os.RemoveAll(file)
	return success("")
}

func (s *Server) deleteWebdavFileList(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.webdavEnabled(ri); rd != nil {
		return rd
	}
	m := bodyMap(r)
	arr, ok := m["path"].([]any)
	if !ok {
		return failure("参数错误")
	}
	home := s.webdavHome(ri.Namespace)
	for _, v := range arr {
		p, _ := v.(string)
		if decoded, err := url.QueryUnescape(p); err == nil {
			p = decoded
		}
		if p != "" {
			_ = os.RemoveAll(safeJoin(home, p))
		}
	}
	return success("")
}

func (s *Server) webdavHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("DAV", "1,2")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Expose-Headers", "DAV, content-length, Allow")
	w.Header().Set("MS-Author-Via", "DAV")
	w.Header().Set("Allow", "OPTIONS,DELETE,GET,PUT,PROPFIND,MKCOL,MOVE,COPY,LOCK,UNLOCK")
	ns := "default"
	if s.App.Secure {
		w.Header().Set("WWW-Authenticate", `Basic realm="Default realm"`)
		user, ok := s.webdavBasicAuth(r)
		if !ok {
			if r.Method == http.MethodOptions && r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ns = user
	}
	home := s.webdavHome(ns)
	p := strings.TrimPrefix(r.URL.Path, "/reader3/webdav")
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	if p == "" {
		p = "/"
	}
	file := safeJoin(home, p)
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	case "PROPFIND":
		s.webdavPropfind(w, r, file, home)
	case "MKCOL":
		_ = os.MkdirAll(file, 0o755)
		w.WriteHeader(http.StatusCreated)
	case http.MethodPut:
		if _, err := os.Stat(filepath.Dir(file)); err != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if st, err := os.Stat(file); err == nil && st.IsDir() {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = os.MkdirAll(filepath.Dir(file), 0o755)
		if err := os.WriteFile(file, data, 0o644); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.Contains(file, "/bookProgress/") && strings.HasSuffix(file, ".json") {
			s.syncBookProgressFromFile(ns, file)
		}
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		if _, err := os.Stat(file); err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if st, _ := os.Stat(file); st != nil && st.IsDir() {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "86400")
		w.Header().Set("Content-Disposition", "attachment; filename="+url.QueryEscape(filepath.Base(file)))
		http.ServeFile(w, r, file)
	case http.MethodDelete:
		if _, err := os.Stat(file); err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = os.RemoveAll(file)
		w.WriteHeader(http.StatusOK)
	case "MOVE", "COPY":
		dest := r.Header.Get("Destination")
		if dest == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		u, err := url.Parse(dest)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		dp := strings.TrimPrefix(u.Path, "/reader3/webdav")
		if decoded, err := url.PathUnescape(dp); err == nil {
			dp = decoded
		}
		target := safeJoin(home, dp)
		if _, err := os.Stat(target); err == nil {
			if r.Header.Get("Overwrite") == "" {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			_ = os.RemoveAll(target)
		}
		if _, err := os.Stat(file); err != nil {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		if r.Method == "MOVE" {
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			_ = os.Rename(file, target)
		} else {
			_ = copyPath(file, target)
		}
		w.WriteHeader(http.StatusCreated)
	case "LOCK":
		lockToken := "urn:uuid:" + randomString(36)
		timeout := r.Header.Get("Timeout")
		if timeout == "" {
			timeout = "Second-3600"
		}
		w.Header().Set("Lock-Token", lockToken)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><D:prop xmlns:D="DAV:"><D:lockdiscovery><D:activelock><D:locktype><D:write/></D:locktype><D:lockscope><D:exclusive/></D:lockscope><D:depth>infinity</D:depth><D:timeout>%s</D:timeout><D:locktoken><D:href>%s</D:href></D:locktoken></D:activelock></D:lockdiscovery></D:prop>`, timeout, lockToken)
	case "UNLOCK":
		if r.Header.Get("Lock-Token") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) webdavBasicAuth(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if auth == "" || !strings.HasPrefix(strings.ToLower(auth), "basic ") {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[6:]))
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) < 2 {
		return "", false
	}
	username, password := parts[0], parts[1]
	users := s.loadUsers()
	u, ok := users[username]
	if !ok {
		return "", false
	}
	if genEncryptedPassword(password, u.Salt) != u.Password {
		return "", false
	}
	if !u.EnableWebdav {
		return "", false
	}
	return username, true
}

type webdavEntry struct {
	Name        string
	URL         string
	IsDir       bool
	Size        int64
	ModTime     time.Time
	ContentType string
}

func (s *Server) webdavPropfind(w http.ResponseWriter, r *http.Request, file, home string) {
	st, err := os.Stat(file)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	base := r.URL.Path
	var entries []webdavEntry
	if st.IsDir() {
		dirURL := base
		if !strings.HasSuffix(dirURL, "/") {
			dirURL += "/"
		}
		entries = append(entries, webdavEntry{Name: "", URL: dirURL, IsDir: true, ModTime: st.ModTime()})
		items, _ := os.ReadDir(file)
		for _, item := range items {
			info, err := item.Info()
			if err != nil {
				continue
			}
			entries = append(entries, webdavEntry{
				Name:        item.Name(),
				URL:         dirURL + url.PathEscape(item.Name()),
				IsDir:       item.IsDir(),
				Size:        info.Size(),
				ModTime:     info.ModTime(),
				ContentType: "application/octet-stream",
			})
		}
	} else {
		entries = append(entries, webdavEntry{Name: filepath.Base(file), URL: base, Size: st.Size(), ModTime: st.ModTime(), ContentType: "application/octet-stream"})
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(207)
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">`)
	for _, e := range entries {
		sb.WriteString(`<D:response><D:href>` + xmlEscape(e.URL) + `</D:href><D:propstat><D:status>HTTP/1.1 200 OK</D:status><D:prop>`)
		if e.IsDir {
			sb.WriteString(`<D:resourcetype><D:collection/></D:resourcetype><D:displayname>` + xmlEscape(e.Name) + `</D:displayname>`)
		} else {
			sb.WriteString(`<D:resourcetype/><D:displayname>` + xmlEscape(e.Name) + `</D:displayname>`)
			sb.WriteString(fmt.Sprintf(`<D:getcontentlength>%d</D:getcontentlength>`, e.Size))
			sb.WriteString(`<D:getcontenttype>` + e.ContentType + `</D:getcontenttype>`)
		}
		sb.WriteString(`<D:getlastmodified>` + e.ModTime.UTC().Format(http.TimeFormat) + `</D:getlastmodified>`)
		sb.WriteString(`</D:prop></D:propstat></D:response>`)
	}
	sb.WriteString(`</D:multistatus>`)
	_, _ = io.WriteString(w, sb.String())
}

func xmlEscape(s string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

func (s *Server) syncBookProgressFromFile(ns, file string) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return
	}
	var book model.Book
	if json.Unmarshal(raw, &book) != nil {
		return
	}
	s.editShelfBook(ns, &book, func(exist *model.Book) {
		exist.DurChapterIndex = book.DurChapterIndex
		exist.DurChapterPos = book.DurChapterPos
		exist.DurChapterTime = book.DurChapterTime
		exist.DurChapterTitle = book.DurChapterTitle
	})
}

func (s *Server) getLatestBackupZip(ns string) string {
	home := s.webdavHome(ns)
	var candidates []string
	for _, dir := range []string{filepath.Join(home, "legado"), home} {
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			continue
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if backupZipPattern.MatchString(strings.ToLower(e.Name())) {
				candidates = append(candidates, filepath.Join(dir, e.Name()))
			}
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		si, _ := os.Stat(candidates[i])
		sj, _ := os.Stat(candidates[j])
		if si == nil || sj == nil {
			return false
		}
		return si.ModTime().After(sj.ModTime())
	})
	return candidates[0]
}

// syncFromWebdav restores user data from a legado backup zip.
func (s *Server) syncFromWebdav(ns, zipPath string) bool {
	home := s.webdavHome(ns)
	tmp := filepath.Join(s.Store.Root, "data", ns, "tmp")
	_ = os.RemoveAll(tmp)
	if !unzipTo(zipPath, tmp) {
		return false
	}
	defer os.RemoveAll(tmp)
	dataDir := filepath.Join(s.Store.Root, "data", ns)
	for _, name := range []string{"bookSource.json", "bookshelf.json", "bookGroup.json", "rssSources.json", "replaceRule.json", "bookmark.json"} {
		from := filepath.Join(tmp, name)
		if _, err := os.Stat(from); err == nil {
			_ = os.Rename(from, filepath.Join(dataDir, name))
		}
	}
	progressDir := filepath.Join(home, "bookProgress")
	if _, err := os.Stat(progressDir); err != nil {
		progressDir = filepath.Join(home, "legado", "bookProgress")
	}
	if entries, err := os.ReadDir(progressDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				s.syncBookProgressFromFile(ns, filepath.Join(progressDir, e.Name()))
			}
		}
	}
	return true
}

// saveToWebdav merges local data into the latest legado backup zip.
func (s *Server) saveToWebdav(ns, zipPath string) bool {
	home := s.webdavHome(ns)
	tmp := filepath.Join(s.Store.Root, "data", ns, "tmp")
	_ = os.RemoveAll(tmp)
	if !unzipTo(zipPath, tmp) {
		return false
	}
	defer os.RemoveAll(tmp)
	dataDir := filepath.Join(s.Store.Root, "data", ns)
	for _, name := range []string{"bookSource.json", "bookshelf.json", "bookGroup.json", "rssSources.json", "replaceRule.json", "bookmark.json"} {
		from := filepath.Join(dataDir, name)
		if _, err := os.Stat(from); err == nil {
			_ = copyFile(from, filepath.Join(tmp, name))
		}
	}
	targetDir := home
	if strings.Contains(zipPath, "legado") {
		targetDir = filepath.Join(home, "legado")
	}
	_ = os.MkdirAll(targetDir, 0o755)
	target := filepath.Join(targetDir, "backup"+time.Now().Format("2006-01-02")+".zip")
	if err := zipDir(tmp, target); err != nil {
		return false
	}
	return true
}

func zipDir(src, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	root := src
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_ = func() error {
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			_, err = io.Copy(w, in)
			return err
		}()
		return nil
	})
}

func (s *Server) restoreFromWebdav(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.webdavEnabled(ri); rd != nil {
		return rd
	}
	p := r.URL.Query().Get("path")
	if r.Method == http.MethodPost {
		if m := bodyMap(r); p == "" {
			p = str(m, "path")
		}
	}
	if decoded, err := url.QueryUnescape(p); err == nil {
		p = decoded
	}
	if p == "" {
		return failure("参数错误")
	}
	if strings.ToLower(extOf(p, "")) != "zip" {
		return failure("路径不是zip备份文件")
	}
	file := safeJoin(s.webdavHome(ri.Namespace), p)
	if _, err := os.Stat(file); err != nil {
		return failure("路径不存在")
	}
	if !s.syncFromWebdav(ri.Namespace, file) {
		logger.Error("restore from webdav failed", "namespace", ri.Namespace, "path", p)
		return failure("恢复失败")
	}
	logger.Info("restored from webdav", "namespace", ri.Namespace, "path", p)
	return success("")
}

func (s *Server) backupToWebdav(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if rd := s.webdavEnabled(ri); rd != nil {
		return rd
	}
	zipPath := s.getLatestBackupZip(ri.Namespace)
	if zipPath == "" {
		return failure("请先使用阅读App备份到webdav")
	}
	if !s.saveToWebdav(ri.Namespace, zipPath) {
		logger.Error("backup to webdav failed", "namespace", ri.Namespace, "zip", zipPath)
		return failure("备份失败")
	}
	logger.Info("backed up to webdav", "namespace", ri.Namespace, "zip", zipPath)
	return success("")
}
