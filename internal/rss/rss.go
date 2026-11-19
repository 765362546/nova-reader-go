package rss

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/rule"
	"github.com/765362546/novel-reader-go/internal/util"
)

var imgSrcPattern = regexp.MustCompile(`<img[^>]*src\s*=\s*["']([^"']+)["']`)

func fetchContent(rawURL string, src *model.RssSource, page int, baseURL string) (string, error) {
	a := rule.NewAnalyze()
	a.Put("page", fmt.Sprintf("%d", page))
	f := rule.NewFetcher(nil, a)
	if src != nil && dstr(src.Header) != "" {
		var headers map[string]string
		if json.Unmarshal([]byte(dstr(src.Header)), &headers) == nil {
			if f.Header == nil {
				f.Header = map[string]string{}
			}
			for k, v := range headers {
				f.Header[k] = v
			}
		}
	}
	resp, err := f.Get(rawURL)
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Body == "" {
		return "", errors.New("error_get_web_content: " + rawURL)
	}
	return resp.Body, nil
}

func dstr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Articles parses a RSS sort page and returns the article list plus the next page URL.
func Articles(sortName, sortURL string, src *model.RssSource, page int) ([]*model.RssArticle, string, error) {
	body, err := fetchContent(sortURL, src, page, src.SourceUrl)
	if err != nil {
		return nil, "", err
	}
	ruleArticles := dstr(src.RuleArticles)
	if strings.TrimSpace(ruleArticles) == "" {
		list := parseDefaultXML(sortName, body, src.SourceUrl)
		return list, "", nil
	}
	a := rule.NewAnalyze()
	a.SetContent(body, sortURL)
	reverse := false
	if strings.HasPrefix(ruleArticles, "-") {
		reverse = true
		ruleArticles = ruleArticles[1:]
	}
	var nextURL string
	if rp := dstr(src.RuleNextPage); rp != "" {
		if strings.ToUpper(rp) == "PAGE" {
			nextURL = sortURL
		} else {
			nextURL = util.AbsoluteURL(sortURL, a.GetString0(rp))
		}
	}
	elements := a.GetElements(ruleArticles)
	out := make([]*model.RssArticle, 0, len(elements))
	for _, el := range elements {
		a.SetContent(el, sortURL)
		article := &model.RssArticle{
			Origin: src.SourceUrl,
			Sort:   sortName,
			Order:  int64(len(out)),
		}
		article.Title = strings.TrimSpace(a.GetString(dstr(src.RuleTitle)))
		article.PubDate = ptr(strings.TrimSpace(a.GetString(dstr(src.RulePubDate))))
		if rd := dstr(src.RuleDescription); rd != "" {
			article.Description = ptr(a.GetString(rd))
		}
		article.Image = ptr(a.GetStringURL(dstr(src.RuleImage)))
		link := a.GetString0(dstr(src.RuleLink))
		article.Link = util.AbsoluteURL(src.SourceUrl, link)
		if article.Variable == nil {
			article.Variable = ptr("")
		}
		if strings.TrimSpace(article.Title) == "" {
			continue
		}
		out = append(out, article)
	}
	if reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nextURL, nil
}

func ptr(s string) *string { return &s }

// Content parses an article body using the RSS content rule.
func Content(article *model.RssArticle, ruleContent string, src *model.RssSource) (string, error) {
	body, err := fetchContent(article.Link, src, 1, article.Origin)
	if err != nil {
		return "", err
	}
	a := rule.NewAnalyze()
	if article.Variable != nil && dstr(article.Variable) != "" {
		var vars map[string]string
		if json.Unmarshal([]byte(dstr(article.Variable)), &vars) == nil {
			for k, v := range vars {
				a.Put(k, v)
			}
		}
	}
	a.SetContent(body, util.AbsoluteURL(article.Origin, article.Link))
	return a.GetString(ruleContent), nil
}

func imageFromHTML(content string) string {
	if m := imgSrcPattern.FindStringSubmatch(content); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// parseDefaultXML handles standard RSS 2.0 and Atom feeds.
func parseDefaultXML(sortName, body, sourceURL string) []*model.RssArticle {
	decoder := xml.NewDecoder(strings.NewReader(body))
	decoder.Strict = false
	var out []*model.RssArticle
	var current *model.RssArticle
	var inItem bool
	var field string
	var text strings.Builder
	linkHref := ""
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if name == "item" || name == "entry" {
				inItem = true
				current = &model.RssArticle{Origin: sourceURL, Sort: sortName}
				linkHref = ""
				continue
			}
			if !inItem {
				continue
			}
			switch name {
			case "title", "description", "summary", "content", "encoded", "pubdate", "published", "updated", "time":
				field = name
				text.Reset()
			case "link":
				linkHref = ""
				for _, attr := range t.Attr {
					if strings.ToLower(attr.Name.Local) == "href" {
						linkHref = attr.Value
					}
				}
				if linkHref == "" {
					field = "link"
					text.Reset()
				}
			case "thumbnail":
				for _, attr := range t.Attr {
					if strings.ToLower(attr.Name.Local) == "url" && current.Image == nil {
						current.Image = ptr(attr.Value)
					}
				}
			case "enclosure":
				typ, url := "", ""
				for _, attr := range t.Attr {
					switch strings.ToLower(attr.Name.Local) {
					case "type":
						typ = attr.Value
					case "url":
						url = attr.Value
					}
				}
				if strings.Contains(typ, "image/") && url != "" {
					current.Image = ptr(url)
				}
			}
		case xml.CharData:
			if inItem && field != "" {
				text.Write([]byte(t))
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if name == "item" || name == "entry" {
				if current != nil {
					if linkHref != "" {
						current.Link = util.AbsoluteURL(sourceURL, linkHref)
					}
					current.Order = int64(len(out))
					if current.Title != "" {
						out = append(out, current)
					}
				}
				inItem = false
				current = nil
				field = ""
				continue
			}
			if !inItem || field == "" {
				continue
			}
			value := strings.TrimSpace(text.String())
			switch field {
			case "title":
				current.Title = value
			case "description", "summary":
				if current.Description == nil && value != "" {
					current.Description = ptr(value)
				}
				if current.Image == nil {
					if img := imageFromHTML(value); img != "" {
						current.Image = ptr(img)
					}
				}
			case "content", "encoded":
				if value != "" {
					current.Content = ptr(value)
				}
				if current.Image == nil {
					if img := imageFromHTML(value); img != "" {
						current.Image = ptr(img)
					}
				}
			case "pubdate", "published", "updated", "time":
				if current.PubDate == nil && value != "" {
					current.PubDate = ptr(value)
				}
			case "link":
				if value != "" && current.Link == "" {
					current.Link = util.AbsoluteURL(sourceURL, value)
				}
			}
			if field == "link" && linkHref == "" && value != "" {
				current.Link = util.AbsoluteURL(sourceURL, value)
			}
			field = ""
			text.Reset()
		}
	}
	if out == nil {
		out = []*model.RssArticle{}
	}
	return out
}
