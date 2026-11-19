package rule

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// jsoupStringList implements the legado jsoup rule dialect:
// tag.div.-1, class.x@text, children@tag.li@tag.a@text, [1,3:-2] filters, CSS selectors.
func jsoupStringList(content any, ruleStr string) []string {
	ruleStr = strings.TrimSpace(ruleStr)
	if ruleStr == "" {
		return nil
	}
	isCSS := false
	if strings.HasPrefix(strings.ToLower(ruleStr), "@css:") {
		isCSS = true
		ruleStr = strings.TrimSpace(ruleStr[5:])
	}
	sel := asSelection(content)
	if sel == nil {
		return nil
	}

	if isCSS {
		last := strings.LastIndex(ruleStr, "@")
		if last < 0 {
			return resultLast(sel, "")
		}
		return resultLast(sel.Find(ruleStr[:last]), ruleStr[last+1:])
	}

	parts, sep := splitRules(ruleStr, "&&", "||", "%%")
	results := make([][]string, 0, len(parts))
	for _, p := range parts {
		temp := getResultList(sel, p)
		if len(temp) > 0 {
			results = append(results, temp)
			if sep == "||" {
				break
			}
		}
	}
	return mergeLists(results, sep)
}

func jsoupString0(content any, ruleStr string) string {
	list := jsoupStringList(content, ruleStr)
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

func jsoupString(content any, ruleStr string) string {
	list := jsoupStringList(content, ruleStr)
	if len(list) == 0 {
		return ""
	}
	return strings.Join(list, "\n")
}

func jsoupElements(content any, ruleStr string) []any {
	sel := asSelection(content)
	if sel == nil {
		return nil
	}
	isCSS := false
	if strings.HasPrefix(strings.ToLower(ruleStr), "@css:") {
		isCSS = true
		ruleStr = strings.TrimSpace(ruleStr[5:])
	}
	var out []any
	if isCSS {
		sel.Find(ruleStr).Each(func(_ int, s *goquery.Selection) {
			out = append(out, s)
		})
		return out
	}
	parts, sep := splitRules(ruleStr, "&&", "||", "%%")
	for i, p := range parts {
		es := getElements(sel, p)
		if len(es) > 0 {
			_ = i
			for _, e := range es {
				out = append(out, e)
			}
			if sep == "||" || sep == "%%" {
				break
			}
		}
	}
	return out
}

func asSelection(content any) *goquery.Selection {
	switch v := content.(type) {
	case *goquery.Selection:
		return v
	case *html.Node:
		if v.Type == html.DocumentNode {
			return goquery.NewDocumentFromNode(v).Selection
		}
		return goquery.NewDocumentFromNode(cloneNode(v)).Selection
	case []*html.Node:
		if len(v) > 0 {
			return asSelection(v[0])
		}
		return nil
	case []any:
		if len(v) > 0 {
			return asSelection(v[0])
		}
		return nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(v))
		if err != nil {
			return nil
		}
		return doc.Selection
	}
	if content == nil {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(fmt.Sprint(content)))
	if err != nil {
		return nil
	}
	return doc.Selection
}

// getResultList handles a rule chain split by '@': tag.div@tag.a@title.
func getResultList(sel *goquery.Selection, ruleStr string) []string {
	ruleStr = strings.TrimSpace(ruleStr)
	ruleStr = strings.TrimLeft(ruleStr, "@ ")
	ruleStr = strings.TrimSpace(ruleStr)
	if ruleStr == "" {
		return nil
	}
	rs := splitAtSign(ruleStr)
	if len(rs) == 0 {
		return nil
	}
	cur := sel
	for i := 0; i < len(rs)-1; i++ {
		cur = getElementsSingle(cur, rs[i])
		if cur == nil || cur.Length() == 0 {
			return nil
		}
	}
	return resultLast(cur, rs[len(rs)-1])
}

func getElements(sel *goquery.Selection, ruleStr string) []*goquery.Selection {
	ruleStr = strings.TrimSpace(ruleStr)
	rs := splitAtSign(ruleStr)
	if len(rs) == 0 {
		return nil
	}
	cur := sel
	for i, r := range rs {
		if i == len(rs)-1 {
			break
		}
		cur = getElementsSingle(cur, r)
		if cur == nil || cur.Length() == 0 {
			return nil
		}
	}
	var out []*goquery.Selection
	getElementsSingle(cur, rs[len(rs)-1]).Each(func(_ int, s *goquery.Selection) {
		out = append(out, s)
	})
	return out
}

// splitAtSign splits on '@' outside of [] and quotes.
func splitAtSign(rule string) []string {
	var parts []string
	depth := 0
	inSingle, inDouble := false, false
	start := 0
	for i := 0; i < len(rule); i++ {
		c := rule[i]
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '[', '(':
			if !inSingle && !inDouble {
				depth++
			}
		case ']', ')':
			if !inSingle && !inDouble && depth > 0 {
				depth--
			}
		case '@':
			if depth == 0 && !inSingle && !inDouble {
				parts = append(parts, rule[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, rule[start:])
	return parts
}

type indexItem struct {
	isRange bool
	a, b, c *int
}

func (it indexItem) indexes(length int) []int {
	if !it.isRange {
		if it.a == nil {
			return nil
		}
		idx := *it.a
		if idx < 0 {
			idx += length
		}
		if idx >= 0 && idx < length {
			return []int{idx}
		}
		return nil
	}
	start := 0
	if it.a != nil {
		start = *it.a
		if start < 0 {
			start += length
		}
	}
	end := length - 1
	if it.b != nil {
		end = *it.b
		if end < 0 {
			end += length
		}
	}
	step := 1
	if it.c != nil {
		step = *it.c
	}
	if start < 0 {
		start = 0
	}
	if start >= length {
		start = length - 1
	}
	if end < 0 {
		end = 0
	}
	if end >= length {
		end = length - 1
	}
	if step == 0 {
		step = 1
	}
	if step < 0 {
		step = -step
	}
	var out []int
	if end >= start {
		for i := start; i <= end; i += step {
			out = append(out, i)
		}
	} else {
		for i := start; i >= end; i -= step {
			out = append(out, i)
		}
	}
	return out
}

func parseInt(s string) *int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return &v
}

func parseIndexRule(rule string) (before string, split byte, items []indexItem) {
	split = ' '
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return rule, split, nil
	}

	// bracket form: tag.div[-1, 3:-2:-10, 2] / tag.div[!0:3] (exclude)
	if strings.HasSuffix(rule, "]") {
		if lb := strings.LastIndex(rule, "["); lb >= 0 {
			inner := rule[lb+1 : len(rule)-1]
			before = rule[:lb]
			split = '.'
			if strings.HasSuffix(before, "!") {
				split = '!'
				before = before[:len(before)-1]
			}
			// legado also allows the exclude marker inside the brackets: [!0:3]
			if trimmed := strings.TrimSpace(inner); strings.HasPrefix(trimmed, "!") {
				split = '!'
				inner = trimmed[1:]
			}
			if strings.TrimSpace(before) == "" {
				before = ""
			}
			for _, part := range strings.Split(inner, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				pieces := strings.Split(part, ":")
				item := indexItem{isRange: len(pieces) > 1}
				item.a = parseInt(pieces[0])
				if len(pieces) > 1 {
					item.b = parseInt(pieces[1])
				}
				if len(pieces) > 2 {
					item.c = parseInt(pieces[2])
				}
				items = append(items, item)
			}
			return before, split, items
		}
	}

	// legacy form: tag.div.-1 / tag.div!0 / tag.div:1:10
	// Mirrors legado's findIndexSet: scan right-to-left, hold the pending
	// number until a separator is met. This keeps names like "tag.h2" or
	// "class.list2" from being mistaken for an index list.
	nums := []int{}
	sep := byte(0)
	var l string
	curMinus := false
	for i := len(rule) - 1; i >= 0; i-- {
		c := rule[i]
		switch {
		case c == ' ':
			continue
		case c >= '0' && c <= '9':
			l = string(c) + l
		case c == '-':
			curMinus = true
		case c == '!' || c == '.' || c == ':':
			n, err := strconv.Atoi(l)
			if err != nil {
				return rule, ' ', nil
			}
			if curMinus {
				n = -n
			}
			nums = append(nums, n)
			l = ""
			curMinus = false
			if c == ':' {
				continue
			}
			sep = c
			before = strings.TrimSpace(rule[:i])
			for k := len(nums) - 1; k >= 0; k-- {
				v := nums[k]
				items = append(items, indexItem{a: &v})
			}
			return before, sep, items
		default:
			// not an index rule
			return rule, ' ', nil
		}
	}
	return rule, ' ', nil
}

func getElementsSingle(sel *goquery.Selection, rule string) *goquery.Selection {
	before, split, items := parseIndexRule(rule)
	var elements *goquery.Selection
	if before == "" {
		elements = sel.Children()
	} else {
		parts := strings.Split(before, ".")
		switch parts[0] {
		case "children":
			elements = sel.Children()
		case "class":
			if len(parts) > 1 {
				elements = sel.Find("." + joinDots(parts[1:]))
			}
		case "tag":
			if len(parts) > 1 {
				elements = sel.Find(joinDots(parts[1:]))
			}
		case "id":
			if len(parts) > 1 {
				elements = sel.Find("#" + joinDots(parts[1:]))
			}
		case "text":
			if len(parts) > 1 {
				needle := joinDots(parts[1:])
				elements = filterOwnText(sel, needle)
			}
		default:
			elements = sel.Find(before)
		}
	}
	if elements == nil {
		return emptySelection(sel)
	}
	if len(items) == 0 {
		return elements
	}
	length := elements.Length()
	selected := map[int]bool{}
	for _, item := range items {
		for _, idx := range item.indexes(length) {
			selected[idx] = true
		}
	}
	out := emptySelection(sel)
	if split == '!' {
		for i := 0; i < length; i++ {
			if !selected[i] {
				out = appendSelection(out, elements.Eq(i))
			}
		}
	} else {
		for i := 0; i < length; i++ {
			if selected[i] {
				out = appendSelection(out, elements.Eq(i))
			}
		}
	}
	return out
}

func joinDots(parts []string) string {
	return strings.Join(parts, ".")
}

func emptySelection(sel *goquery.Selection) *goquery.Selection {
	if sel == nil {
		return &goquery.Selection{}
	}
	return sel.Find("__legado_empty_never_match__")
}

func appendSelection(base, add *goquery.Selection) *goquery.Selection {
	if add == nil || add.Length() == 0 {
		return base
	}
	// goquery >= 1.10 returns a new Selection from AddNodes; the return value
	// must be kept, otherwise the accumulated nodes are silently dropped.
	return base.AddNodes(add.Nodes...)
}

func filterOwnText(sel *goquery.Selection, needle string) *goquery.Selection {
	out := emptySelection(sel)
	sel.Each(func(_ int, s *goquery.Selection) {
		s.Find("*").AddBack().Each(func(_ int, e *goquery.Selection) {
			if strings.Contains(ownText(e), needle) {
				out = out.AddNodes(e.Nodes...)
			}
		})
	})
	return out
}

func ownText(sel *goquery.Selection) string {
	var b strings.Builder
	for _, n := range sel.Nodes {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				b.WriteString(c.Data)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func outerHTML(sel *goquery.Selection) string {
	var buf bytes.Buffer
	for _, n := range sel.Nodes {
		_ = html.Render(&buf, n)
	}
	return buf.String()
}

func resultLast(sel *goquery.Selection, lastRule string) []string {
	if sel == nil {
		return nil
	}
	switch lastRule {
	case "text":
		var out []string
		sel.Each(func(_ int, s *goquery.Selection) {
			t := strings.TrimSpace(s.Text())
			if t != "" {
				out = append(out, t)
			}
		})
		return out
	case "ownText":
		var out []string
		sel.Each(func(_ int, s *goquery.Selection) {
			t := ownText(s)
			if t != "" {
				out = append(out, t)
			}
		})
		return out
	case "textNodes":
		var out []string
		sel.Each(func(_ int, s *goquery.Selection) {
			var lines []string
			for _, n := range s.Nodes {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						if t := strings.TrimSpace(c.Data); t != "" {
							lines = append(lines, t)
						}
					}
				}
			}
			if len(lines) > 0 {
				out = append(out, strings.Join(lines, "\n"))
			}
		})
		return out
	case "html":
		clone := cloneSelection(sel)
		clone.Find("script").Remove()
		clone.Find("style").Remove()
		htmlStr := outerHTML(clone)
		if htmlStr != "" {
			return []string{htmlStr}
		}
		return nil
	case "all":
		return []string{outerHTML(sel)}
	default:
		var out []string
		sel.Each(func(_ int, s *goquery.Selection) {
			url := s.AttrOr(lastRule, "")
			if strings.TrimSpace(url) == "" {
				return
			}
			for _, existing := range out {
				if existing == url {
					return
				}
			}
			out = append(out, url)
		})
		return out
	}
}

func cloneSelection(sel *goquery.Selection) *goquery.Selection {
	out := &goquery.Selection{}
	for _, n := range sel.Nodes {
		out = out.AddNodes(cloneNode(n))
	}
	return out
}

func cloneNode(n *html.Node) *html.Node {
	c := &html.Node{Type: n.Type, Data: n.Data, Attr: append([]html.Attribute(nil), n.Attr...)}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		cc := cloneNode(child)
		c.AppendChild(cc)
	}
	return c
}

func mergeLists(results [][]string, sep string) []string {
	if len(results) == 0 {
		return nil
	}
	if sep == "%%" {
		var out []string
		for i := 0; i < len(results[0]); i++ {
			for _, r := range results {
				if i < len(r) {
					out = append(out, r[i])
				}
			}
		}
		return out
	}
	var out []string
	for _, r := range results {
		out = append(out, r...)
	}
	return out
}

// splitRules splits on top-level && / || / %% while respecting brackets and quotes.
func splitRules(rule string, seps ...string) ([]string, string) {
	if len(rule) == 0 {
		return []string{rule}, ""
	}
	if len(seps) == 0 {
		return []string{rule}, ""
	}
	var out []string
	depth := 0
	inSingle, inDouble := false, false
	sepFound := ""
	start := 0
	for i := 0; i < len(rule); i++ {
		c := rule[i]
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '[', '(':
			if !inSingle && !inDouble {
				depth++
			}
		case ']', ')':
			if !inSingle && !inDouble && depth > 0 {
				depth--
			}
		}
		if depth != 0 || inSingle || inDouble {
			continue
		}
		for _, sep := range seps {
			if strings.HasPrefix(rule[i:], sep) {
				out = append(out, rule[start:i])
				if sepFound == "" {
					sepFound = sep
				}
				i += len(sep) - 1
				start = i + 1
				break
			}
		}
	}
	out = append(out, rule[start:])
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out, sepFound
}
