package localbook

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2"

	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/util"
)

//go:embed defaults/txtTocRule.json
var defaultFS embed.FS

var (
	txtRulesOnce sync.Once
	txtRules     []model.TxtTocRule
)

func DefaultTxtTocRules() []model.TxtTocRule {
	txtRulesOnce.Do(func() {
		b, err := defaultFS.ReadFile("defaults/txtTocRule.json")
		if err != nil {
			return
		}
		_ = json.Unmarshal(b, &txtRules)
		sort.SliceStable(txtRules, func(i, j int) bool { return txtRules[i].SerialNumber < txtRules[j].SerialNumber })
	})
	return txtRules
}

func txtFile(book *model.Book, workDir string) string {
	p := book.OriginName
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "\\") {
		p = workDir + "/" + p
	}
	return p
}

func readTxt(book *model.Book, workDir string) (string, string, error) {
	p := txtFile(book, workDir)
	b, err := os.ReadFile(p)
	if err != nil {
		// preview path: originName may point to a directory containing index.epub/txt
		return "", "", err
	}
	text, cs := DecodeText(b)
	return text, cs, nil
}

// TextChapterList parses a TXT file into chapters.
func TextChapterList(book *model.Book, workDir, rule string, defaultRule bool) ([]*model.BookChapter, error) {
	text, charset, err := readTxt(book, workDir)
	if err != nil {
		return nil, fmt.Errorf("本地文件不存在")
	}
	if book.Charset == "" {
		book.Charset = charset
	}
	if rule == "" {
		rule = pickTxtRule(text)
		if rule != "" {
			s := rule
			book.TocUrl = s
		}
	}
	if rule != "" {
		return parseByRule(book, text, rule)
	}
	return parseByLength(book, text), nil
}

func pickTxtRule(text string) string {
	sample := text
	if len(sample) > 512*1024 {
		sample = sample[:512*1024]
	}
	rules := DefaultTxtTocRules()
	best := ""
	maxCs := 1
	for i := len(rules) - 1; i >= 0; i-- {
		r := rules[i]
		if !r.Enable || strings.TrimSpace(r.Rule) == "" {
			continue
		}
		re, err := regexp2.Compile(r.Rule, regexp2.Multiline)
		if err != nil {
			continue
		}
		re.MatchTimeout = 5 * time.Second
		cs := 0
		m, err := re.FindStringMatch(sample)
		for m != nil {
			cs++
			m, err = re.FindNextMatch(m)
			if err != nil {
				break
			}
		}
		if cs >= maxCs {
			maxCs = cs
			best = r.Rule
		}
	}
	return best
}

func parseByRule(book *model.Book, text, rule string) ([]*model.BookChapter, error) {
	re, err := regexp2.Compile(rule, regexp2.Multiline)
	if err != nil {
		return nil, fmt.Errorf("目录规则错误: %v", err)
	}
	re.MatchTimeout = 5 * time.Second
	byteAt := buildRuneIndex(text)
	type match struct {
		startByte, endByte int
		title              string
	}
	var matches []match
	m, _ := re.FindStringMatch(text)
	for m != nil {
		startRune := m.Index
		endRune := m.Index + m.Length
		matches = append(matches, match{
			startByte: byteAt(startRune),
			endByte:   byteAt(endRune),
			title:     m.String(),
		})
		m, _ = re.FindNextMatch(m)
	}
	if len(matches) == 0 {
		return parseByLength(book, text), nil
	}

	var chapters []*model.BookChapter
	// preface
	if matches[0].startByte > 0 {
		preface := strings.TrimSpace(text[:matches[0].startByte])
		if preface != "" {
			chapters = append(chapters, &model.BookChapter{
				URL:   util.MD5(book.OriginName + "0前言")[8:24],
				Title: "前言",
				Start: 0,
				End:   int64(matches[0].startByte),
			})
		}
	}
	for i, mt := range matches {
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1].startByte
		}
		ch := &model.BookChapter{
			URL:   util.MD5(fmt.Sprintf("%s%d%s", book.OriginName, i, mt.title))[8:24],
			Title: mt.title,
			Start: int64(mt.startByte),
			End:   int64(end),
		}
		body := strings.TrimSpace(text[mt.endByte:end])
		if body == "" {
			ch.IsVolume = true
		}
		chapters = append(chapters, ch)
	}
	indexChapters(book, chapters)
	return chapters, nil
}

func parseByLength(book *model.Book, text string) []*model.BookChapter {
	const size = 10 * 1024
	var chapters []*model.BookChapter
	start := 0
	i := 1
	for start < len(text) {
		end := start + size
		if end >= len(text) {
			end = len(text)
		} else {
			// move to next newline
			if idx := strings.Index(text[end:], "\n"); idx >= 0 && idx < 4096 {
				end = end + idx + 1
			}
		}
		chapters = append(chapters, &model.BookChapter{
			URL:   util.MD5(fmt.Sprintf("%s%d", book.OriginName, i))[8:24],
			Title: fmt.Sprintf("第%d章(%d)", i, i),
			Start: int64(start),
			End:   int64(end),
		})
		start = end
		i++
	}
	indexChapters(book, chapters)
	return chapters
}

func indexChapters(book *model.Book, chapters []*model.BookChapter) {
	for i, c := range chapters {
		c.Index = i
		c.BookUrl = book.BookUrl
	}
	if len(chapters) > 0 {
		book.LatestChapterTitle = chapters[len(chapters)-1].Title
	}
	book.TotalChapterNum = len(chapters)
}

// TextContent returns the chapter text with the original formatting behavior.
func TextContent(book *model.Book, chapter *model.BookChapter, workDir string) (string, error) {
	text, _, err := readTxt(book, workDir)
	if err != nil {
		return "", err
	}
	start := int(chapter.Start)
	end := int(chapter.End)
	if start < 0 || start > len(text) {
		return "", fmt.Errorf("章节数据错误")
	}
	if end > len(text) || end < start {
		end = len(text)
	}
	content := text[start:end]
	if idx := strings.Index(content, chapter.Title); idx >= 0 {
		content = content[idx+len(chapter.Title):]
	}
	content = strings.TrimLeft(content, "\n\r\t ")
	return "　　" + content, nil
}

func buildRuneIndex(s string) func(int) int {
	offsets := make([]int, 0, utf8RuneCount(s)+1)
	for i := 0; i < len(s); {
		offsets = append(offsets, i)
		_, size := runeAt(s, i)
		i += size
	}
	offsets = append(offsets, len(s))
	return func(runeIdx int) int {
		if runeIdx < 0 {
			return 0
		}
		if runeIdx >= len(offsets) {
			return len(s)
		}
		return offsets[runeIdx]
	}
}

func runeAt(s string, i int) (rune, int) {
	for _, r := range s[i:] {
		if r < 0x80 {
			return r, 1
		}
		if r < 0x800 {
			return r, 2
		}
		if r < 0x10000 {
			return r, 3
		}
		return r, 4
	}
	return 0, 1
}

func utf8RuneCount(s string) int {
	count := 0
	for i := 0; i < len(s); {
		_, size := runeAt(s, i)
		i += size
		count++
	}
	return count
}
