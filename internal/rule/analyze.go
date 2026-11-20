package rule

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/765362546/novel-reader-go/internal/cache"
	"github.com/765362546/novel-reader-go/internal/model"
	"golang.org/x/net/html"
)

type mode int

const (
	modeDefault mode = iota
	modeJS
	modeJSON
	modeXPath
	modeRegex
)

var (
	jsTagPattern   = regexp.MustCompile(`(?i)<js>([\w\W]*?)</js>`)
	jsRulePattern  = regexp.MustCompile(`(?i)@js:`)
	inlinePattern  = regexp.MustCompile(`\{\{([\w\W]*?)\}\}`)
	putPattern     = regexp.MustCompile(`(?i)@put:(\{[^}]+?\})`)
	getPattern     = regexp.MustCompile(`(?i)@get:\{([^}]+)\}`)
	regexGroupPatt = regexp.MustCompile(`\$\d{1,2}`)

	regexCache = cache.New[string, *regexp.Regexp](2048)
)

// compiledRegexp caches compiled rule patterns; chapter rules are evaluated
// repeatedly against every page, so recompiling the same regexp is wasteful.
func compiledRegexp(pattern string) (*regexp.Regexp, error) {
	if re, ok := regexCache.Get(pattern); ok {
		return re, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Put(pattern, re)
	return re, nil
}

// Analyze is the Go port of io.legado.app.model.analyzeRule.AnalyzeRule.
type Analyze struct {
	Content        any
	BaseUrl        string
	RedirectUrl    string
	Book           *model.Book
	Chapter        *model.BookChapter
	Source         *model.BookSource
	NextChapterURL string
	Vars           map[string]string

	// isJSON mirrors legado: a JSON body makes every plain rule a JsonPath rule.
	isJSON bool

	// Fetch is used by java.ajax in JS rules.
	Fetch func(string) (string, error)
}

func NewAnalyze() *Analyze {
	return &Analyze{Vars: map[string]string{}}
}

func (a *Analyze) SetContent(content any, baseURL string) {
	a.Content = content
	a.BaseUrl = baseURL
	a.isJSON = isJSONText(content)
	if baseURL != "" {
		if u, err := url.Parse(baseURL); err == nil {
			a.RedirectUrl = u.String()
		}
	}
}

// isJSONText mirrors legado's String.isJson(): trimmed text starting with { }
// or [ ] is treated as JSON, so plain rules are evaluated as JsonPath.
func isJSONText(content any) bool {
	s, ok := content.(string)
	if !ok {
		return false
	}
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return false
	}
	return (strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"))
}

func (a *Analyze) Get(key string) string {
	switch key {
	case "bookName":
		if a.Book != nil {
			return a.Book.Name
		}
	case "title":
		if a.Chapter != nil {
			return a.Chapter.Title
		}
	}
	if a.Vars != nil {
		return a.Vars[key]
	}
	return ""
}

func (a *Analyze) Put(key, value string) string {
	if a.Vars == nil {
		a.Vars = map[string]string{}
	}
	a.Vars[key] = value
	return value
}

// GetString evaluates a rule and returns the joined text.
func (a *Analyze) GetString(ruleStr string) string {
	if strings.TrimSpace(ruleStr) == "" || a.Content == nil {
		return ""
	}
	result := a.evalRules(ruleStr, a.Content, false)
	str := resultString(result)
	return html.UnescapeString(str)
}

// GetStringList evaluates a rule and returns a list of strings.
func (a *Analyze) GetStringList(ruleStr string) []string {
	if strings.TrimSpace(ruleStr) == "" || a.Content == nil {
		return nil
	}
	result := a.evalRules(ruleStr, a.Content, false)
	list := resultList(result)
	for i := range list {
		list[i] = html.UnescapeString(list[i])
	}
	return list
}

// GetString0 returns the first string (URL rules use it).
func (a *Analyze) GetString0(ruleStr string) string {
	list := a.GetStringList(ruleStr)
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

// GetStringURL returns the first result as an absolute URL.
func (a *Analyze) GetStringURL(ruleStr string) string {
	base := a.RedirectUrl
	if base == "" {
		base = a.BaseUrl
	}
	return absoluteURL(base, a.GetString0(ruleStr))
}

func (a *Analyze) GetStringListURL(ruleStr string) []string {
	list := a.GetStringList(ruleStr)
	base := a.RedirectUrl
	if base == "" {
		base = a.BaseUrl
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s == "" {
			continue
		}
		out = append(out, absoluteURL(base, s))
	}
	return out
}

func (a *Analyze) GetElements(ruleStr string) []any {
	if strings.TrimSpace(ruleStr) == "" || a.Content == nil {
		return nil
	}
	result := a.evalRules(ruleStr, a.Content, true)
	if list, ok := result.([]any); ok {
		return list
	}
	return nil
}

func resultString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []string:
		return strings.Join(t, "\n")
	case []any:
		var parts []string
		for _, x := range t {
			parts = append(parts, resultString(x))
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(v)
	}
}

func resultList(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		// legado splits a string result into lines for getStringList.
		lines := strings.Split(t, "\n")
		out := make([]string, 0, len(lines))
		for _, l := range lines {
			if l != "" {
				out = append(out, l)
			}
		}
		return out
	case []string:
		return t
	case []any:
		var out []string
		for _, x := range t {
			if s := resultString(x); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return []string{fmt.Sprint(v)}
	}
}

// evalRules handles JS segments first, then evaluates each normal segment.
func (a *Analyze) evalRules(ruleStr string, content any, wantElements bool) any {
	segments := splitJSSegments(ruleStr)
	// legado: when the content is a JS object (Rhino NativeObject), the rule is
	// used directly as a field name, e.g. chapterName="title" on
	// {title:"第一章",url:"..."}. Without this the rule would be parsed as CSS.
	if !wantElements && len(segments) > 0 && !segments[0].isJS {
		if m, ok := asStringMap(content); ok {
			key := mapFieldKey(segments[0].text)
			if key != "" {
				return m[key]
			}
		}
	}
	result := content
	for _, seg := range segments {
		if seg.isJS {
			result = a.EvalJS(seg.text, result)
			continue
		}
		result = a.evalSegment(seg.text, result, wantElements)
		if result == nil {
			return nil
		}
	}
	return result
}

// asStringMap reports whether content is a decoded JS/JSON object.
func asStringMap(content any) (map[string]any, bool) {
	switch m := content.(type) {
	case map[string]any:
		return m, true
	case map[string]string:
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out, true
	}
	return nil, false
}

// mapFieldKey normalizes a plain rule into an object field name.
func mapFieldKey(rule string) string {
	key := strings.TrimSpace(rule)
	key = strings.TrimLeft(key, "@ \t\r\n")
	if strings.HasPrefix(key, "##") {
		return ""
	}
	if idx := strings.Index(key, "##"); idx >= 0 {
		key = strings.TrimSpace(key[:idx])
	}
	return key
}

type ruleSegment struct {
	text string
	isJS bool
}

func splitJSSegments(ruleStr string) []ruleSegment {
	var out []ruleSegment
	pos := 0
	for pos < len(ruleStr) {
		jsTagLoc := jsTagPattern.FindStringSubmatchIndex(ruleStr[pos:])
		jsPrefixLoc := jsRulePattern.FindStringIndex(ruleStr[pos:])
		best := -1
		isTag := false
		if jsTagLoc != nil {
			best = jsTagLoc[0]
			isTag = true
		}
		if jsPrefixLoc != nil && (best == -1 || jsPrefixLoc[0] < best) {
			best = jsPrefixLoc[0]
			isTag = false
		}
		if best == -1 {
			out = append(out, ruleSegment{text: ruleStr[pos:]})
			break
		}
		if best > 0 {
			normal := ruleStr[pos : pos+best]
			if strings.TrimSpace(normal) != "" {
				out = append(out, ruleSegment{text: normal})
			}
		}
		if isTag {
			end := pos + jsTagLoc[1]
			code := ruleStr[pos+jsTagLoc[2] : pos+jsTagLoc[3]]
			out = append(out, ruleSegment{text: code, isJS: true})
			pos = end
		} else {
			code := ruleStr[pos+jsPrefixLoc[1]:]
			out = append(out, ruleSegment{text: code, isJS: true})
			pos = len(ruleStr)
		}
	}
	return out
}

func (a *Analyze) evalSegment(ruleStr string, content any, wantElements bool) any {
	ruleStr = strings.TrimSpace(ruleStr)
	ruleStr = strings.TrimLeft(ruleStr, "@ \t\r\n")
	if ruleStr == "" {
		return content
	}

	// @put / @get / {{...}}
	ruleStr = a.expandInline(ruleStr, content)

	// ## replacement: rule##regex##replacement[##first]
	rulePart := ruleStr
	replaceRegex := ""
	replacement := ""
	replaceFirst := false
	if idx := strings.Index(ruleStr, "##"); idx >= 0 {
		parts := strings.Split(ruleStr, "##")
		rulePart = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			replaceRegex = parts[1]
		}
		if len(parts) > 2 {
			replacement = parts[2]
		}
		if len(parts) > 3 {
			replaceFirst = true
		}
	}

	m := a.detectMode(rulePart)
	ruleBody := trimModePrefix(rulePart, m)
	if rulePart == "" && replaceRegex != "" {
		return applyReplace(resultString(content), replaceRegex, replacement, replaceFirst)
	}

	var result any
	switch m {
	case modeJS:
		result = a.EvalJS(ruleBody, content)
	case modeJSON:
		if wantElements {
			result = jsonPathElements(content, ruleBody)
		} else {
			result = a.jsonStringWithInner(content, ruleBody)
		}
	case modeXPath:
		if wantElements {
			result = xpathElements(content, rulePart)
		} else {
			result = xpathString(content, rulePart)
		}
	case modeRegex:
		if wantElements {
			result = regexElements(content, rulePart)
		} else {
			result = regexString(content, rulePart)
		}
	default:
		if wantElements {
			result = jsoupElements(content, rulePart)
		} else {
			result = jsoupNonCss(content, rulePart, m)
		}
	}

	if replaceRegex != "" && result != nil {
		if list, ok := result.([]string); ok {
			for i := range list {
				list[i] = applyReplace(list[i], replaceRegex, replacement, replaceFirst)
			}
			result = list
		} else if arr, ok := result.([]any); ok {
			_ = arr
		} else {
			result = applyReplace(resultString(result), replaceRegex, replacement, replaceFirst)
		}
	}
	return result
}

func (a *Analyze) detectMode(rule string) mode {
	lower := strings.ToLower(rule)
	switch {
	case strings.HasPrefix(lower, "@css:"):
		return modeDefault
	case strings.HasPrefix(rule, "@@"):
		return modeDefault
	case strings.HasPrefix(lower, "@xpath:"):
		return modeXPath
	case strings.HasPrefix(lower, "@json:"):
		return modeJSON
	case strings.HasPrefix(rule, "$.") || strings.HasPrefix(rule, "$["):
		return modeJSON
	}
	// legado: a JSON document switches every plain rule to JsonPath.
	if a.isJSON {
		return modeJSON
	}
	if strings.HasPrefix(rule, "/") {
		return modeXPath
	}
	if regexGroupPatt.MatchString(rule) {
		return modeRegex
	}
	return modeDefault
}

func trimModePrefix(rule string, m mode) string {
	switch m {
	case modeXPath:
		if strings.HasPrefix(strings.ToLower(rule), "@xpath:") {
			return strings.TrimSpace(rule[7:])
		}
		return rule
	case modeJSON:
		if strings.HasPrefix(strings.ToLower(rule), "@json:") {
			return strings.TrimSpace(rule[6:])
		}
		return rule
	case modeDefault:
		if strings.HasPrefix(rule, "@@") {
			return rule[2:]
		}
		return rule
	}
	return rule
}

func jsoupNonCss(content any, rule string, m mode) any {
	if strings.HasPrefix(strings.ToLower(rule), "@css:") || strings.HasPrefix(rule, "@CSS:") {
		return jsoupString(content, rule)
	}
	return jsoupString(content, rule)
}

// jsonStringWithInner expands {$.rule} inner rules then evaluates.
func (a *Analyze) jsonStringWithInner(content any, rule string) string {
	expanded := expandJSONInner(rule, func(inner string) string {
		return jsonPathString(content, inner)
	})
	return jsonPathString(content, expanded)
}

func expandJSONInner(rule string, fn func(string) string) string {
	var b strings.Builder
	i := 0
	for i < len(rule) {
		start := strings.Index(rule[i:], "{")
		if start < 0 {
			b.WriteString(rule[i:])
			break
		}
		start += i
		// find balanced closing brace
		depth := 0
		end := -1
		inS, inD := false, false
		for j := start; j < len(rule); j++ {
			c := rule[j]
			if c == '\'' && !inD {
				inS = !inS
			} else if c == '"' && !inS {
				inD = !inD
			}
			if inS || inD {
				continue
			}
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					end = j
					break
				}
			}
		}
		if end < 0 {
			b.WriteString(rule[i:])
			break
		}
		inner := rule[start+1 : end]
		if strings.HasPrefix(inner, "$.") || strings.HasPrefix(inner, "$[") {
			b.WriteString(rule[i:start])
			b.WriteString(fn(inner))
			i = end + 1
			continue
		}
		if strings.HasPrefix(inner, "{{") {
			b.WriteString(rule[i:start])
			b.WriteString("{{" + fn(strings.TrimPrefix(inner, "{{")) + "}}")
			i = end + 1
			continue
		}
		b.WriteString(rule[i : start+1])
		i = start + 1
	}
	return b.String()
}

func (a *Analyze) expandInline(rule string, content any) string {
	rule = putPattern.ReplaceAllStringFunc(rule, func(s string) string {
		sub := putPattern.FindStringSubmatch(s)
		if len(sub) > 1 {
			a.applyPutJSON(sub[1])
		}
		return ""
	})
	rule = getPattern.ReplaceAllStringFunc(rule, func(s string) string {
		sub := getPattern.FindStringSubmatch(s)
		if len(sub) > 1 {
			return a.Get(sub[1])
		}
		return ""
	})
	// {{...}} inline expressions
	for i := 0; i < 3; i++ {
		changed := false
		rule = inlinePattern.ReplaceAllStringFunc(rule, func(s string) string {
			sub := inlinePattern.FindStringSubmatch(s)
			if len(sub) < 2 {
				return s
			}
			changed = true
			expr := strings.TrimSpace(sub[1])
			if isRuleLike(expr) {
				return resultString(a.evalRules(expr, content, false))
			}
			v := a.EvalJS(expr, content)
			return resultString(v)
		})
		if !changed {
			break
		}
	}
	return rule
}

func isRuleLike(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(s, "@") || strings.HasPrefix(s, "$.") || strings.HasPrefix(s, "$[") ||
		strings.HasPrefix(s, "//") || strings.HasPrefix(lower, "<js>")
}

func (a *Analyze) applyPutJSON(jsonStr string) {
	var m map[string]string
	if err := json.Unmarshal([]byte(jsonStr), &m); err == nil {
		for k, v := range m {
			a.Put(k, a.GetString(v))
		}
	}
}

func applyReplace(s, pattern, replacement string, first bool) string {
	re, err := compiledRegexp(pattern)
	if err != nil {
		if first {
			return strings.Replace(s, pattern, replacement, 1)
		}
		return strings.ReplaceAll(s, pattern, replacement)
	}
	if first {
		loc := re.FindStringIndex(s)
		if loc == nil {
			return ""
		}
		return re.ReplaceAllString(s, replacement)
	}
	return re.ReplaceAllString(s, replacement)
}

// Regex extraction ("：" prefix / $1 group rules).
func regexString(content any, rule string) string {
	list := regexLists(content, rule, false)
	_ = list
	if len(list) == 0 {
		return ""
	}
	return strings.Join(list[0], "\n")
}

func regexElements(content any, rule string) []any {
	text := resultString(content)
	regs := strings.Split(strings.TrimPrefix(strings.TrimSpace(rule), ":"), "&&")
	if len(regs) == 0 {
		return nil
	}
	re, err := compiledRegexp(regs[0])
	if err != nil {
		return nil
	}
	var out []any
	for _, m := range re.FindAllString(text, -1) {
		out = append(out, m)
	}
	return out
}

func regexLists(content any, rule string, all bool) [][]string {
	rule = strings.TrimSpace(rule)
	rule = strings.TrimPrefix(rule, ":")
	regs := strings.Split(rule, "&&")
	text := resultString(content)
	if len(regs) == 0 {
		return nil
	}
	re, err := compiledRegexp(regs[0])
	if err != nil {
		return nil
	}
	if all {
		return nil
	}
	m := re.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	if len(regs) == 1 {
		return [][]string{m}
	}
	var sb strings.Builder
	for _, mm := range re.FindAllString(text, -1) {
		sb.WriteString(mm)
	}
	return regexLists(sb.String(), strings.Join(regs[1:], "&&"), all)
}
