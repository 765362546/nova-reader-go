package util

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	nameRegex       = regexp.MustCompile(`\s+作\s*者.*|\s+\S+\s+著`)
	authorRegex     = regexp.MustCompile(`^\s*作\s*者[:：\s]+|\s+著`)
	fileNameRegex   = regexp.MustCompile(`[\\/:*?"<>|.]`)
	wrapHTMLRegex   = regexp.MustCompile(`</?(?:div|p|br|hr|h\d|article|dd|dl)[^>]*>`)
	commentRegex    = regexp.MustCompile(`<!--[^>]*-->`)
	spaceLineRegex  = regexp.MustCompile(`\s*\n+\s*`)
	imgSrcRegex     = regexp.MustCompile(`<img[^>]*src *= *"([^"]*)"[^>]*>`)
	htmlTagStrip    = regexp.MustCompile(`<[script>]*.*?>|&nbsp;`)
	brDivRegex      = regexp.MustCompile(`(?i)<(br[\s/]*|/*p\b.*?|/*div\b.*?)>`)
)

func FormatBookName(name string) string {
	return strings.TrimSpace(nameRegex.ReplaceAllString(name, ""))
}

func FormatBookAuthor(author string) string {
	return strings.TrimSpace(authorRegex.ReplaceAllString(author, ""))
}

func FormatAuthor(author string) string {
	out := regexp.MustCompile(`作\s*者[\s:：]*`).ReplaceAllString(author, "")
	out = regexp.MustCompile(`\s+`).ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

func FormatFolderName(name string) string {
	return fileNameRegex.ReplaceAllString(name, "")
}

func WordCountFormat(wc string) string {
	if wc == "" {
		return ""
	}
	if n, err := strconv.Atoi(strings.TrimSpace(wc)); err == nil {
		if n <= 0 {
			return ""
		}
		if n > 10000 {
			v := float64(n) / 10000.0
			return trimZero(fmt.Sprintf("%.1f", v)) + "万字"
		}
		return fmt.Sprintf("%d字", n)
	}
	return wc
}

func trimZero(s string) string {
	s = strings.TrimSuffix(s, ".0")
	return s
}

// HTMLFormat ports String?.htmlFormat().
func HTMLFormat(htmlStr string) string {
	if strings.TrimSpace(htmlStr) == "" {
		return ""
	}
	out := brDivRegex.ReplaceAllString(htmlStr, "\n")
	out = htmlTagStrip.ReplaceAllString(out, "")
	out = spaceLineRegex.ReplaceAllString(out, "\n　　")
	out = regexp.MustCompile(`^[\n\s]+`).ReplaceAllString(out, "　　")
	out = regexp.MustCompile(`[\n\s]+$`).ReplaceAllString(out, "")
	return out
}

// HTMLFormatKeepImg ports HtmlFormatter.formatKeepImg.
func HTMLFormatKeepImg(htmlStr string, redirectURL string) string {
	if htmlStr == "" {
		return ""
	}
	out := wrapHTMLRegex.ReplaceAllString(htmlStr, "\n")
	out = commentRegex.ReplaceAllString(out, "")
	// Remove all tags except img.
	out = stripTagsExceptImg(out)
	out = spaceLineRegex.ReplaceAllString(out, "\n　　")
	out = regexp.MustCompile(`^[\n\s]+`).ReplaceAllString(out, "　　")
	out = regexp.MustCompile(`[\n\s]+$`).ReplaceAllString(out, "")

	// Make image src absolute.
	return imgSrcRegex.ReplaceAllStringFunc(out, func(tag string) string {
		m := imgSrcRegex.FindStringSubmatch(tag)
		if len(m) < 2 {
			return tag
		}
		src := strings.TrimSpace(m[1])
		if src == "" {
			return tag
		}
		return `<img src="` + AbsoluteURL(redirectURL, src) + `">`
	})
}

func stripTagsExceptImg(s string) string {
	var b strings.Builder
	pos := 0
	for pos < len(s) {
		i := strings.IndexByte(s[pos:], '<')
		if i < 0 {
			b.WriteString(s[pos:])
			break
		}
		i += pos
		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			b.WriteString(s[pos:])
			break
		}
		j += i
		b.WriteString(s[pos:i])
		tag := s[i+1 : j]
		tag = strings.TrimPrefix(tag, "/")
		tag = strings.TrimSpace(tag)
		k := 0
		for k < len(tag) && ((tag[k] >= 'a' && tag[k] <= 'z') || (tag[k] >= 'A' && tag[k] <= 'Z')) {
			k++
		}
		if strings.ToLower(tag[:k]) == "img" {
			b.WriteString(s[i : j+1])
		}
		pos = j + 1
	}
	return b.String()
}

func AbsoluteURL(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return base
	}
	b, err := url.Parse(base)
	if err != nil || b.Scheme == "" {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func MD5(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func BookDirName(name, author string) string {
	name = FormatFolderName(FormatBookName(name))
	author = FormatFolderName(FormatBookAuthor(author))
	if author == "" {
		return name
	}
	return name + "_" + author
}
