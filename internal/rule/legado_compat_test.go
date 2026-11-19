package rule

import "testing"

func TestTocJSObjectArray(t *testing.T) {
	a := NewAnalyze()
	a.SetContent("<html><body>x</body></html>", "https://m.qidian.com/book/1/")
	elements := a.GetElements(`<js>var c=[];c.push({title:"第一章",url:"/c1.html",v:false});c.push({title:"第二章",url:"/c2.html",v:true});c</js>`)
	if len(elements) != 2 {
		t.Fatalf("elements=%#v", elements)
	}
	wantTitles := []string{"第一章", "第二章"}
	wantURLs := []string{"/c1.html", "/c2.html"}
	wantVIP := []string{"false", "true"}
	for i, el := range elements {
		a.SetContent(el, "https://m.qidian.com/book/1/")
		if got := a.GetString("title"); got != wantTitles[i] {
			t.Fatalf("item %d title=%q want %q", i, got, wantTitles[i])
		}
		if got := a.GetString0("url"); got != wantURLs[i] {
			t.Fatalf("item %d url=%q want %q", i, got, wantURLs[i])
		}
		if got := a.GetString("v"); got != wantVIP[i] {
			t.Fatalf("item %d v=%q want %q", i, got, wantVIP[i])
		}
	}
}

func TestJSONBodyPlainRule(t *testing.T) {
	a := NewAnalyze()
	a.SetContent(`{"data":{"list":[{"name":"书A","author":"作者A"}]}}`, "https://x.test/")
	if got := a.GetString("$.data.list[0].name"); got != "书A" {
		t.Fatalf("jsonpath rule got %q", got)
	}
	// A JSON body makes plain rules JsonPath (legado isJSON behavior).
	if got := a.GetString("data.list[0].author"); got != "作者A" {
		t.Fatalf("plain rule on JSON body got %q", got)
	}
	els := a.GetElements("data.list")
	if len(els) != 1 {
		t.Fatalf("elements=%#v", els)
	}
}

func TestGetStringListSplitsLines(t *testing.T) {
	a := NewAnalyze()
	a.SetContent("<div><p>a</p><p>b</p></div>", "https://x.test/")
	list := a.GetStringList(`<js>"l1\nl2"</js>`)
	if len(list) != 2 || list[0] != "l1" || list[1] != "l2" {
		t.Fatalf("list=%#v", list)
	}
}
