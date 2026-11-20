package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/rule"
)

func (s *Server) saveBookSource(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	raw := json.RawMessage(bodyBytes(r))
	var src model.BookSource
	if len(raw) == 0 || json.Unmarshal(raw, &src) != nil || src.BookSourceUrl == "" {
		return failure("参数错误")
	}
	ns := ri.managerNameSpace()
	list := s.loadBookSources(ns)
	if list == nil {
		list = []json.RawMessage{}
	}
	idx := containsSource(list, src.BookSourceUrl)
	if idx >= 0 {
		list = replaceAt(list, idx, raw)
	} else {
		list = append(list, raw)
	}
	if err := s.saveArray(ns, "bookSource", list); err != nil {
		logger.Error("save book source failed", "namespace", ns, "source", src.BookSourceName, "error", err)
	}
	logger.Debug("book source saved", "namespace", ns, "source", src.BookSourceName, "url", src.BookSourceUrl)
	return success("")
}

func (s *Server) saveBookSources(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var raws []json.RawMessage
	if json.Unmarshal(bodyBytes(r), &raws) != nil {
		return failure("参数错误")
	}
	ns := ri.managerNameSpace()
	list := s.loadBookSources(ns)
	if list == nil {
		list = []json.RawMessage{}
	}
	for _, raw := range raws {
		var src model.BookSource
		if json.Unmarshal(raw, &src) != nil {
			continue
		}
		idx := containsSource(list, src.BookSourceUrl)
		if idx >= 0 {
			list = replaceAt(list, idx, raw)
		} else {
			list = append(list, raw)
		}
	}
	if err := s.saveArray(ns, "bookSource", list); err != nil {
		logger.Error("save book sources failed", "namespace", ns, "count", len(raws), "error", err)
	}
	logger.Info("book sources imported", "namespace", ns, "received", len(raws), "total", len(list))
	return success("")
}

func (s *Server) getBookSource(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	body := bodyMap(r)
	url := queryOrBody(r, body, "bookSourceUrl")
	if url == "" {
		return failure("书源链接不能为空")
	}
	list := s.loadBookSources(ri.managerNameSpace())
	idx := containsSource(list, url)
	if idx < 0 {
		return failure("书源信息不存在")
	}
	return success(list[idx])
}

func (s *Server) getBookSources(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	body := bodyMap(r)
	simple := 0
	if v := queryOrBody(r, body, "simple"); v != "" {
		fmt.Sscanf(v, "%d", &simple)
	}
	list := s.loadBookSources(ri.managerNameSpace())
	if list == nil {
		return success([]any{})
	}
	if simple > 0 {
		out := []map[string]any{}
		for _, raw := range list {
			var src model.BookSource
			if json.Unmarshal(raw, &src) != nil {
				continue
			}
			out = append(out, map[string]any{
				"bookSourceGroup": src.BookSourceGroup,
				"bookSourceName":  src.BookSourceName,
				"bookSourceUrl":   src.BookSourceUrl,
				"exploreUrl":      src.ExploreUrl,
			})
		}
		return success(out)
	}
	return success(list)
}

func (s *Server) deleteBookSource(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var src model.BookSource
	if json.Unmarshal(bodyBytes(r), &src) != nil {
		return failure("参数错误")
	}
	ns := ri.managerNameSpace()
	list := s.loadBookSources(ns)
	idx := containsSource(list, src.BookSourceUrl)
	if idx >= 0 {
		list = removeAt(list, idx)
	}
	_ = s.saveArray(ns, "bookSource", list)
	rule.CloseSourceClient(src.BookSourceUrl)
	return success("")
}

func (s *Server) deleteBookSources(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var raws []json.RawMessage
	if json.Unmarshal(bodyBytes(r), &raws) != nil {
		return failure("参数错误")
	}
	ns := ri.managerNameSpace()
	list := s.loadBookSources(ns)
	if list == nil {
		list = []json.RawMessage{}
	}
	for _, raw := range raws {
		var src model.BookSource
		if json.Unmarshal(raw, &src) != nil {
			continue
		}
		if idx := containsSource(list, src.BookSourceUrl); idx >= 0 {
			list = removeAt(list, idx)
			rule.CloseSourceClient(src.BookSourceUrl)
		}
	}
	_ = s.saveArray(ns, "bookSource", list)
	return success("")
}

func (s *Server) deleteAllBookSources(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	_ = s.saveArray(ri.managerNameSpace(), "bookSource", []json.RawMessage{})
	rule.CloseAllSourceClients()
	return success("")
}

func (s *Server) setAsDefaultBookSources(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if !s.managerOK(r, ri) {
		return failureSecureKey()
	}
	username := str(bodyMap(r), "username")
	list := s.loadArray(username, "bookSource")
	if list == nil {
		return failure("用户书源不存在")
	}
	_ = s.saveArray("default", "bookSource", list)
	return success("设置默认书源成功")
}

func (s *Server) readSourceFile(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		return failure("请上传文件")
	}
	var contents []string
	for _, fhs := range r.MultipartForm.File {
		for _, fh := range fhs {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			b, _ := io.ReadAll(f)
			f.Close()
			contents = append(contents, string(b))
		}
	}
	if len(contents) == 0 {
		return failure("请上传文件")
	}
	return success(contents)
}

func (s *Server) readRemoteSourceFile(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	body := bodyMap(r)
	url := queryOrBody(r, body, "url")
	if url == "" {
		return failure("请输入远程书源链接")
	}
	resp, err := http.Get(url)
	if err != nil {
		return failure("远程书源链接错误")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || len(b) == 0 {
		return failure("远程书源链接错误")
	}
	return success([]string{string(b)})
}

func (s *Server) deleteUserBookSource(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if !s.managerOK(r, ri) {
		return failureSecureKey()
	}
	var users []string
	_ = json.Unmarshal(bodyBytes(r), &users)
	for _, u := range users {
		s.deleteUserBookSourceFile(u)
	}
	return success("删除书源成功")
}

func (s *Server) deleteBookSourcesFile(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	s.deleteUserBookSourceFile(ri.managerNameSpace())
	return success("")
}

func bodyBytes(r *http.Request) []byte {
	b, _ := io.ReadAll(r.Body)
	return b
}

func trimBOM(s string) string {
	return strings.TrimPrefix(s, "\ufeff")
}
