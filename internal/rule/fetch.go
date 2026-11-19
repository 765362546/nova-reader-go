package rule

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"

	"github.com/765362546/novel-reader-go/internal/model"
)

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3770.142 Safari/537.36"

// Response is a decoded HTTP response.
type Response struct {
	Body    string
	Bytes   []byte
	URL     string
	Status  int
	Header  http.Header
}

// Fetcher performs AnalyzeUrl-style requests (URL + JSON options + charset).
type Fetcher struct {
	Client   *http.Client
	Source   *model.BookSource
	RuleData *Analyze
	Header   map[string]string
	UserAgent string
	mu       sync.Mutex
	last     time.Time
	minGap   time.Duration
}

func NewFetcher(source *model.BookSource, ruleData *Analyze) *Fetcher {
	jar, _ := cookiejar.New(nil)
	f := &Fetcher{
		Client: &http.Client{
			Jar:     jar,
			Timeout: 40 * time.Second,
		},
		Source:    source,
		RuleData:  ruleData,
		Header:    map[string]string{},
		UserAgent: defaultUserAgent,
	}
	if source != nil {
		if source.Header != nil && strings.TrimSpace(*source.Header) != "" {
			var hm map[string]any
			if json.Unmarshal([]byte(*source.Header), &hm) == nil {
				for k, v := range hm {
					f.Header[k] = fmt.Sprint(v)
				}
			}
		}
		if source.ConcurrentRate != nil {
			parseRate(f, *source.ConcurrentRate)
		}
	}
	return f
}

func parseRate(f *Fetcher, rate string) {
	parts := strings.Split(rate, "/")
	if len(parts) == 2 {
		n, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		d, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		if n > 0 && d > 0 {
			f.minGap = time.Duration(d) * time.Second / time.Duration(n)
		}
	}
}

func (f *Fetcher) wait() {
	if f.minGap <= 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.last.IsZero() {
		if d := time.Until(f.last.Add(f.minGap)); d > 0 {
			time.Sleep(d)
		}
	}
	f.last = time.Now()
}

type urlOption struct {
	Method  string
	Charset string
	Headers map[string]string
	Body    string
	Retry   int
	Type    string
	WebJs   string
	JS      string
}

func parseURLOptions(raw string) (string, urlOption) {
	opt := urlOption{}
	trimmed := strings.TrimSpace(raw)
	idx := strings.LastIndex(trimmed, ",{")
	if idx < 0 || !strings.HasSuffix(trimmed, "}") {
		return raw, opt
	}
	urlPart := strings.TrimSpace(trimmed[:idx])
	jsonPart := strings.TrimSpace(trimmed[idx+1:])
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonPart), &m); err != nil {
		return raw, opt
	}
	for k, v := range m {
		switch strings.ToLower(k) {
		case "method":
			opt.Method = strings.ToUpper(fmt.Sprint(v))
		case "charset":
			opt.Charset = fmt.Sprint(v)
		case "headers":
			opt.Headers = toStringMap(v)
		case "body":
			switch b := v.(type) {
			case string:
				opt.Body = b
			default:
				bs, _ := json.Marshal(b)
				opt.Body = string(bs)
			}
		case "retry":
			opt.Retry = intFromAny(v)
		case "type":
			opt.Type = fmt.Sprint(v)
		case "webjs":
			opt.WebJs = fmt.Sprint(v)
		case "js":
			opt.JS = fmt.Sprint(v)
		}
	}
	return urlPart, opt
}

func toStringMap(v any) map[string]string {
	out := map[string]string{}
	switch m := v.(type) {
	case map[string]any:
		for k, val := range m {
			out[k] = fmt.Sprint(val)
		}
	case string:
		var mm map[string]any
		if json.Unmarshal([]byte(m), &mm) == nil {
			for k, val := range mm {
				out[k] = fmt.Sprint(val)
			}
		}
	}
	return out
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

func (f *Fetcher) evalTemplate(s string) string {
	if f.RuleData == nil || !strings.Contains(s, "{{") {
		return s
	}
	return f.RuleData.expandInline(s, f.RuleData.Content)
}

// Get fetches the URL and decodes the response.
func (f *Fetcher) Get(rawURL string) (*Response, error) {
	rawURL = strings.TrimSpace(rawURL)
	rawURL = f.evalTemplate(rawURL)
	urlPart, opt := parseURLOptions(rawURL)
	urlPart = f.evalTemplate(urlPart)
	if opt.Body != "" {
		opt.Body = f.evalTemplate(opt.Body)
	}
	urlPart = strings.ReplaceAll(urlPart, "{{key}}", f.variable("key"))
	urlPart = strings.ReplaceAll(urlPart, "{{page}}", f.variable("page"))
	if strings.TrimSpace(urlPart) == "" {
		return nil, fmt.Errorf("empty url")
	}
	// legado resolves relative rules against the book source url.
	if f.Source != nil {
		urlPart = resolveURL(f.Source.BookSourceUrl, urlPart)
	}

	// data URI
	if strings.HasPrefix(urlPart, "data:") {
		if idx := strings.Index(urlPart, ";base64,"); idx >= 0 {
			raw := urlPart[idx+8:]
			return &Response{Body: raw}, nil
		}
	}

	retry := opt.Retry
	if retry <= 0 {
		retry = 1
	}
	var lastErr error
	for attempt := 0; attempt < retry; attempt++ {
		f.wait()
		resp, body, err := f.doRequest(urlPart, opt)
		if err == nil && resp.StatusCode < 500 {
			return f.buildResponse(resp, body, opt), nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("http %d", resp.StatusCode)
		}
	}
	return nil, lastErr
}

func (f *Fetcher) variable(key string) string {
	if f.RuleData != nil {
		return f.RuleData.Get(key)
	}
	return ""
}

// resolveURL turns a relative rule url into an absolute one using the source
// home url, e.g. "/search.php?q=1" on "https://example.com".
func resolveURL(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || base == "" {
		return ref
	}
	if u, err := url.Parse(ref); err == nil && (u.IsAbs() || strings.HasPrefix(ref, "data:")) {
		return ref
	}
	base = strings.TrimSpace(base)
	if i := strings.Index(base, "##"); i >= 0 {
		base = base[:i]
	}
	b, err := url.Parse(base)
	if err != nil || b.Host == "" {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func (f *Fetcher) doRequest(rawURL string, opt urlOption) (*http.Response, []byte, error) {
	method := http.MethodGet
	if opt.Method == http.MethodPost {
		method = http.MethodPost
	}
	var bodyReader io.Reader
	contentType := ""
	if method == http.MethodPost {
		if strings.HasPrefix(strings.TrimSpace(opt.Body), "{") || strings.HasPrefix(strings.TrimSpace(opt.Body), "[") {
			bodyReader = bytes.NewReader([]byte(opt.Body))
			contentType = "application/json"
		} else if opt.Body != "" {
			bodyReader = bytes.NewReader([]byte(opt.Body))
		}
	}
	req, err := http.NewRequest(method, encodeURL(rawURL), bodyReader)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range f.Header {
		req.Header.Set(k, v)
	}
	for k, v := range opt.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return resp, b, nil
}

func (f *Fetcher) buildResponse(resp *http.Response, body []byte, opt urlOption) *Response {
	out := &Response{URL: resp.Request.URL.String(), Status: resp.StatusCode, Header: resp.Header}
	if opt.Type != "" {
		// binary/hex style responses
		switch strings.ToLower(opt.Type) {
		case "hex":
			out.Bytes = body
			out.Body = hex.EncodeToString(body)
		default:
			out.Bytes = body
			out.Body = string(body)
		}
		return out
	}
	decoded := decodeCharset(body, opt.Charset, resp.Header.Get("Content-Type"))
	out.Bytes = decoded
	out.Body = string(decoded)
	return out
}

func decodeCharset(body []byte, charsetName, contentType string) []byte {
	if charsetName != "" && !strings.EqualFold(charsetName, "auto") {
		if strings.EqualFold(charsetName, "escape") {
			return body
		}
		if enc := charsetFromLabel(charsetName); enc != nil {
			if decoded, err := enc.NewDecoder().Bytes(body); err == nil {
				return decoded
			}
		}
		return body
	}
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return body
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		return body
	}
	return decoded
}

func charsetFromLabel(label string) encoding.Encoding {
	enc, err := htmlindex.Get(label)
	if err != nil {
		return nil
	}
	return enc
}

func encodeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		// try percent-encoding the query manually
		if idx := strings.Index(raw, "?"); idx >= 0 {
			base := raw[:idx]
			q := raw[idx+1:]
			return base + "?" + encodeQuery(q)
		}
		return raw
	}
	if u.RawQuery != "" {
		u.RawQuery = encodeQuery(u.RawQuery)
		return u.String()
	}
	// path may contain Chinese
	if u.Path != "" && strings.Contains(u.Path, "%") {
		return u.String()
	}
	if hasNonASCII(u.Path) {
		u.Path = escapePath(u.Path)
	}
	return u.String()
}

func hasNonASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/")
}

func encodeQuery(q string) string {
	pairs := strings.Split(q, "&")
	for i, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := kv[0]
		v := kv[1]
		if !strings.Contains(v, "%") {
			v = url.QueryEscape(v)
		}
		pairs[i] = k + "=" + v
	}
	return strings.Join(pairs, "&")
}
