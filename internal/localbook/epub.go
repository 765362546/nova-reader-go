package localbook

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/util"
)

type opfItem struct {
	ID        string `xml:"id,attr"`
	Href      string `xml:"href,attr"`
	MediaType string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type opfSpine struct {
	IDRef string `xml:"idref,attr"`
}

type opfDoc struct {
	Metadata struct {
		Title       string `xml:"title"`
		Creator     string `xml:"creator"`
		Description string `xml:"description"`
		Language    string `xml:"language"`
	} `xml:"metadata"`
	Manifest struct {
		Items []opfItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Items []opfSpine `xml:"itemref"`
	} `xml:"spine"`
	Guide struct {
		References []struct {
			Type  string `xml:"type,attr"`
			Href  string `xml:"href,attr"`
			Title string `xml:"title,attr"`
		} `xml:"reference"`
	} `xml:"guide"`
}

type ncxPoint struct {
	Text     string     `xml:"navLabel>text"`
	Src      string     `xml:"content>src,attr"`
	Children []ncxPoint `xml:"navPoint"`
}

type ncxDoc struct {
	Points []ncxPoint `xml:"navMap>navPoint"`
}

func localBookPath(book *model.Book, workDir string) string {
	p := book.OriginName
	if !filepath.IsAbs(p) {
		p = filepath.Join(workDir, p)
	}
	return p
}

func epubArchivePath(book *model.Book, workDir string) string {
	p := localBookPath(book, workDir)
	st, err := os.Stat(p)
	if err == nil && st.IsDir() {
		return filepath.Join(p, "index.epub")
	}
	return p
}

// ExtractEpub unzips the epub into <originName>/index.
func ExtractEpub(book *model.Book, workDir string) error {
	zipPath := epubArchivePath(book, workDir)
	dest := filepath.Join(localBookPath(book, workDir), "index")
	if dirExists(dest) {
		return nil
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := f.Name
		if strings.Contains(name, "..") {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0o755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		rc, err := f.Open()
		if err != nil {
			continue
		}
		out, err := os.Create(target)
		if err == nil {
			_, _ = io.Copy(out, rc)
			_ = out.Close()
		}
		_ = rc.Close()
	}
	return nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func readZipFile(zr *zip.ReadCloser, name string) ([]byte, bool) {
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, name) {
			rc, err := f.Open()
			if err != nil {
				return nil, false
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			return b, true
		}
	}
	return nil, false
}

func findOPF(zr *zip.ReadCloser) (string, *opfDoc) {
	if b, ok := readZipFile(zr, "META-INF/container.xml"); ok {
		var container struct {
			Rootfiles []struct {
				FullPath string `xml:"full-path,attr"`
			} `xml:"rootfiles>rootfile"`
		}
		if xml.Unmarshal(b, &container) == nil && len(container.Rootfiles) > 0 {
			full := container.Rootfiles[0].FullPath
			if data, ok := readZipFile(zr, full); ok {
				var doc opfDoc
				if xml.Unmarshal(data, &doc) == nil {
					return full, &doc
				}
			}
		}
	}
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".opf") {
			if data, ok := readZipFile(zr, f.Name); ok {
				var doc opfDoc
				if xml.Unmarshal(data, &doc) == nil {
					return f.Name, &doc
				}
			}
		}
	}
	return "", nil
}

// EpubChapterList parses the epub toc/spine into chapters.
func EpubChapterList(book *model.Book, workDir string) ([]*model.BookChapter, error) {
	zipPath := epubArchivePath(book, workDir)
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("epub文件错误")
	}
	defer zr.Close()

	opfPath, opf := findOPF(zr)
	if opf == nil {
		return nil, fmt.Errorf("epub格式错误")
	}
	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	// Metadata
	if book.Name == "" {
		book.Name = strings.TrimSpace(opf.Metadata.Title)
	}
	if book.Author == "" {
		book.Author = strings.TrimSpace(opf.Metadata.Creator)
	}
	if book.Intro == nil || *book.Intro == "" {
		intro := util.HTMLFormat(stripTags(opf.Metadata.Description))
		if intro != "" {
			book.Intro = &intro
		}
	}

	baseByID := map[string]opfItem{}
	for _, item := range opf.Manifest.Items {
		baseByID[item.ID] = item
	}

	mode := ""
	if book.TocUrl != "" {
		mode = book.TocUrl
	}
	var tocChapters []*model.BookChapter

	// EPUB3 nav
	for _, item := range opf.Manifest.Items {
		if strings.Contains(item.Properties, "nav") {
			navPath := path.Join(opfDir, item.Href)
			if data, ok := readZipFile(zr, navPath); ok {
				tocChapters = parseNav(data, path.Dir(navPath))
			}
			break
		}
	}
	// EPUB2 ncx
	if len(tocChapters) == 0 {
		for _, item := range opf.Manifest.Items {
			if strings.Contains(strings.ToLower(item.MediaType), "ncx") {
				ncxPath := path.Join(opfDir, item.Href)
				if data, ok := readZipFile(zr, ncxPath); ok {
					tocChapters = parseNCX(data, path.Dir(ncxPath), opfDir)
				}
				break
			}
		}
	}

	// Spine
	var spineChapters []*model.BookChapter
	for _, sp := range opf.Spine.Items {
		item, ok := baseByID[sp.IDRef]
		if !ok {
			continue
		}
		if !strings.Contains(item.MediaType, "html") && !strings.HasSuffix(item.Href, ".xhtml") && !strings.HasSuffix(item.Href, ".html") {
			continue
		}
		u := path.Join(opfDir, item.Href)
		spineChapters = append(spineChapters, &model.BookChapter{URL: u, Title: path.Base(item.Href)})
	}
	if mode == "" {
		mode = "toc"
	}
	switch mode {
	case "spine":
		tocChapters = spineChapters
	case "toc+spine", "spin+toc", "spine+toc":
		if len(tocChapters) == 0 {
			tocChapters = spineChapters
		}
	default:
		if len(tocChapters) == 0 {
			tocChapters = spineChapters
		}
	}
	if len(tocChapters) == 0 {
		return nil, fmt.Errorf("epub目录为空")
	}
	for i, c := range tocChapters {
		c.Index = i
		c.BookUrl = book.BookUrl
		c.URL = path.Clean(c.URL)
	}
	indexTitles := map[string]string{}
	if len(tocChapters) > 0 && len(spineChapters) > 0 {
		for _, c := range tocChapters {
			indexTitles[c.URL] = c.Title
		}
		for _, c := range spineChapters {
			if t, ok := indexTitles[c.URL]; ok && t == "" {
				c.Title = t
			}
		}
	}
	book.TotalChapterNum = len(tocChapters)
	last := tocChapters[len(tocChapters)-1].Title
	book.LatestChapterTitle = &last
	return tocChapters, nil
}

func parseNCX(data []byte, navDir, opfDir string) []*model.BookChapter {
	var doc ncxDoc
	if xml.Unmarshal(data, &doc) != nil {
		return nil
	}
	var out []*model.BookChapter
	var walk func(points []ncxPoint)
	walk = func(points []ncxPoint) {
		for _, p := range points {
			src := p.Src
			if src != "" {
				u := path.Join(navDir, cleanHref(src))
				out = append(out, &model.BookChapter{URL: u, Title: strings.TrimSpace(p.Text)})
			}
			if len(p.Children) > 0 {
				walk(p.Children)
			}
		}
	}
	walk(doc.Points)
	return out
}

func parseNav(data []byte, navDir string) []*model.BookChapter {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil
	}
	var out []*model.BookChapter
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inToc bool) {
		if n.Type == html.ElementNode && n.Data == "nav" {
			for _, a := range n.Attr {
				if strings.HasSuffix(strings.ToLower(a.Key), "type") && strings.EqualFold(a.Val, "toc") {
					inToc = true
				}
			}
			if inToc {
				var collect func(*html.Node)
				collect = func(x *html.Node) {
					if x.Type == html.ElementNode && x.Data == "a" {
						href := ""
						for _, a := range x.Attr {
							if a.Key == "href" {
								href = a.Val
							}
						}
						if href != "" {
							out = append(out, &model.BookChapter{
								URL:   path.Join(navDir, cleanHref(href)),
								Title: strings.TrimSpace(nodeText(x)),
							})
						}
					}
					for c := x.FirstChild; c != nil; c = c.NextSibling {
						collect(c)
					}
				}
				collect(n)
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inToc)
		}
	}
	walk(doc, false)
	return out
}

func cleanHref(href string) string {
	if idx := strings.Index(href, "#"); idx >= 0 {
		return href // keep fragment for anchor support
	}
	return href
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CbzChapterList lists images inside a cbz archive.
func CbzChapterList(book *model.Book, workDir string) ([]*model.BookChapter, error) {
	zipPath := epubArchivePath(book, workDir)
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("cbz文件错误")
	}
	defer zr.Close()
	var images []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
			images = append(images, f.Name)
		}
	}
	sort.Strings(images)
	var chapters []*model.BookChapter
	for i, img := range images {
		chapters = append(chapters, &model.BookChapter{
			URL: img, Title: fmt.Sprintf("第%d页", i+1), Index: i, BookUrl: book.BookUrl,
		})
	}
	book.TotalChapterNum = len(chapters)
	return chapters, nil
}
