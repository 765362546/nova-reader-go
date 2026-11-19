package rule

import (
	"strings"
	"testing"
)

func TestIndexRuleForms(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<div class="listmain"><dl><dt>t</dt>`)
	for i := 0; i < 20; i++ {
		sb.WriteString(`<dd><a href="/c">c</a></dd>`)
	}
	sb.WriteString(`</dl></div>`)
	a := NewAnalyze()
	a.SetContent(sb.String(), "http://x.test/")
	cases := map[string]int{
		"class.listmain@tag.dd":         20,
		"class.listmain@tag.dd!0:1":     18,
		"class.listmain@tag.dd.0:1":     2,
		"class.listmain@tag.dd!0:1:2:3": 16,
		"class.listmain@tag.dd[!0:3]":   16,
		"class.listmain@tag.dd[0,2]":    2,
	}
	for rule, want := range cases {
		if got := len(a.GetElements(rule)); got != want {
			t.Errorf("rule %q got %d want %d", rule, got, want)
		}
	}
}

func TestTagNamesEndingWithDigit(t *testing.T) {
	a := NewAnalyze()
	a.SetContent(`<div class="list2"><h2>标题</h2><a href="/x">链接</a></div>`, "http://x.test/")
	if got := a.GetString(`class.list2@tag.h2@text`); got != "标题" {
		t.Fatalf("h2 text got %q", got)
	}
	if got := a.GetString0(`class.list2@tag.a@href`); got != "/x" {
		t.Fatalf("href got %q", got)
	}
	if got := len(a.GetElements(`class.list2@tag.h2`)); got != 1 {
		t.Fatalf("elements got %d", got)
	}
}

func TestIndexRuleExcludeKeepsOrder(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<div class="listmain"><dl>`)
	for i := 0; i < 5; i++ {
		sb.WriteString(`<dd>n` + string(rune('0'+i)) + `</dd>`)
	}
	sb.WriteString(`</dl></div>`)
	a := NewAnalyze()
	a.SetContent(sb.String(), "http://x.test/")
	els := a.GetElements("class.listmain@tag.dd!0:1")
	if len(els) != 3 {
		t.Fatalf("got %d", len(els))
	}
	var titles []string
	for _, el := range els {
		a.SetContent(el, "http://x.test/")
		titles = append(titles, a.GetString("text"))
	}
	got := strings.Join(titles, ",")
	if got != "n2,n3,n4" {
		t.Fatalf("got %q", got)
	}
}
