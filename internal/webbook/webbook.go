package webbook

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/765362546/novel-reader-go/internal/logging"
	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/rule"
	"github.com/765362546/novel-reader-go/internal/util"
)

var logger = logging.Component("webbook")

// WebBook ports io.legado.app.model.webBook.WebBook.
type WebBook struct {
	Source *model.BookSource
}

func New(source *model.BookSource) *WebBook {
	return &WebBook{Source: source}
}

// sourceName identifies the book source in log records.
func (w *WebBook) sourceName() string {
	if w.Source == nil {
		return ""
	}
	if w.Source.BookSourceName != "" {
		return w.Source.BookSourceName
	}
	return w.Source.BookSourceUrl
}

func (w *WebBook) searchRule() *model.SearchRule {
	if w.Source.RuleSearch == nil {
		w.Source.RuleSearch = &model.SearchRule{}
	}
	return w.Source.RuleSearch
}

func (w *WebBook) exploreRule() *model.ExploreRule {
	if w.Source.RuleExplore == nil {
		w.Source.RuleExplore = &model.ExploreRule{}
	}
	return w.Source.RuleExplore
}

func (w *WebBook) infoRule() *model.BookInfoRule {
	if w.Source.RuleBookInfo == nil {
		w.Source.RuleBookInfo = &model.BookInfoRule{}
	}
	return w.Source.RuleBookInfo
}

func (w *WebBook) tocRule() *model.TocRule {
	if w.Source.RuleToc == nil {
		w.Source.RuleToc = &model.TocRule{}
	}
	return w.Source.RuleToc
}

func (w *WebBook) contentRule() *model.ContentRule {
	if w.Source.RuleContent == nil {
		w.Source.RuleContent = &model.ContentRule{}
	}
	return w.Source.RuleContent
}

func ptr(s string) *string { return &s }

func val(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (w *WebBook) headerMap() map[string]string { return nil }

func newAnalyze(source *model.BookSource, book *model.Book, chapter *model.BookChapter) *rule.Analyze {
	a := rule.NewAnalyze()
	a.Source = source
	a.Book = book
	a.Chapter = chapter
	if book != nil && book.Variable != nil && strings.TrimSpace(*book.Variable) != "" {
		_ = json.Unmarshal([]byte(*book.Variable), &a.Vars)
	}
	if chapter != nil && chapter.Variable != nil && strings.TrimSpace(*chapter.Variable) != "" {
		var m map[string]string
		if json.Unmarshal([]byte(*chapter.Variable), &m) == nil {
			for k, v := range m {
				if _, ok := a.Vars[k]; !ok {
					a.Vars[k] = v
				}
			}
		}
	}
	if a.Vars == nil {
		a.Vars = map[string]string{}
	}
	return a
}

func syncVars(book *model.Book, a *rule.Analyze) {
	if book == nil || a == nil || len(a.Vars) == 0 {
		return
	}
	b, _ := json.Marshal(a.Vars)
	s := string(b)
	book.Variable = &s
}

func (w *WebBook) fetcher(a *rule.Analyze) *rule.Fetcher {
	f := rule.NewFetcher(w.Source, a)
	f.Header = map[string]string{}
	if w.Source.Header != nil && strings.TrimSpace(*w.Source.Header) != "" {
		var hm map[string]any
		if json.Unmarshal([]byte(*w.Source.Header), &hm) == nil {
			for k, v := range hm {
				f.Header[k] = fmt.Sprint(v)
			}
		}
	}
	a.Fetch = func(u string) (string, error) {
		resp, err := f.Get(u)
		if err != nil {
			return "", err
		}
		return resp.Body, nil
	}
	return f
}

func (w *WebBook) loginCheck(a *rule.Analyze, body string) string {
	if w.Source.LoginCheckJs != nil && strings.TrimSpace(*w.Source.LoginCheckJs) != "" {
		if v, ok := a.EvalJS(*w.Source.LoginCheckJs, body).(string); ok && v != "" {
			return v
		}
	}
	return body
}

// SearchBook performs a search using the source search rule.
func (w *WebBook) Search(key string, page int) ([]*model.SearchBook, error) {
	if w.Source.SearchUrl == nil || strings.TrimSpace(*w.Source.SearchUrl) == "" {
		return nil, nil
	}
	variable := model.NewSearchBook()
	a := newAnalyze(w.Source, nil, nil)
	a.Book = nil
	a.Vars["key"] = key
	a.Vars["page"] = fmt.Sprint(page)
	f := w.fetcher(a)
	a.SetContent("", w.Source.BookSourceUrl)
	// URL template may need JS evaluation; AnalyzeURL does that via evalInline.
	resp, err := f.Get(*w.Source.SearchUrl)
	if err != nil {
		logger.Warn("search request failed", "source", w.sourceName(), "key", key, "page", page, "error", err)
		return nil, err
	}
	body := w.loginCheck(a, resp.Body)
	start := time.Now()
	list, err := w.analyzeBookList(body, a, resp.URL, variable, true, resp.URL)
	if err != nil {
		logger.Warn("search parse failed", "source", w.sourceName(), "key", key, "page", page, "error", err)
		return nil, err
	}
	logger.Debug("search completed",
		"source", w.sourceName(),
		"key", key,
		"page", page,
		"count", len(list),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return list, nil
}

// ExploreBook performs discovery.
func (w *WebBook) Explore(urlStr string, page int) ([]*model.SearchBook, error) {
	variable := model.NewSearchBook()
	a := newAnalyze(w.Source, nil, nil)
	a.Vars["page"] = fmt.Sprint(page)
	f := w.fetcher(a)
	a.SetContent("", w.Source.BookSourceUrl)
	resp, err := f.Get(urlStr)
	if err != nil {
		logger.Warn("explore request failed", "source", w.sourceName(), "url", urlStr, "page", page, "error", err)
		return nil, err
	}
	body := w.loginCheck(a, resp.Body)
	return w.analyzeBookList(body, a, resp.URL, variable, false, resp.URL)
}

func (w *WebBook) analyzeBookList(body string, baseAnalyze *rule.Analyze, redirectURL string, variableBook *model.SearchBook, isSearch bool, requestURL string) ([]*model.SearchBook, error) {
	if body == "" {
		return nil, fmt.Errorf("error_get_web_content")
	}
	result := []*model.SearchBook{}
	a := newAnalyze(w.Source, nil, nil)
	a.Book = nil
	// Bind java.ajax for JS rules evaluated on this response.
	w.fetcher(a)
	a.SetContent(body, redirectURL)
	a.RedirectUrl = redirectURL

	if w.Source.BookUrlPattern != nil && strings.TrimSpace(*w.Source.BookUrlPattern) != "" {
		if re, err := regexp.Compile("^(?:" + *w.Source.BookUrlPattern + ")$"); err == nil && re.MatchString(redirectURL) {
			if sb := w.getInfoItem(body, a, redirectURL, requestURL, variableBook.Variable); sb != nil {
				return []*model.SearchBook{sb}, nil
			}
			return result, nil
		}
	}

	var listRule *model.SearchRule
	if isSearch {
		listRule = w.searchRule()
	} else if w.exploreRule().BookList == nil || strings.TrimSpace(*w.exploreRule().BookList) == "" {
		listRule = w.searchRule()
	} else {
		listRule = &model.SearchRule{
			BookList: w.exploreRule().BookList, Name: w.exploreRule().Name,
			Author: w.exploreRule().Author, Intro: w.exploreRule().Intro,
			Kind: w.exploreRule().Kind, BookUrl: w.exploreRule().BookUrl,
			CoverUrl: w.exploreRule().CoverUrl, LastChapter: w.exploreRule().LastChapter,
			WordCount: w.exploreRule().WordCount, UpdateTime: w.exploreRule().UpdateTime,
		}
	}

	ruleList := val(listRule.BookList)
	reverse := false
	if strings.HasPrefix(ruleList, "-") {
		reverse = true
		ruleList = strings.TrimPrefix(ruleList, "-")
	}
	if strings.HasPrefix(ruleList, "+") {
		ruleList = strings.TrimPrefix(ruleList, "+")
	}
	elements := a.GetElements(ruleList)
	if len(elements) == 0 && (w.Source.BookUrlPattern == nil || *w.Source.BookUrlPattern == "") {
		if sb := w.getInfoItem(body, a, redirectURL, requestURL, variableBook.Variable); sb != nil {
			result = append(result, sb)
		}
		return result, nil
	}
	for _, item := range elements {
		sb := w.getSearchItem(item, a, redirectURL, requestURL, variableBook.Variable, listRule)
		if sb != nil {
			result = append(result, sb)
		}
	}
	if reverse {
		for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
			result[i], result[j] = result[j], result[i]
		}
	}
	return result, nil
}

func (w *WebBook) getInfoItem(body string, a *rule.Analyze, baseURL, requestURL string, variable *string) *model.SearchBook {
	book := model.NewBook()
	if variable != nil {
		book.Variable = variable
	}
	book.BookUrl = requestURL
	book.Origin = w.Source.BookSourceUrl
	book.OriginName = w.Source.BookSourceName
	book.OriginOrder = w.Source.CustomOrder
	book.Type = w.Source.BookSourceType
	a.Book = book
	w.analyzeBookInfo(book, body, a, baseURL, baseURL, false)
	if strings.TrimSpace(book.Name) == "" {
		return nil
	}
	return bookToSearchBook(book)
}

func (w *WebBook) getSearchItem(item any, a *rule.Analyze, baseURL, requestURL string, variable *string, listRule *model.SearchRule) *model.SearchBook {
	sb := model.NewSearchBook()
	if variable != nil {
		sb.Variable = variable
	}
	sb.Origin = w.Source.BookSourceUrl
	sb.OriginName = w.Source.BookSourceName
	sb.Type = w.Source.BookSourceType
	sb.OriginOrder = w.Source.CustomOrder
	a.Book = nil
	a.SetContent(item, baseURL)

	sb.Name = util.FormatBookName(a.GetString(val(listRule.Name)))
	if sb.Name == "" {
		return nil
	}
	sb.Author = util.FormatBookAuthor(a.GetString(val(listRule.Author)))
	if kindList := a.GetStringList(val(listRule.Kind)); len(kindList) > 0 {
		sb.Kind = ptr(strings.Join(kindList, ","))
	}
	if wc := util.WordCountFormat(a.GetString(val(listRule.WordCount))); wc != "" {
		sb.WordCount = ptr(wc)
	}
	if lc := a.GetString(val(listRule.LastChapter)); lc != "" {
		sb.LatestChapterTitle = ptr(lc)
	}
	if intro := util.HTMLFormat(a.GetString(val(listRule.Intro))); intro != "" {
		sb.Intro = ptr(intro)
	}
	if cover := a.GetString(val(listRule.CoverUrl)); cover != "" {
		c := util.AbsoluteURL(baseURL, cover)
		sb.CoverUrl = &c
	}
	sb.BookUrl = a.GetStringURL(val(listRule.BookUrl))
	if sb.BookUrl == "" || sb.BookUrl == baseURL {
		sb.BookUrl = baseURL
	}
	sb.Time = time.Now().UnixMilli()
	return sb
}

func bookToSearchBook(book *model.Book) *model.SearchBook {
	return &model.SearchBook{
		BookUrl: book.BookUrl, Origin: book.Origin, OriginName: book.OriginName,
		Type: book.Type, Name: book.Name, Author: book.Author, Kind: book.Kind,
		CoverUrl: book.CoverUrl, Intro: book.Intro, WordCount: book.WordCount,
		LatestChapterTitle: book.LatestChapterTitle, TocUrl: book.TocUrl,
		Time: time.Now().UnixMilli(), Variable: book.Variable, OriginOrder: book.OriginOrder,
	}
}

// BookInfo fetches and parses book details.
func (w *WebBook) BookInfo(book *model.Book, canReName bool) (*model.Book, error) {
	book.Type = w.Source.BookSourceType
	a := newAnalyze(w.Source, book, nil)
	f := w.fetcher(a)
	a.SetContent("", w.Source.BookSourceUrl)
	resp, err := f.Get(book.BookUrl)
	if err != nil {
		logger.Warn("book info request failed",
			"source", w.sourceName(), "book", book.Name, "url", book.BookUrl, "error", err)
		return nil, err
	}
	body := w.loginCheck(a, resp.Body)
	w.analyzeBookInfo(book, body, a, book.BookUrl, resp.URL, canReName)
	syncVars(book, a)
	logger.Debug("book info fetched",
		"source", w.sourceName(), "book", book.Name, "author", book.Author, "tocUrl", book.TocUrl)
	return book, nil
}

func (w *WebBook) analyzeBookInfo(book *model.Book, body string, a *rule.Analyze, baseURL, redirectURL string, canReName bool) {
	a.SetContent(body, baseURL)
	a.RedirectUrl = redirectURL
	infoRule := w.infoRule()
	if infoRule.Init != nil && *infoRule.Init != "" {
		if els := a.GetElements(*infoRule.Init); len(els) > 0 {
			a.SetContent(els[0], baseURL)
		}
	}
	mCanReName := canReName && infoRule.CanReName != nil && *infoRule.CanReName != ""
	if name := util.FormatBookName(a.GetString(val(infoRule.Name))); name != "" && (mCanReName || book.Name == "") {
		book.Name = name
	}
	if author := util.FormatBookAuthor(a.GetString(val(infoRule.Author))); author != "" && (mCanReName || book.Author == "") {
		book.Author = author
	}
	if kind := a.GetStringList(val(infoRule.Kind)); len(kind) > 0 {
		book.Kind = ptr(strings.Join(kind, ","))
	}
	if wc := util.WordCountFormat(a.GetString(val(infoRule.WordCount))); wc != "" {
		book.WordCount = ptr(wc)
	}
	if lc := a.GetString(val(infoRule.LastChapter)); lc != "" {
		book.LatestChapterTitle = ptr(lc)
	}
	if intro := util.HTMLFormat(a.GetString(val(infoRule.Intro))); intro != "" {
		book.Intro = ptr(intro)
	}
	if cover := a.GetString(val(infoRule.CoverUrl)); cover != "" {
		c := util.AbsoluteURL(redirectURL, cover)
		book.CoverUrl = &c
	}
	book.TocUrl = a.GetStringURL(val(infoRule.TocUrl))
	if book.TocUrl == "" {
		book.TocUrl = baseURL
	}
}

// ChapterList fetches the table of contents.
func (w *WebBook) ChapterList(book *model.Book) ([]*model.BookChapter, error) {
	tocURL := strings.TrimSpace(book.TocUrl)
	if tocURL == "" {
		tocURL = book.BookUrl
	}
	chapters, err := w.chapterListAt(book, tocURL)
	if err == nil && len(chapters) > 0 {
		return chapters, nil
	}
	// The toc url may point at a book detail page (or be missing entirely when
	// the reader is opened straight from search results). Re-fetch book info so
	// its toc rule can reveal the real catalogue url, then retry once.
	if w.infoRule().TocUrl == nil {
		return chapters, err
	}
	if _, infoErr := w.BookInfo(book, false); infoErr != nil {
		return chapters, err
	}
	fixed := strings.TrimSpace(book.TocUrl)
	if fixed == "" || fixed == tocURL {
		return chapters, err
	}
	logger.Debug("chapter list retry with toc url from book info",
		"source", w.sourceName(), "book", book.Name, "tocUrl", fixed)
	if retried, retryErr := w.chapterListAt(book, fixed); retryErr == nil && len(retried) > 0 {
		return retried, nil
	}
	return chapters, err
}

func (w *WebBook) chapterListAt(book *model.Book, tocURL string) ([]*model.BookChapter, error) {
	book.Type = w.Source.BookSourceType
	a := newAnalyze(w.Source, book, nil)
	f := w.fetcher(a)
	a.SetContent("", book.BookUrl)
	start := time.Now()
	resp, err := f.Get(tocURL)
	if err != nil {
		logger.Warn("chapter list request failed",
			"source", w.sourceName(), "book", book.Name, "tocUrl", tocURL, "error", err)
		return nil, err
	}
	body := w.loginCheck(a, resp.Body)
	tocRule := w.tocRule()
	listRule := val(tocRule.ChapterList)
	reverse := false
	if strings.HasPrefix(listRule, "-") {
		reverse = true
		listRule = strings.TrimPrefix(listRule, "-")
	}
	if strings.HasPrefix(listRule, "+") {
		listRule = strings.TrimPrefix(listRule, "+")
	}

	chapters, nextURLs, err := w.analyzeChapterList(book, tocURL, resp.URL, body, listRule)
	if err != nil {
		return nil, err
	}
	// Follow sequential next pages, bounded.
	visited := map[string]bool{resp.URL: true}
	for i := 0; i < 50 && len(nextURLs) > 0; i++ {
		if len(nextURLs) > 1 {
			break
		}
		next := nextURLs[0]
		if next == "" || visited[next] {
			break
		}
		visited[next] = true
		na := newAnalyze(w.Source, book, nil)
		nf := w.fetcher(na)
		na.SetContent("", book.BookUrl)
		nresp, err := nf.Get(next)
		if err != nil {
			break
		}
		nbody := w.loginCheck(na, nresp.Body)
		nextChapters, nexts, _ := w.analyzeChapterList(book, next, nresp.URL, nbody, listRule)
		chapters = append(chapters, nextChapters...)
		nextURLs = nexts
	}
	if len(chapters) == 0 {
		logger.Warn("chapter list empty",
			"source", w.sourceName(), "book", book.Name, "tocUrl", tocURL, "listRule", listRule)
		return nil, fmt.Errorf("目录为空")
	}
	if !reverse {
		reverseChapters(chapters)
	}
	chapters = dedupeChapters(chapters)
	reverseChapters(chapters)
	for i, c := range chapters {
		c.Index = i
	}
	if len(chapters) > 0 {
		last := chapters[len(chapters)-1].Title
		book.LatestChapterTitle = &last
	}
	book.TotalChapterNum = len(chapters)
	book.LatestChapterTime = time.Now().UnixMilli()
	syncVars(book, a)
	logger.Debug("chapter list fetched",
		"source", w.sourceName(),
		"book", book.Name,
		"count", len(chapters),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return chapters, nil
}

func reverseChapters(list []*model.BookChapter) {
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
}

func dedupeChapters(list []*model.BookChapter) []*model.BookChapter {
	seen := map[string]bool{}
	out := make([]*model.BookChapter, 0, len(list))
	for _, c := range list {
		key := c.Title + "\x00" + c.URL
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

func (w *WebBook) analyzeChapterList(book *model.Book, baseURL, redirectURL, body, listRule string) ([]*model.BookChapter, []string, error) {
	tocRule := w.tocRule()
	a := newAnalyze(w.Source, book, nil)
	// Bind java.ajax for JS table-of-contents rules.
	w.fetcher(a)
	a.SetContent(body, baseURL)
	a.RedirectUrl = redirectURL
	chapters := []*model.BookChapter{}
	elements := a.GetElements(listRule)
	var nextURLs []string
	if tocRule.NextTocUrl != nil && *tocRule.NextTocUrl != "" {
		for _, u := range a.GetStringListURL(*tocRule.NextTocUrl) {
			if u != redirectURL {
				nextURLs = append(nextURLs, u)
			}
		}
	}
	for index, item := range elements {
		a.SetContent(item, baseURL)
		chapter := &model.BookChapter{BookUrl: book.BookUrl, BaseURL: redirectURL}
		a.Chapter = chapter
		chapter.Title = a.GetString(val(tocRule.ChapterName))
		chapter.URL = a.GetString0(val(tocRule.ChapterUrl))
		if chapter.URL != "" {
			chapter.URL = util.AbsoluteURL(redirectURL, chapter.URL)
		}
		if tag := a.GetString(val(tocRule.UpdateTime)); tag != "" {
			chapter.Tag = &tag
		}
		if isVolume := a.GetString(val(tocRule.IsVolume)); isTrue(isVolume) {
			chapter.IsVolume = true
		}
		if chapter.URL == "" {
			if chapter.IsVolume {
				chapter.URL = fmt.Sprintf("%s%d", chapter.Title, index)
			} else {
				chapter.URL = baseURL
			}
		}
		if chapter.Title != "" {
			if isVip := a.GetString(val(tocRule.IsVip)); isTrue(isVip) {
				chapter.Title = "🔒" + chapter.Title
			}
			chapters = append(chapters, chapter)
		}
	}
	return chapters, nextURLs, nil
}

func isTrue(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return false
	}
	return !(v == "false" || v == "no" || v == "not" || v == "0")
}

// Content fetches a chapter's text.
func (w *WebBook) Content(book *model.Book, chapter *model.BookChapter, nextChapterURL string) (string, error) {
	contentRule := w.contentRule()
	if contentRule.Content == nil || strings.TrimSpace(*contentRule.Content) == "" {
		return chapter.URL, nil
	}
	if chapter.IsVolume && strings.HasPrefix(chapter.URL, chapter.Title) {
		return val(chapter.Tag), nil
	}
	base := chapter.BaseURL
	if base == "" {
		base = book.TocUrl
	}
	a := newAnalyze(w.Source, book, chapter)
	f := w.fetcher(a)
	a.SetContent("", base)
	a.NextChapterURL = nextChapterURL
	chapterURL := chapter.URL
	if !strings.HasPrefix(chapterURL, "http") && base != "" {
		chapterURL = util.AbsoluteURL(base, chapterURL)
	}
	resp, err := f.Get(chapterURL)
	if err != nil {
		logger.Warn("chapter content request failed",
			"source", w.sourceName(), "book", book.Name, "chapter", chapter.Title, "url", chapterURL, "error", err)
		return "", err
	}
	body := w.loginCheck(a, resp.Body)
	if contentRule.WebJs != nil && strings.TrimSpace(*contentRule.WebJs) != "" {
		sub := newAnalyze(w.Source, book, chapter)
		sub.Source = w.Source
		sub.SetContent(body, base)
		if v, ok := sub.EvalJS(*contentRule.WebJs, body).(string); ok && v != "" {
			body = v
		}
	}
	content, nextURLs := w.analyzeContent(book, chapter, base, resp.URL, body, nextChapterURL)
	visited := map[string]bool{resp.URL: true}
	for i := 0; i < 50 && len(nextURLs) == 1; i++ {
		next := nextURLs[0]
		if next == "" || visited[next] {
			break
		}
		if nextChapterURL != "" && util.AbsoluteURL(resp.URL, next) == util.AbsoluteURL(resp.URL, nextChapterURL) {
			break
		}
		visited[next] = true
		na := newAnalyze(w.Source, book, chapter)
		nf := w.fetcher(na)
		na.SetContent("", base)
		nresp, err := nf.Get(next)
		if err != nil {
			break
		}
		ncontent, nexts := w.analyzeContent(book, chapter, next, nresp.URL, nresp.Body, nextChapterURL)
		content += "\n" + ncontent
		nextURLs = nexts
	}
	if contentRule.ReplaceRegex != nil && *contentRule.ReplaceRegex != "" {
		sub := newAnalyze(w.Source, book, chapter)
		sub.SetContent(content, resp.URL)
		content = sub.GetString(*contentRule.ReplaceRegex)
	}
	syncVars(book, a)
	return content, nil
}

func (w *WebBook) analyzeContent(book *model.Book, chapter *model.BookChapter, baseURL, redirectURL, body, nextChapterURL string) (string, []string) {
	contentRule := w.contentRule()
	a := newAnalyze(w.Source, book, chapter)
	// Bind java.ajax for JS content rules.
	w.fetcher(a)
	a.SetContent(body, baseURL)
	a.RedirectUrl = redirectURL
	a.NextChapterURL = nextChapterURL
	content := a.GetString(val(contentRule.Content))
	content = util.HTMLFormatKeepImg(content, redirectURL)
	var nextURLs []string
	if contentRule.NextContentUrl != nil && *contentRule.NextContentUrl != "" {
		nextURLs = a.GetStringListURL(*contentRule.NextContentUrl)
	}
	syncVars(book, a)
	return content, nextURLs
}
