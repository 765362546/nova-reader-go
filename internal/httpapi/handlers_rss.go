package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/rss"
)

func (s *Server) loadRssSources(ns string) []*model.RssSource {
	raw, err := s.Store.Get("data", ns, "rssSources")
	if err != nil {
		return nil
	}
	var list []*model.RssSource
	_ = json.Unmarshal(raw, &list)
	return list
}

func (s *Server) saveRssSourcesFile(ns string, list []*model.RssSource) error {
	if list == nil {
		list = []*model.RssSource{}
	}
	return s.Store.SavePretty(list, "data", ns, "rssSources")
}

func (s *Server) rssSourceByURL(ns, url string) *model.RssSource {
	if url == "" {
		return nil
	}
	for _, rs := range s.loadRssSources(ns) {
		if rs.SourceUrl == url {
			return rs
		}
	}
	return nil
}

func (s *Server) getRssSources(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	list := s.loadRssSources(ri.Namespace)
	if list == nil {
		list = []*model.RssSource{}
	}
	return success(list)
}

func (s *Server) saveRssSource(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	m := bodyMap(r)
	var src model.RssSource
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &src)
	return s.upsertRssSource(ri.Namespace, &src)
}

func (s *Server) saveRssSources(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	arr := bodyArray(r)
	for _, v := range arr {
		var src model.RssSource
		b, _ := json.Marshal(v)
		if json.Unmarshal(b, &src) != nil {
			continue
		}
		if rd := s.upsertRssSource(ri.Namespace, &src); rd != nil && !rd.IsSuccess {
			continue
		}
	}
	return success("")
}

func (s *Server) upsertRssSource(ns string, src *model.RssSource) *ReturnData {
	if src.SourceUrl == "" {
		return failure("RSS链接不能为空")
	}
	if src.SourceName == "" {
		return failure("RSS名称不能为空")
	}
	list := s.loadRssSources(ns)
	idx := -1
	for i, old := range list {
		if old.SourceUrl == src.SourceUrl {
			idx = i
			break
		}
	}
	if idx >= 0 {
		list[idx] = src
	} else {
		list = append(list, src)
	}
	if err := s.saveRssSourcesFile(ns, list); err != nil {
		return failure(err.Error())
	}
	return success("")
}

func (s *Server) deleteRssSource(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
    }
	m := bodyMap(r)
	var src model.RssSource
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &src)
	list := s.loadRssSources(ri.Namespace)
	for i, old := range list {
		if old.SourceUrl == src.SourceUrl {
			list = removeAtIdx(list, i)
			break
		}
	}
	_ = s.saveRssSourcesFile(ri.Namespace, list)
	return success("")
}

func (s *Server) getRssArticles(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == "POST" {
		m = bodyMap(r)
	}
	sourceURL := queryOrBody(r, m, "sourceUrl")
	sortName := queryOrBody(r, m, "sortName")
	sortURL := queryOrBody(r, m, "sortUrl")
	page := queryIntOrBody(r, m, "page", 1)
	if sourceURL == "" {
		return failure("RSS源链接不能为空")
	}
	if sortURL == "" {
		sortURL = sourceURL
	}
	src := s.rssSourceByURL(ri.Namespace, sourceURL)
	if src == nil {
		return failure("RSS源不存在")
	}
	list, next, err := rss.Articles(sortName, sortURL, src, page)
	if err != nil {
		logger.Warn("fetch rss articles failed",
			"namespace", ri.Namespace, "source", sourceURL, "sortName", sortName, "sortUrl", sortURL, "page", page, "error", err)
		return failure(err.Error())
	}
	return success(map[string]any{"first": list, "second": next})
}

func (s *Server) getRssContent(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var m map[string]any
	if r.Method == "POST" {
		m = bodyMap(r)
	}
	sourceURL := queryOrBody(r, m, "sourceUrl")
	link := queryOrBody(r, m, "link")
	origin := queryOrBody(r, m, "origin")
	if sourceURL == "" {
		return failure("RSS链接不能为空")
	}
	if link == "" {
		return failure("RSS文章链接不能为空")
	}
	if origin == "" {
		return failure("RSS文章来源不能为空")
	}
	src := s.rssSourceByURL(ri.Namespace, sourceURL)
	if src == nil {
		return failure("RSS源不存在")
    }
	article := &model.RssArticle{Origin: origin, Link: link}
	content := ""
	if src.RuleContent != nil && *src.RuleContent != "" {
		var err error
		content, err = rss.Content(article, *src.RuleContent, src)
		if err != nil {
			logger.Warn("fetch rss content failed",
				"namespace", ri.Namespace, "source", sourceURL, "link", link, "error", err)
			return failure(err.Error())
		}
	}
	return success(content)
}
