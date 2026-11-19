package rule

import "testing"

func TestParseURLOptions(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		url     string
		method  string
		body    string
		charset string
		header  string
		retry   int
		js      string
	}{
		{
			name:   "plain json",
			raw:    `https://a.com/search,{"method":"POST","body":"k=1"}`,
			url:    "https://a.com/search",
			method: "POST",
			body:   "k=1",
		},
		{
			name:    "single quotes and unquoted keys",
			raw:     "https://a.com/search,{\n'charset': 'gbk',\n'method': 'POST',\n'body': 'k=1&t=2'\n}",
			url:     "https://a.com/search",
			method:  "POST",
			body:    "k=1&t=2",
			charset: "gbk",
		},
		{
			name:   "nested single quoted json body",
			raw:    `https://a.com/api,{'method':'POST','body':'{"bookId":"{{$.id}}"}'}`,
			url:    "https://a.com/api",
			method: "POST",
			body:   `{"bookId":"{{$.id}}"}`,
		},
		{
			name:   "headers and retry",
			raw:    `https://a.com/x, { method: 'POST', headers: {'X-Token': 'abc'}, retry: 3 }`,
			url:    "https://a.com/x",
			method: "POST",
			header: "abc",
			retry:  3,
		},
		{
			name:   "trailing comma",
			raw:    `https://a.com/x,{'method':'GET',}`,
			url:    "https://a.com/x",
			method: "GET",
		},
		{
			name: "js option",
			raw:  `https://a.com/x,{'js':'result'}`,
			url:  "https://a.com/x",
			js:   "result",
		},
		{
			name: "no options",
			raw:  `https://a.com/x?a=1,2`,
			url:  `https://a.com/x?a=1,2`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, opt := parseURLOptions(c.raw)
			if u != c.url {
				t.Fatalf("url = %q, want %q", u, c.url)
			}
			if opt.Method != c.method {
				t.Fatalf("method = %q, want %q", opt.Method, c.method)
			}
			if opt.Body != c.body {
				t.Fatalf("body = %q, want %q", opt.Body, c.body)
			}
			if opt.Charset != c.charset {
				t.Fatalf("charset = %q, want %q", opt.Charset, c.charset)
			}
			if opt.Retry != c.retry {
				t.Fatalf("retry = %d, want %d", opt.Retry, c.retry)
			}
			if opt.JS != c.js {
				t.Fatalf("js = %q, want %q", opt.JS, c.js)
			}
			if c.header != "" && opt.Headers["X-Token"] != c.header {
				t.Fatalf("headers = %v, want X-Token=%q", opt.Headers, c.header)
			}
		})
	}
}
