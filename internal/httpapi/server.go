package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/765362546/novel-reader-go/internal/config"
	"github.com/765362546/novel-reader-go/internal/logging"
	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/store"
)

var logger = logging.Component("httpapi")

// ReturnData is the unified API response.
type ReturnData struct {
	IsSuccess bool   `json:"isSuccess"`
	ErrorMsg  string `json:"errorMsg"`
	Data      any    `json:"data"`
}

func success(data any) *ReturnData {
	return &ReturnData{IsSuccess: true, ErrorMsg: "", Data: data}
}

func failure(msg string) *ReturnData {
	return &ReturnData{IsSuccess: false, ErrorMsg: msg}
}

func failureLogin() *ReturnData {
	return &ReturnData{IsSuccess: false, ErrorMsg: "请登录后使用", Data: "NEED_LOGIN"}
}

func failureSecureKey() *ReturnData {
	return &ReturnData{IsSuccess: false, ErrorMsg: "请输入管理密码", Data: "NEED_SECURE_KEY"}
}

type invalidBookSource struct {
	SourceUrl string `json:"sourceUrl"`
	Time      int64  `json:"time"`
	Error     string `json:"error"`
}

type Server struct {
	App      *config.App
	Store    *store.Store
	Sessions *Sessions

	cacheMu       sync.Mutex
	bookInfoCache map[string]*model.Book
	invalidCache  map[string]map[string]invalidBookSource
	shelfMu       sync.Mutex

	sourceMu      sync.RWMutex
	sourceIndexes map[string]*bookSourceIndex

	breakerMu sync.Mutex
	breakers  map[string]*sourceBreaker
}

func New(app *config.App) *Server {
	store := store.New(app.StorageRoot())
	if err := os.MkdirAll(filepath.Join(app.StorageRoot(), "data", "default"), 0o755); err != nil {
		logger.Error("create storage dir failed", "path", app.StorageRoot(), "error", err)
	}
	assetsDir := filepath.Join(app.StorageRoot(), "assets")
	_ = os.MkdirAll(assetsDir, 0o755)
	_ = os.MkdirAll(filepath.Join(app.StorageRoot(), "cache"), 0o755)

	// Mirror YueduApi: create a blank reader.css on first run so users
	// have a place to inject custom styles via the /assets/ endpoint.
	readerCSS := filepath.Join(assetsDir, "reader.css")
	if _, err := os.Stat(readerCSS); os.IsNotExist(err) {
		_ = os.WriteFile(readerCSS, []byte("/* 在此处可以编写CSS样式来自定义页面 */\n"), 0o644)
	}

	return &Server{
		App:           app,
		Store:         store,
		Sessions:      NewSessions(),
		bookInfoCache: map[string]*model.Book{},
		invalidCache:  map[string]map[string]invalidBookSource{},
		sourceIndexes: map[string]*bookSourceIndex{},
		breakers:      map[string]*sourceBreaker{},
	}
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData

func (s *Server) api(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ri := s.auth(r)
		if ri.NewCookie != "" {
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookie, Value: ri.NewCookie, Path: "/", HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}
		rd := fn(w, r, ri)
		if rd == nil {
			return
		}
		if !rd.IsSuccess {
			logger.Debug("api request failed",
				"method", r.Method,
				"path", r.URL.Path,
				"error", rd.ErrorMsg,
			)
		}
		writeJSON(w, rd)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (s *Server) Run() error {
	r := chi.NewRouter()
	s.routes(r)
	addr := fmt.Sprintf(":%d", s.App.Port)
	logger.Info("http server listening",
		"addr", addr,
		"webDir", s.App.WebDir,
		"storage", s.Store.Root,
		"secure", s.App.Secure,
	)
	s.StartScheduler()
	handler := requestLogger(recovery(r))
	return http.ListenAndServe(addr, handler)
}

// requestLogger records every request at Debug, promotes slow requests to
// Warn and 5xx responses to Error.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rw, r)
		if rw.status == 0 {
			rw.status = http.StatusOK
		}
		elapsed := time.Since(start)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"bytes", rw.bytes,
			"duration_ms", elapsed.Milliseconds(),
			"remote", remoteIP(r),
		}
		switch {
		case rw.status >= http.StatusInternalServerError:
			logger.Error("http request failed", attrs...)
		case elapsed > 2*time.Second:
			logger.Warn("slow http request", attrs...)
		default:
			logger.Debug("http request", attrs...)
		}
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rw *responseRecorder) WriteHeader(code int) {
	if rw.status == 0 {
		rw.status = code
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += n
	return n, err
}

// Flush keeps SSE endpoints working through the wrapper.
func (rw *responseRecorder) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.Error("panic recovered",
					"method", r.Method,
					"path", r.URL.Path,
					"panic", err,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes(r chi.Router) {
	// system
	r.Get("/reader3/getSystemInfo", s.api(s.getSystemInfo))

	// book source
	r.Post("/reader3/saveBookSource", s.api(s.saveBookSource))
	r.Post("/reader3/saveBookSources", s.api(s.saveBookSources))
	r.Get("/reader3/getBookSource", s.api(s.getBookSource))
	r.Post("/reader3/getBookSource", s.api(s.getBookSource))
	r.Get("/reader3/getBookSources", s.api(s.getBookSources))
	r.Post("/reader3/getBookSources", s.api(s.getBookSources))
	r.Post("/reader3/deleteAllBookSources", s.api(s.deleteAllBookSources))
	r.Post("/reader3/deleteBookSource", s.api(s.deleteBookSource))
	r.Post("/reader3/deleteBookSources", s.api(s.deleteBookSources))
	r.Post("/reader3/readSourceFile", s.api(s.readSourceFile))
	r.Post("/reader3/readRemoteSourceFile", s.api(s.readRemoteSourceFile))
	r.Post("/reader3/setAsDefaultBookSources", s.api(s.setAsDefaultBookSources))
	r.Post("/reader3/deleteUserBookSource", s.api(s.deleteUserBookSource))
	r.Post("/reader3/deleteBookSourcesFile", s.api(s.deleteBookSourcesFile))

	// books
	r.Get("/reader3/getBookshelf", s.api(s.getBookshelf))
	r.Get("/reader3/getShelfBook", s.api(s.getShelfBook))
	r.Post("/reader3/saveBook", s.api(s.saveBook))
	r.Post("/reader3/deleteBook", s.api(s.deleteBook))
	r.Post("/reader3/deleteBooks", s.api(s.deleteBooks))
	r.Post("/reader3/getInvalidBookSources", s.api(s.getInvalidBookSources))
	r.Post("/reader3/exploreBook", s.api(s.exploreBook))
	r.Get("/reader3/exploreBook", s.api(s.exploreBook))
	r.Get("/reader3/searchBook", s.api(s.searchBook))
	r.Post("/reader3/searchBook", s.api(s.searchBook))
	r.Get("/reader3/searchBookMulti", s.api(s.searchBookMulti))
	r.Post("/reader3/searchBookMulti", s.api(s.searchBookMulti))
	r.Get("/reader3/searchBookMultiSSE", s.apiSSE(s.searchBookMultiSSE))
	r.Get("/reader3/getBookInfo", s.api(s.getBookInfo))
	r.Post("/reader3/getBookInfo", s.api(s.getBookInfo))
	r.Get("/reader3/getChapterList", s.api(s.getChapterList))
	r.Post("/reader3/getChapterList", s.api(s.getChapterList))
	r.Get("/reader3/getBookContent", s.api(s.getBookContent))
	r.Post("/reader3/getBookContent", s.api(s.getBookContent))
	r.Post("/reader3/saveBookProgress", s.api(s.saveBookProgress))
	r.Get("/reader3/cover", s.api(s.getBookCover))
	r.Get("/reader3/searchBookSource", s.api(s.searchBookSource))
	r.Post("/reader3/searchBookSource", s.api(s.searchBookSource))
	r.Get("/reader3/getAvailableBookSource", s.api(s.getAvailableBookSource))
	r.Post("/reader3/getAvailableBookSource", s.api(s.getAvailableBookSource))
	r.Get("/reader3/searchBookSourceSSE", s.apiSSE(s.searchBookSourceSSE))
	r.Get("/reader3/setBookSource", s.api(s.setBookSource))
	r.Post("/reader3/setBookSource", s.api(s.setBookSource))
	r.Post("/reader3/saveBookGroupId", s.api(s.saveBookGroupId))
	r.Post("/reader3/addBookGroupMulti", s.api(s.addBookGroupMulti))
	r.Post("/reader3/removeBookGroupMulti", s.api(s.removeBookGroupMulti))
	r.Post("/reader3/importBookPreview", s.api(s.importBookPreview))
	r.Post("/reader3/refreshLocalBook", s.api(s.refreshLocalBook))
	r.Get("/reader3/getTxtTocRules", s.api(s.getTxtTocRules))
	r.Post("/reader3/getChapterListByRule", s.api(s.getChapterListByRule))
	r.Get("/reader3/getBookGroups", s.api(s.getBookGroups))
	r.Post("/reader3/saveBookGroup", s.api(s.saveBookGroup))
	r.Post("/reader3/deleteBookGroup", s.api(s.deleteBookGroup))
	r.Post("/reader3/saveBookGroupOrder", s.api(s.saveBookGroupOrder))
	r.Get("/reader3/getLocalStoreFileList", s.api(s.getLocalStoreFileList))
	r.Get("/reader3/getLocalStoreFile", s.api(s.getLocalStoreFile))
	r.Post("/reader3/deleteLocalStoreFile", s.api(s.deleteLocalStoreFile))
	r.Post("/reader3/deleteLocalStoreFileList", s.api(s.deleteLocalStoreFileList))
	r.Post("/reader3/importFromLocalPathPreview", s.api(s.importFromLocalPathPreview))
	r.Post("/reader3/uploadFileToLocalStore", s.api(s.uploadFileToLocalStore))
	r.Get("/reader3/bookSourceDebugSSE", s.apiSSE(s.bookSourceDebugSSE))
	r.Get("/reader3/cacheBookSSE", s.apiSSE(s.cacheBookSSE))
	r.Get("/reader3/getShelfBookWithCacheInfo", s.api(s.getShelfBookWithCacheInfo))
	r.Post("/reader3/deleteBookCache", s.api(s.deleteBookCache))
	r.Get("/reader3/exportBook", s.api(s.exportBook))
	r.Post("/reader3/exportBook", s.api(s.exportBook))
	r.Get("/reader3/searchBookContent", s.api(s.searchBookContent))
	r.Post("/reader3/searchBookContent", s.api(s.searchBookContent))

	// user
	r.Post("/reader3/uploadFile", s.api(s.uploadFile))
	r.Post("/reader3/deleteFile", s.api(s.deleteFile))
	r.Post("/reader3/login", s.api(s.login))
	r.Post("/reader3/logout", s.api(s.logout))
	r.Get("/reader3/getUserInfo", s.api(s.getUserInfo))
	r.Post("/reader3/saveUserConfig", s.api(s.saveUserConfig))
	r.Get("/reader3/getUserConfig", s.api(s.getUserConfig))
	r.Get("/reader3/getUserList", s.api(s.getUserList))
	r.Post("/reader3/deleteUsers", s.api(s.deleteUsers))
	r.Post("/reader3/addUser", s.api(s.addUser))
	r.Post("/reader3/resetPassword", s.api(s.resetPassword))
	r.Post("/reader3/updateUser", s.api(s.updateUser))

	// webdav
	r.Get("/reader3/getWebdavFileList", s.api(s.getWebdavFileList))
	r.Get("/reader3/getWebdavFile", s.api(s.getWebdavFile))
	r.Post("/reader3/uploadFileToWebdav", s.api(s.uploadFileToWebdav))
	r.Get("/reader3/deleteWebdavFile", s.api(s.deleteWebdavFile))
	r.Post("/reader3/deleteWebdavFile", s.api(s.deleteWebdavFile))
	r.Post("/reader3/deleteWebdavFileList", s.api(s.deleteWebdavFileList))
	r.Post("/reader3/restoreFromWebdav", s.api(s.restoreFromWebdav))
	r.Post("/reader3/backupToWebdav", s.api(s.backupToWebdav))
	// WebDAV clients use arbitrary methods (PROPFIND, MKCOL, MOVE, ...); chi's
	// Handle registers the pattern for every method.
	r.Handle("/reader3/webdav/*", http.HandlerFunc(s.webdavHandler))
	r.Handle("/reader3/webdav", http.HandlerFunc(s.webdavHandler))

	// rss
	r.Get("/reader3/getRssSources", s.api(s.getRssSources))
	r.Post("/reader3/saveRssSource", s.api(s.saveRssSource))
	r.Post("/reader3/saveRssSources", s.api(s.saveRssSources))
	r.Post("/reader3/deleteRssSource", s.api(s.deleteRssSource))
	r.Get("/reader3/getRssArticles", s.api(s.getRssArticles))
	r.Post("/reader3/getRssArticles", s.api(s.getRssArticles))
	r.Get("/reader3/getRssContent", s.api(s.getRssContent))
	r.Post("/reader3/getRssContent", s.api(s.getRssContent))

	// replace rule
	r.Get("/reader3/getReplaceRules", s.api(s.getReplaceRules))
	r.Post("/reader3/saveReplaceRule", s.api(s.saveReplaceRule))
	r.Post("/reader3/saveReplaceRules", s.api(s.saveReplaceRules))
	r.Post("/reader3/deleteReplaceRule", s.api(s.deleteReplaceRule))
	r.Post("/reader3/deleteReplaceRules", s.api(s.deleteReplaceRules))

	// bookmark
	r.Get("/reader3/getBookmarks", s.api(s.getBookmarks))
	r.Post("/reader3/saveBookmark", s.api(s.saveBookmark))
	r.Post("/reader3/saveBookmarks", s.api(s.saveBookmarks))
	r.Post("/reader3/deleteBookmark", s.api(s.deleteBookmark))
	r.Post("/reader3/deleteBookmarks", s.api(s.deleteBookmarks))

	// assets & epub (epub chapters get JS-injected on first access)
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(s.Store.Root, "assets")))))
	r.Handle("/epub/*", epubHandler(filepath.Join(s.Store.Root, "data")))

	// frontend (unchanged Vue build output)
	r.NotFound(s.webHandler().ServeHTTP)
}

func (s *Server) apiSSE(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ri := s.auth(r)
		if ri.NewCookie != "" {
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: ri.NewCookie, Path: "/", HttpOnly: true})
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, _ := w.(http.Flusher)
		rd := fn(w, r, ri)
		if rd != nil {
			writeSSE(w, rd, flusher)
		}
	}
}

func writeSSE(w http.ResponseWriter, rd *ReturnData, f http.Flusher) {
	b, _ := json.Marshal(rd)
	event := ""
	if !rd.IsSuccess {
		event = "error"
	}
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	if f != nil {
		f.Flush()
	}
}

func (s *Server) webHandler() http.Handler {
	dir := s.App.WebDir
	if dist := filepath.Join(dir, "dist"); dirExists(dist) {
		dir = dist
	}
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, filepath.Clean(r.URL.Path))
		if !strings.HasPrefix(path, filepath.Clean(dir)) {
			http.NotFound(w, r)
			return
		}
		if _, err := os.Stat(path); err != nil && !strings.HasPrefix(r.URL.Path, "/api") {
			// SPA history fallback
			index := filepath.Join(dir, "index.html")
			if _, err := os.Stat(index); err == nil {
				http.ServeFile(w, r, index)
				return
			}
		}
		fs.ServeHTTP(w, r)
	})
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Request helpers ----------------------------------------------------------

func bodyMap(r *http.Request) map[string]any {
	b, _ := io.ReadAll(r.Body)
	if len(b) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func bodyAny(r *http.Request) any {
	b, _ := io.ReadAll(r.Body)
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}

func bodyArray(r *http.Request) []any {
	v := bodyAny(r)
	if arr, ok := v.([]any); ok {
		return arr
	}
	return nil
}

func str(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intVal(m map[string]any, key string, def int) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			var i int
			if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
				return i
			}
		}
	}
	return def
}

func boolVal(m map[string]any, key string) (bool, bool) {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b, true
		}
	}
	return false, false
}

func queryOrBody(r *http.Request, m map[string]any, key string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return str(m, key)
}

func (s *Server) getSystemInfo(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return success(map[string]any{
		"fonts":       os.Getenv("reader.system.fonts"),
		"freeMemory":  fmt.Sprintf("%dM", (ms.HeapAlloc)/1024/1024),
		"totalMemory": fmt.Sprintf("%dM", ms.HeapSys/1024/1024),
		"maxMemory":   "0M",
	})
}
