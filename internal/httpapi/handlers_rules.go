package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/765362546/novel-reader-go/internal/model"
)

// Replace rules ------------------------------------------------------------

func (s *Server) getReplaceRules(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	list := s.loadArray(ri.managerNameSpace(), "replaceRule")
	if list == nil {
		return success([]any{})
	}
	return success(list)
}

func (s *Server) saveReplaceRule(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	raw := json.RawMessage(bodyBytes(r))
	var rule model.ReplaceRule
	if json.Unmarshal(raw, &rule) != nil {
		return failure("参数错误")
	}
	if rule.Name == "" {
		return failure("名称不能为空")
	}
	if rule.Pattern == "" {
		return failure("替换规则不能为空")
	}
	ns := ri.managerNameSpace()
	list := s.loadArray(ns, "replaceRule")
	if list == nil {
		list = []json.RawMessage{}
	}
	found := -1
	for i, item := range list {
		var x model.ReplaceRule
		if json.Unmarshal(item, &x) == nil && x.Name == rule.Name {
			found = i
			break
		}
	}
	if found >= 0 {
		list = replaceAt(list, found, raw)
	} else {
		list = append(list, raw)
	}
	_ = s.saveArray(ns, "replaceRule", list)
	return success("")
}

func (s *Server) saveReplaceRules(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var raws []json.RawMessage
	if json.Unmarshal(bodyBytes(r), &raws) != nil {
		return failure("参数错误")
	}
	ns := ri.managerNameSpace()
	list := s.loadArray(ns, "replaceRule")
	if list == nil {
		list = []json.RawMessage{}
	}
	for _, raw := range raws {
		var rule model.ReplaceRule
		if json.Unmarshal(raw, &rule) != nil || rule.Name == "" || rule.Pattern == "" {
			continue
		}
		found := -1
		for i, item := range list {
			var x model.ReplaceRule
			if json.Unmarshal(item, &x) == nil && x.Name == rule.Name {
				found = i
				break
			}
		}
		if found >= 0 {
			list = replaceAt(list, found, raw)
		} else {
			list = append(list, raw)
		}
	}
	_ = s.saveArray(ns, "replaceRule", list)
	return success("")
}

func (s *Server) deleteReplaceRule(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var rule model.ReplaceRule
	_ = json.Unmarshal(bodyBytes(r), &rule)
	ns := ri.managerNameSpace()
	list := s.loadArray(ns, "replaceRule")
	for i, item := range list {
		var x model.ReplaceRule
		if json.Unmarshal(item, &x) == nil && x.Name == rule.Name {
			list = removeAt(list, i)
			break
		}
	}
	_ = s.saveArray(ns, "replaceRule", list)
	return success("")
}

func (s *Server) deleteReplaceRules(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var raws []json.RawMessage
	_ = json.Unmarshal(bodyBytes(r), &raws)
	ns := ri.managerNameSpace()
	list := s.loadArray(ns, "replaceRule")
	for _, raw := range raws {
		var rule model.ReplaceRule
		if json.Unmarshal(raw, &rule) != nil {
			continue
		}
		for i, item := range list {
			var x model.ReplaceRule
			if json.Unmarshal(item, &x) == nil && x.Name == rule.Name {
				list = removeAt(list, i)
				break
			}
		}
	}
	_ = s.saveArray(ns, "replaceRule", list)
	return success("")
}

// Bookmarks ---------------------------------------------------------------

func (s *Server) getBookmarks(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	list := s.loadArray(ri.managerNameSpace(), "bookmark")
	if list == nil {
		return success([]any{})
	}
	return success(list)
}

func bookmarkKey(b *model.Bookmark) string { return b.BookName + "\x00" + b.BookAuthor }

func (s *Server) saveBookmark(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	raw := json.RawMessage(bodyBytes(r))
	var bookmark model.Bookmark
	if json.Unmarshal(raw, &bookmark) != nil {
		return failure("参数错误")
	}
	if bookmark.BookName == "" && bookmark.BookAuthor == "" {
		return failure("书籍信息错误")
	}
	ns := ri.managerNameSpace()
	list := s.loadArray(ns, "bookmark")
	if list == nil {
		list = []json.RawMessage{}
	}
	found := -1
	key := bookmarkKey(&bookmark)
	for i, item := range list {
		var x model.Bookmark
		if json.Unmarshal(item, &x) == nil && bookmarkKey(&x) == key {
			found = i
			break
		}
	}
	if found >= 0 {
		list = replaceAt(list, found, raw)
	} else {
		list = append(list, raw)
	}
	_ = s.saveArray(ns, "bookmark", list)
	return success("")
}

func (s *Server) saveBookmarks(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var raws []json.RawMessage
	if json.Unmarshal(bodyBytes(r), &raws) != nil {
		return failure("参数错误")
	}
	ns := ri.managerNameSpace()
	list := s.loadArray(ns, "bookmark")
	if list == nil {
		list = []json.RawMessage{}
	}
	for _, raw := range raws {
		var b model.Bookmark
		if json.Unmarshal(raw, &b) != nil || (b.BookName == "" && b.BookAuthor == "") {
			continue
		}
		key := bookmarkKey(&b)
		found := -1
		for i, item := range list {
			var x model.Bookmark
			if json.Unmarshal(item, &x) == nil && bookmarkKey(&x) == key {
				found = i
				break
			}
		}
		if found >= 0 {
			list = replaceAt(list, found, raw)
		} else {
			list = append(list, raw)
		}
	}
	_ = s.saveArray(ns, "bookmark", list)
	return success("")
}

func (s *Server) deleteBookmark(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var b model.Bookmark
	_ = json.Unmarshal(bodyBytes(r), &b)
	ns := ri.managerNameSpace()
	list := s.loadArray(ns, "bookmark")
	key := bookmarkKey(&b)
	for i, item := range list {
		var x model.Bookmark
		if json.Unmarshal(item, &x) == nil && bookmarkKey(&x) == key {
			list = removeAt(list, i)
			break
		}
	}
	_ = s.saveArray(ns, "bookmark", list)
	return success("")
}

func (s *Server) deleteBookmarks(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var raws []json.RawMessage
	_ = json.Unmarshal(bodyBytes(r), &raws)
	ns := ri.managerNameSpace()
	list := s.loadArray(ns, "bookmark")
	for _, raw := range raws {
		var b model.Bookmark
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		key := bookmarkKey(&b)
		for i, item := range list {
			var x model.Bookmark
			if json.Unmarshal(item, &x) == nil && bookmarkKey(&x) == key {
				list = removeAt(list, i)
				break
			}
		}
	}
	_ = s.saveArray(ns, "bookmark", list)
	return success("")
}
