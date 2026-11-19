package rule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
	"golang.org/x/net/html"
)

func parseHTMLNode(content any) *html.Node {
	switch v := content.(type) {
	case *html.Node:
		if v.Type == html.DocumentNode {
			return v
		}
		doc := &html.Node{Type: html.DocumentNode}
		root := &html.Node{Type: html.ElementNode, Data: "html"}
		body := &html.Node{Type: html.ElementNode, Data: "body"}
		root.AppendChild(body)
		doc.AppendChild(root)
		body.AppendChild(cloneNode(v))
		return doc
	}
	htmlStr := ""
	switch v := content.(type) {
	case string:
		htmlStr = v
	default:
		htmlStr = outerHTML(asSelection(content))
	}
	htmlStr = strings.TrimSpace(htmlStr)
	if htmlStr == "" {
		return nil
	}
	if strings.HasSuffix(htmlStr, "</td>") {
		htmlStr = "<tr>" + htmlStr + "</tr>"
	}
	if strings.HasSuffix(htmlStr, "</tr>") || strings.HasSuffix(htmlStr, "</tbody>") {
		htmlStr = "<table>" + htmlStr + "</table>"
	}
	doc, err := htmlquery.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil
	}
	return doc
}

func xpathStringList(content any, ruleStr string) []string {
	if strings.TrimSpace(ruleStr) == "" {
		return nil
	}
	doc := parseHTMLNode(content)
	if doc == nil {
		return nil
	}
	parts, sep := splitRules(ruleStr, "&&", "||", "%%")
	var results [][]string
	for _, p := range parts {
		r := xpathQuery(doc, p)
		if len(r) > 0 {
			results = append(results, r)
			if sep == "||" {
				break
			}
		}
	}
	return mergeLists(results, sep)
}

func xpathString(content any, ruleStr string) string {
	list := xpathStringList(content, ruleStr)
	return strings.Join(list, "\n")
}

func xpathQuery(doc *html.Node, expr string) []string {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimPrefix(expr, "@XPath:")
	expr = strings.TrimSpace(expr)

	// //a/@href  -> select elements then read attribute
	attr := ""
	if idx := strings.LastIndex(expr, "/@"); idx >= 0 {
		attr = expr[idx+2:]
		expr = expr[:idx]
	}
	isText := false
	if strings.HasSuffix(expr, "/text()") {
		isText = true
		expr = strings.TrimSuffix(expr, "/text()")
	}
	if strings.HasSuffix(expr, "//text()") {
		isText = true
		expr = strings.TrimSuffix(expr, "//text()")
	}

	nodes, err := htmlquery.QueryAll(doc, expr)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range nodes {
		var s string
		switch {
		case attr != "":
			if n.Type == html.ElementNode {
				s = htmlquery.SelectAttr(n, attr)
			}
		case isText:
			s = nodeText(n)
		default:
			if n.Type == html.ElementNode {
				s = nodeOuterHTML(n)
			} else {
				s = nodeText(n)
			}
		}
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func xpathElements(content any, expr string) []any {
	doc := parseHTMLNode(content)
	if doc == nil {
		return nil
	}
	nodes, err := htmlquery.QueryAll(doc, expr)
	if err != nil {
		return nil
	}
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
	}
	return out
}

func nodeOuterHTML(n *html.Node) string {
	var buf bytes.Buffer
	_ = html.Render(&buf, n)
	return buf.String()
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func jsonPathStringList(content any, ruleStr string) []string {
	ruleStr = strings.TrimSpace(ruleStr)
	ruleStr = strings.TrimPrefix(ruleStr, "@json:")
	ruleStr = strings.TrimPrefix(ruleStr, "@Json:")
	ruleStr = strings.TrimSpace(ruleStr)
	data := parseJSON(content)
	if data == nil || ruleStr == "" {
		return nil
	}
	parts, sep := splitRules(ruleStr, "&&", "||", "%%")
	var results [][]string
	for _, p := range parts {
		vals := jsonPathGet(data, p)
		if len(vals) > 0 {
			results = append(results, vals)
			if sep == "||" {
				break
			}
		}
	}
	return mergeLists(results, sep)
}

func jsonPathString(content any, ruleStr string) string {
	return strings.Join(jsonPathStringList(content, ruleStr), "\n")
}

func jsonPathGet(data any, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	expr, err := jp.ParseString(path)
	if err != nil {
		return nil
	}
	vals := expr.Get(data)
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, toStringValue(v))
	}
	return out
}

func toStringValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return t.String()
	default:
		if _, ok := v.(map[string]any); ok {
			b, _ := json.Marshal(v)
			return string(b)
		}
		if _, ok := v.([]any); ok {
			b, _ := json.Marshal(v)
			return string(b)
		}
		return fmt.Sprint(v)
	}
}

func parseJSON(content any) any {
	switch v := content.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		if data, err := oj.Parse([]byte(v)); err == nil {
			return data
		}
		var data any
		if json.Unmarshal([]byte(v), &data) == nil {
			return data
		}
		return nil
	case []byte:
		if data, err := oj.Parse(v); err == nil {
			return data
		}
		var data any
		if json.Unmarshal(v, &data) == nil {
			return data
		}
		return nil
	default:
		return v
	}
}

func jsonPathElements(content any, ruleStr string) []any {
	ruleStr = strings.TrimSpace(ruleStr)
	ruleStr = strings.TrimPrefix(ruleStr, "@json:")
	ruleStr = strings.TrimSpace(ruleStr)
	data := parseJSON(content)
	if data == nil || ruleStr == "" {
		return nil
	}
	expr, err := jp.ParseString(ruleStr)
	if err != nil {
		return nil
	}
	vals := expr.Get(data)
	out := make([]any, 0, len(vals))
	for _, v := range vals {
		out = append(out, v)
	}
	return out
}

func jsonPathObject(data any, path string) (any, bool) {
	expr, err := jp.ParseString(path)
	if err != nil {
		return nil, false
	}
	vals := expr.Get(data)
	if len(vals) == 0 {
		return nil, false
	}
	if len(vals) == 1 {
		return vals[0], true
	}
	return vals, true
}
