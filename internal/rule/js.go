package rule

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/765362546/novel-reader-go/internal/cache"
	"github.com/765362546/novel-reader-go/internal/logging"
	"github.com/765362546/novel-reader-go/internal/model"
)

var logger = logging.Component("rule")

// jsProgramCache avoids re-parsing the same rule script on every evaluation.
var jsProgramCache = cache.New[string, *goja.Program](256)

func cachedProgram(code string) (*goja.Program, error) {
	if p, ok := jsProgramCache.Get(code); ok {
		return p, nil
	}
	p, err := goja.Compile("rule.js", code, false)
	if err != nil {
		return nil, err
	}
	jsProgramCache.Put(code, p)
	return p, nil
}

// androidID is a stable fake device id for sources that call java.androidId().
var androidID = fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%d", time.Now().UnixNano()))))

// EvalJS runs a Java-Script rule inside a fresh goja runtime with legado bindings.
func (a *Analyze) EvalJS(code string, result any) any {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	vm := goja.New()
	interrupt := time.AfterFunc(15*time.Second, func() { vm.Interrupt("timeout") })
	defer interrupt.Stop()

	contentStr := resultString(a.Content)
	resultVal := result
	if _, ok := resultVal.(string); !ok {
		if list, ok := resultVal.([]string); ok {
			arr := make([]any, len(list))
			for i := range list {
				arr[i] = list[i]
			}
			resultVal = arr
		} else if resultVal != nil {
			resultVal = resultString(resultVal)
		}
	}

	_ = vm.Set("result", normalizeJSValue(resultVal))
	_ = vm.Set("src", contentStr)
	_ = vm.Set("baseUrl", a.BaseUrl)
	_ = vm.Set("title", chapterTitle(a))
	_ = vm.Set("nextChapterUrl", a.NextChapterURL)
	_ = vm.Set("book", bookMap(a.Book))
	_ = vm.Set("chapter", chapterMap(a.Chapter))
	_ = vm.Set("source", sourceMap(a.Source))
	_ = vm.Set("page", a.Get("page"))
	_ = vm.Set("key", a.Get("key"))
	_ = vm.Set("cookie", map[string]any{
		"getCookie":    func(call goja.FunctionCall) goja.Value { return goja.Undefined() },
		"setCookie":    func(call goja.FunctionCall) goja.Value { return goja.Undefined() },
		"removeCookie": func(call goja.FunctionCall) goja.Value { return goja.Undefined() },
	})
	_ = vm.Set("cache", map[string]any{
		"get": func(call goja.FunctionCall) goja.Value { return vm.ToValue(a.Get(argString(call, 0))) },
		"put": func(call goja.FunctionCall) goja.Value {
			a.Put(argString(call, 0), argString(call, 1))
			return goja.Undefined()
		},
	})

	java := map[string]any{
		"ajax": func(call goja.FunctionCall) goja.Value {
			u := strings.TrimSpace(argString(call, 0))
			if u == "" || a.Fetch == nil {
				return goja.Null()
			}
			body, err := a.Fetch(u)
			if err != nil {
				logger.Debug("js ajax failed", "url", u, "error", err)
				return goja.Null()
			}
			return vm.ToValue(body)
		},
		"getString": func(call goja.FunctionCall) goja.Value {
			rule := argString(call, 0)
			sub := a.clone()
			if len(call.Arguments) > 1 {
				sub.SetContent(exportString(call.Arguments[1]), a.BaseUrl)
			}
			return vm.ToValue(sub.GetString(rule))
		},
		"getElement": func(call goja.FunctionCall) goja.Value {
			rule := argString(call, 0)
			return vm.ToValue(strings.Join(a.GetStringList(rule), "\n"))
		},
		"getElements": func(call goja.FunctionCall) goja.Value {
			rule := argString(call, 0)
			list := a.GetStringList(rule)
			arr := make([]any, len(list))
			for i := range list {
				arr[i] = list[i]
			}
			return vm.ToValue(arr)
		},
		"getStringList": func(call goja.FunctionCall) goja.Value {
			list := a.GetStringList(argString(call, 0))
			arr := make([]any, len(list))
			for i := range list {
				arr[i] = list[i]
			}
			return vm.ToValue(arr)
		},
		"get": func(call goja.FunctionCall) goja.Value { return vm.ToValue(a.Get(argString(call, 0))) },
		"put": func(call goja.FunctionCall) goja.Value {
			a.Put(argString(call, 0), argString(call, 1))
			return vm.ToValue(argString(call, 1))
		},
		"log": func(call goja.FunctionCall) goja.Value {
			logger.Debug("js rule log", "message", argString(call, 0), "baseUrl", a.BaseUrl)
			return goja.Undefined()
		},
		"toast": func(call goja.FunctionCall) goja.Value { return goja.Undefined() },
		"base64Encode": func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(base64.StdEncoding.EncodeToString([]byte(argString(call, 0))))
		},
		"base64Decode": func(call goja.FunctionCall) goja.Value {
			b, err := base64.StdEncoding.DecodeString(argString(call, 0))
			if err != nil {
				return vm.ToValue("")
			}
			return vm.ToValue(string(b))
		},
		"md5Encode": func(call goja.FunctionCall) goja.Value {
			sum := md5.Sum([]byte(argString(call, 0)))
			return vm.ToValue(hex.EncodeToString(sum[:]))
		},
		"md5Encode16": func(call goja.FunctionCall) goja.Value {
			sum := md5.Sum([]byte(argString(call, 0)))
			return vm.ToValue(hex.EncodeToString(sum[:])[8:24])
		},
		"timeFormat": func(call goja.FunctionCall) goja.Value {
			ms := int64(argFloat(call, 0))
			format := "yyyy/MM/dd HH:mm"
			if len(call.Arguments) > 1 {
				format = argString(call, 1)
			}
			return vm.ToValue(timeFormatJava(ms, format))
		},
		"timeFormatUTC": func(call goja.FunctionCall) goja.Value {
			ms := int64(argFloat(call, 0))
			format := argString(call, 1)
			return vm.ToValue(timeFormatJava(ms, format))
		},
		"htmlFormat": func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(htmlFormat(argString(call, 0)))
		},
		"encodeURI": func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(url.QueryEscape(argString(call, 0)))
		},
		"decodeURI": func(call goja.FunctionCall) goja.Value {
			s, err := url.QueryUnescape(argString(call, 0))
			if err != nil {
				return vm.ToValue("")
			}
			return vm.ToValue(s)
		},
		"getAbsoluteUrl": func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(absoluteURL(a.BaseUrl, argString(call, 0)))
		},
		"toNumChapter": func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(argString(call, 0))
		},
		"androidId": func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(androidID)
		},
		"refreshBookUrl": func(call goja.FunctionCall) goja.Value { return goja.Undefined() },
		"refreshTocUrl":  func(call goja.FunctionCall) goja.Value { return goja.Undefined() },
	}
	_ = vm.Set("java", java)

	// Legacy global helper functions.
	_ = vm.Set("base64Encode", java["base64Encode"])
	_ = vm.Set("base64Decode", java["base64Decode"])
	_ = vm.Set("md5Encode", java["md5Encode"])
	_ = vm.Set("md5Encode16", java["md5Encode16"])
	_ = vm.Set("timeFormat", java["timeFormat"])
	_ = vm.Set("htmlFormat", java["htmlFormat"])
	_ = vm.Set("encodeURI", java["encodeURI"])
	_ = vm.Set("decodeURI", java["decodeURI"])
	_ = vm.Set("ajax", java["ajax"])
	_ = vm.Set("log", java["log"])
	_ = vm.Set("put", java["put"])
	_ = vm.Set("get", java["get"])
	_ = vm.Set("uuid", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(fmt.Sprintf("%d", time.Now().UnixNano()))
	})

	program, err := cachedProgram(code)
	if err != nil {
		logger.Warn("js rule compile failed",
			"error", err,
			"baseUrl", a.BaseUrl,
			"code", truncate(code, 300),
		)
		return ""
	}
	val, err := vm.RunProgram(program)
	if err != nil {
		logger.Warn("js rule eval failed",
			"error", err,
			"baseUrl", a.BaseUrl,
			"code", truncate(code, 300),
		)
		return ""
	}
	// Propagate book url changes made from JS.
	if a.Book != nil {
		if obj, ok := vm.Get("book").(*goja.Object); ok {
			if b := obj.Get("bookUrl"); b != nil && !goja.IsUndefined(b) && !goja.IsNull(b) {
				if s := b.String(); s != "" {
					a.Book.BookUrl = s
				}
			}
			if b := obj.Get("tocUrl"); b != nil && !goja.IsUndefined(b) && !goja.IsNull(b) {
				if s := b.String(); s != "" {
					a.Book.TocUrl = s
				}
			}
		}
	}
	return exportValue(val)
}

func (a *Analyze) clone() *Analyze {
	c := *a
	vars := map[string]string{}
	for k, v := range a.Vars {
		vars[k] = v
	}
	c.Vars = vars
	return &c
}

func bookMap(b *model.Book) map[string]any {
	if b == nil {
		return map[string]any{}
	}
	return map[string]any{
		"bookUrl":            b.BookUrl,
		"tocUrl":             b.TocUrl,
		"origin":             b.Origin,
		"originName":         b.OriginName,
		"name":               b.Name,
		"author":             b.Author,
		"kind":               b.Kind,
		"coverUrl":           b.CoverUrl,
		"intro":              b.Intro,
		"type":               b.Type,
		"group":              b.Group,
		"latestChapterTitle": b.LatestChapterTitle,
		"totalChapterNum":    b.TotalChapterNum,
		"durChapterTitle":    b.DurChapterTitle,
		"durChapterIndex":    b.DurChapterIndex,
		"durChapterPos":      b.DurChapterPos,
		"variable":           deref(b.Variable),
	}
}

func chapterMap(c *model.BookChapter) map[string]any {
	if c == nil {
		return map[string]any{}
	}
	return map[string]any{
		"url":      c.URL,
		"title":    c.Title,
		"baseUrl":  c.BaseURL,
		"bookUrl":  c.BookUrl,
		"index":    c.Index,
		"tag":      deref(c.Tag),
		"start":    c.Start,
		"end":      c.End,
		"variable": deref(c.Variable),
	}
}

func sourceMap(s *model.BookSource) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return map[string]any{
		"bookSourceUrl":   s.BookSourceUrl,
		"bookSourceName":  s.BookSourceName,
		"bookSourceGroup": deref(s.BookSourceGroup),
		"bookSourceType":  s.BookSourceType,
		"bookUrlPattern":  deref(s.BookUrlPattern),
		"header":          deref(s.Header),
		"loginUrl":        deref(s.LoginUrl),
		"loginCheckJs":    deref(s.LoginCheckJs),
		"concurrentRate":  deref(s.ConcurrentRate),
		"enabled":         s.Enabled,
		// legado's BaseSource helpers, used by many JS rules.
		"getKey": func() string { return s.BookSourceUrl },
		"getTag": func() string { return s.BookSourceName },
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func chapterTitle(a *Analyze) string {
	if a.Chapter == nil {
		return ""
	}
	return a.Chapter.Title
}

func normalizeJSValue(v any) any {
	if v == nil {
		return goja.Null()
	}
	switch v.(type) {
	case string, bool, int, int64, float64, []any, map[string]any:
		return v
	}
	// fall back to JSON-compatible representation
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return fmt.Sprint(v)
	}
	return out
}

func exportValue(v goja.Value) any {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	return v.Export()
}

func exportString(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	if s, ok := v.Export().(string); ok {
		return s
	}
	return fmt.Sprint(v.Export())
}

func argString(call goja.FunctionCall, i int) string {
	if i >= len(call.Arguments) || call.Arguments[i] == nil || goja.IsUndefined(call.Arguments[i]) || goja.IsNull(call.Arguments[i]) {
		return ""
	}
	if s, ok := call.Arguments[i].Export().(string); ok {
		return s
	}
	return fmt.Sprint(call.Arguments[i].Export())
}

func argFloat(call goja.FunctionCall, i int) float64 {
	if i >= len(call.Arguments) || call.Arguments[i] == nil {
		return 0
	}
	switch n := call.Arguments[i].Export().(type) {
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case float64:
		return n
	case string:
		var f float64
		_, _ = fmt.Sscanf(n, "%f", &f)
		return f
	}
	return 0
}

func timeFormatJava(ms int64, format string) string {
	t := time.UnixMilli(ms)
	layout := strings.NewReplacer(
		"yyyy", "2006",
		"MM", "01",
		"dd", "02",
		"HH", "15",
		"mm", "04",
		"ss", "05",
		"SSS", "000",
	).Replace(format)
	return t.Format(layout)
}

func htmlFormat(s string) string {
	if strings.Contains(s, "<p") || strings.Contains(s, "<img") {
		return s
	}
	parts := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var b strings.Builder
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(p)
		b.WriteString("</p>")
	}
	return b.String()
}

func absoluteURL(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return base
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
