package localbook

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/765362546/novel-reader-go/internal/model"
	"github.com/765362546/novel-reader-go/internal/util"
)

var (
	bookAuthorPattern1 = regexp.MustCompile(`(.*?)[\s_-]+(?:作者|作 者)[:：\s]*(.*)`)
	bookAuthorPattern2 = regexp.MustCompile(`(.*?)[\(（](.*?)[\)）]$`)
	bookAuthorPattern3 = regexp.MustCompile(`(.*?)\s+by\s+(.*)`)
)

func splitNameAuthor(fileName string) (string, string) {
	name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if m := bookAuthorPattern1.FindStringSubmatch(name); m != nil {
		return util.FormatBookName(m[1]), util.FormatBookAuthor(m[2])
	}
	if m := bookAuthorPattern2.FindStringSubmatch(name); m != nil {
		return util.FormatBookName(m[1]), util.FormatBookAuthor(m[2])
	}
	if m := bookAuthorPattern3.FindStringSubmatch(name); m != nil {
		return util.FormatBookName(m[1]), util.FormatBookAuthor(m[2])
	}
	return util.FormatBookName(name), ""
}

func extOf(book *model.Book) string {
	u := strings.ToLower(book.BookUrl)
	if idx := strings.IndexAny(u, "?#"); idx >= 0 {
		u = u[:idx]
	}
	ext := filepath.Ext(u)
	if ext == "" && book.OriginName != "" {
		ext = filepath.Ext(strings.ToLower(book.OriginName))
	}
	return ext
}

func IsEpub(book *model.Book) bool { return extOf(book) == ".epub" }
func IsCbz(book *model.Book) bool  { return extOf(book) == ".cbz" }
func IsUmd(book *model.Book) bool  { return extOf(book) == ".umd" }
func IsTxt(book *model.Book) bool {
	ext := extOf(book)
	return ext == ".txt" || ext == ".text" || ext == ".md"
}

// GetChapterList returns the local book's chapters.
func GetChapterList(book *model.Book, workDir, txtRule string) ([]*model.BookChapter, error) {
	switch {
	case IsEpub(book):
		book.TocUrl = defaultTocUrl(book.TocUrl, "toc")
		return EpubChapterList(book, workDir)
	case IsCbz(book):
		return CbzChapterList(book, workDir)
	case IsUmd(book):
		return nil, fmt.Errorf("暂不支持UMD格式")
	default:
		return TextChapterList(book, workDir, txtRule, false)
	}
}

func defaultTocUrl(cur, def string) string {
	if strings.TrimSpace(cur) == "" || cur == "toc" || cur == "spine" {
		return def
	}
	return cur
}

// GetContent returns local chapter content for TXT books. EPUB/CBZ are served via /epub URLs.
func GetContent(book *model.Book, chapter *model.BookChapter, workDir string) (string, error) {
	switch {
	case IsEpub(book):
		return epubContentURL(book, chapter), nil
	case IsCbz(book):
		return cbzContentURL(book, chapter), nil
	case IsUmd(book):
		return "", fmt.Errorf("暂不支持UMD格式")
	default:
		return TextContent(book, chapter, workDir)
	}
}

func epubContentURL(book *model.Book, chapter *model.BookChapter) string {
	root := ""
	if strings.HasSuffix(strings.ToLower(book.OriginName), ".epub") {
		root = "" // extracted archive root
	}
	base := strings.Replace(book.BookUrl, "storage/data/", "/epub/", 1)
	if base == "" {
		base = strings.Replace(book.OriginName, "storage/data/", "/epub/", 1)
	}
	if root == "" {
		return base + "/index/" + chapter.URL
	}
	return base + "/index/" + root + "/" + chapter.URL
}

func cbzContentURL(book *model.Book, chapter *model.BookChapter) string {
	base := strings.Replace(book.BookUrl, "storage/data/", "/epub/", 1)
	return base + "/index/" + chapter.URL
}

// InitLocalBook creates a Book for a local file, matching BookController.initLocalBook.
func InitLocalBook(localFileURL, localFilePath string) *model.Book {
	book := model.NewBook()
	book.BookUrl = localFileURL
	book.OriginName = localFilePath
	book.Origin = model.BookTypeLocal
	book.Name, book.Author = splitNameAuthor(filepath.Base(localFilePath))
	if strings.HasPrefix(strings.ToLower(localFilePath), "storage/assets/") {
		book.OriginName = strings.TrimPrefix(localFilePath, "")
	}
	return book
}
