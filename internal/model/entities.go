package model

import (
	"encoding/json"
	"time"
)

// Book type constants (BookType.kt).
const (
	BookTypeText       = 0
	BookTypeAudio      = 1
	BookTypeImage      = 2
	BookTypeFile       = 3
	BookTypeLocal      = "loc_book"
	BookTypeWebDav     = "webdav"
	BookTypeLocalStore = "local_store"
)

// ReadConfig mirrors Book.ReadConfig.
type ReadConfig struct {
	ReverseToc     bool    `json:"reverseToc"`
	PageAnim       int     `json:"pageAnim"`
	ReSegment      bool    `json:"reSegment"`
	ImageStyle     *string `json:"imageStyle"`
	UseReplaceRule bool    `json:"useReplaceRule"`
	DelTag         int64   `json:"delTag"`
}

// Book mirrors io.legado.app.data.entities.Book.
type Book struct {
	BookUrl            string      `json:"bookUrl"`
	TocUrl             string      `json:"tocUrl"`
	Origin             string      `json:"origin"`
	OriginName         string      `json:"originName"`
	Name               string      `json:"name"`
	Author             string      `json:"author"`
	Kind               string      `json:"kind,omitempty"`
	CustomTag          string      `json:"customTag,omitempty"`
	CoverUrl           string      `json:"coverUrl,omitempty"`
	CustomCoverUrl     string      `json:"customCoverUrl,omitempty"`
	Intro              string      `json:"intro,omitempty"`
	CustomIntro        string      `json:"customIntro,omitempty"`
	Charset            string      `json:"charset,omitempty"`
	Type               int         `json:"type"`
	Group              int         `json:"group"`
	LatestChapterTitle string      `json:"latestChapterTitle,omitempty"`
	LatestChapterTime  int64       `json:"latestChapterTime"`
	LastCheckTime      int64       `json:"lastCheckTime"`
	LastCheckCount     int         `json:"lastCheckCount"`
	TotalChapterNum    int         `json:"totalChapterNum"`
	DurChapterTitle    string      `json:"durChapterTitle,omitempty"`
	DurChapterIndex    int         `json:"durChapterIndex"`
	DurChapterPos      int         `json:"durChapterPos"`
	DurChapterTime     int64       `json:"durChapterTime"`
	WordCount          string      `json:"wordCount,omitempty"`
	CanUpdate          bool        `json:"canUpdate"`
	Order              int         `json:"order"`
	OriginOrder        int         `json:"originOrder"`
	UseReplaceRule     bool        `json:"useReplaceRule"`
	Variable           *string     `json:"variable"`
	ReadConfig         *ReadConfig `json:"readConfig"`
}

func NewBook() *Book {
	now := time.Now().UnixMilli()
	return &Book{
		Origin:            BookTypeLocal,
		LatestChapterTime: now,
		LastCheckTime:     now,
		DurChapterTime:    now,
		CanUpdate:         true,
		UseReplaceRule:    true,
	}
}

// UnmarshalJSON applies Kotlin default values for omitted fields.
func (b *Book) UnmarshalJSON(data []byte) error {
	type alias Book
	def := alias(*NewBook())
	if err := json.Unmarshal(data, &def); err != nil {
		return err
	}
	*b = Book(def)
	return nil
}

// SearchBook mirrors SearchBook.kt.
type SearchBook struct {
	BookUrl            string  `json:"bookUrl"`
	Origin             string  `json:"origin"`
	OriginName         string  `json:"originName"`
	Type               int     `json:"type"`
	Name               string  `json:"name"`
	Author             string  `json:"author"`
	Kind               string  `json:"kind,omitempty"`
	CoverUrl           string  `json:"coverUrl,omitempty"`
	Intro              string  `json:"intro,omitempty"`
	WordCount          string  `json:"wordCount,omitempty"`
	LatestChapterTitle string  `json:"latestChapterTitle,omitempty"`
	TocUrl             string  `json:"tocUrl"`
	Time               int64   `json:"time"`
	Variable           *string `json:"variable"`
	OriginOrder        int     `json:"originOrder"`
}

func NewSearchBook() *SearchBook {
	return &SearchBook{Origin: BookTypeLocal, Time: time.Now().UnixMilli()}
}

func (b *SearchBook) UnmarshalJSON(data []byte) error {
	type alias SearchBook
	def := alias(*NewSearchBook())
	if err := json.Unmarshal(data, &def); err != nil {
		return err
	}
	*b = SearchBook(def)
	return nil
}

// BookChapter mirrors BookChapter.kt.
type BookChapter struct {
	URL             string  `json:"url"`
	Title           string  `json:"title"`
	IsVolume        bool    `json:"isVolume"`
	BaseURL         string  `json:"baseUrl"`
	BookUrl         string  `json:"bookUrl"`
	Index           int     `json:"index"`
	ResourceUrl     *string `json:"resourceUrl"`
	Tag             *string `json:"tag"`
	Start           int64   `json:"start"`
	End             int64   `json:"end"`
	StartFragmentId *string `json:"startFragmentId"`
	EndFragmentId   *string `json:"endFragmentId"`
	Variable        *string `json:"variable"`
}

// BookGroup mirrors BookGroup.kt.
type BookGroup struct {
	GroupId   int64  `json:"groupId"`
	GroupName string `json:"groupName"`
	Order     int    `json:"order"`
	Show      bool   `json:"show"`
}

// Rule structs -------------------------------------------------------------

type SearchRule struct {
	BookList    *string `json:"bookList"`
	Name        *string `json:"name"`
	Author      *string `json:"author"`
	Intro       *string `json:"intro"`
	Kind        *string `json:"kind"`
	BookUrl     *string `json:"bookUrl"`
	CoverUrl    *string `json:"coverUrl"`
	LastChapter *string `json:"lastChapter"`
	UpdateTime  *string `json:"updateTime"`
	WordCount   *string `json:"wordCount"`
}

type ExploreRule struct {
	BookList    *string `json:"bookList"`
	Name        *string `json:"name"`
	Author      *string `json:"author"`
	Intro       *string `json:"intro"`
	Kind        *string `json:"kind"`
	BookUrl     *string `json:"bookUrl"`
	CoverUrl    *string `json:"coverUrl"`
	LastChapter *string `json:"lastChapter"`
	UpdateTime  *string `json:"updateTime"`
	WordCount   *string `json:"wordCount"`
}

type BookInfoRule struct {
	Init        *string `json:"init"`
	Name        *string `json:"name"`
	Author      *string `json:"author"`
	Intro       *string `json:"intro"`
	Kind        *string `json:"kind"`
	CoverUrl    *string `json:"coverUrl"`
	LastChapter *string `json:"lastChapter"`
	UpdateTime  *string `json:"updateTime"`
	TocUrl      *string `json:"tocUrl"`
	WordCount   *string `json:"wordCount"`
	CanReName   *string `json:"canReName"`
}

type TocRule struct {
	PreUpdateJs *string `json:"preUpdateJs"`
	ChapterList *string `json:"chapterList"`
	ChapterName *string `json:"chapterName"`
	ChapterUrl  *string `json:"chapterUrl"`
	IsVolume    *string `json:"isVolume"`
	IsVip       *string `json:"isVip"`
	UpdateTime  *string `json:"updateTime"`
	NextTocUrl  *string `json:"nextTocUrl"`
}

type ContentRule struct {
	Content        *string `json:"content"`
	ReplaceRegex   *string `json:"replaceRegex"`
	NextContentUrl *string `json:"nextContentUrl"`
	WebJs          *string `json:"webJs"`
	SourceRegex    *string `json:"sourceRegex"`
	ImageStyle     *string `json:"imageStyle"`
}

// BookSource mirrors BookSource.kt.
type BookSource struct {
	BookSourceName    string        `json:"bookSourceName"`
	BookSourceGroup   *string       `json:"bookSourceGroup"`
	BookSourceUrl     string        `json:"bookSourceUrl"`
	BookSourceType    int           `json:"bookSourceType"`
	BookUrlPattern    *string       `json:"bookUrlPattern"`
	CustomOrder       int           `json:"customOrder"`
	Enabled           bool          `json:"enabled"`
	EnabledExplore    bool          `json:"enabledExplore"`
	ConcurrentRate    *string       `json:"concurrentRate"`
	Header            *string       `json:"header"`
	LoginUrl          *string       `json:"loginUrl"`
	LoginCheckJs      *string       `json:"loginCheckJs"`
	LastUpdateTime    int64         `json:"lastUpdateTime"`
	Weight            int           `json:"weight"`
	ExploreUrl        *string       `json:"exploreUrl"`
	RuleExplore       *ExploreRule  `json:"ruleExplore"`
	SearchUrl         *string       `json:"searchUrl"`
	RuleSearch        *SearchRule   `json:"ruleSearch"`
	RuleBookInfo      *BookInfoRule `json:"ruleBookInfo"`
	RuleToc           *TocRule      `json:"ruleToc"`
	RuleContent       *ContentRule  `json:"ruleContent"`
	BookSourceComment *string       `json:"bookSourceComment"`
	RespondTime       int64         `json:"respondTime"`
}

func NewBookSource() *BookSource {
	return &BookSource{
		Enabled:        true,
		EnabledExplore: true,
		RespondTime:    180000,
	}
}

func (s *BookSource) UnmarshalJSON(data []byte) error {
	type alias BookSource
	def := alias(*NewBookSource())
	if err := json.Unmarshal(data, &def); err != nil {
		return err
	}
	*s = BookSource(def)
	return nil
}

// RssSource mirrors RssSource.kt.
type RssSource struct {
	SourceUrl           string  `json:"sourceUrl"`
	SourceName          string  `json:"sourceName"`
	SourceGroup         *string `json:"sourceGroup"`
	SourceType          int     `json:"sourceType"`
	SourceIcon          *string `json:"sourceIcon"`
	SourceComment       *string `json:"sourceComment"`
	Enabled             bool    `json:"enabled"`
	EnabledExplore      bool    `json:"enabledExplore"`
	ConcurrentRate      *string `json:"concurrentRate"`
	Header              *string `json:"header"`
	LoginUrl            *string `json:"loginUrl"`
	LastUpdateTime      int64   `json:"lastUpdateTime"`
	CustomOrder         int     `json:"customOrder"`
	SortUrl             *string `json:"sortUrl"`
	SortName            *string `json:"sortName"`
	SortList            *string `json:"sortList"`
	RuleArticles        *string `json:"ruleArticles"`
	RuleNextPage        *string `json:"ruleNextPage"`
	RuleTitle           *string `json:"ruleTitle"`
	RulePubDate         *string `json:"rulePubDate"`
	RuleDescription     *string `json:"ruleDescription"`
	RuleLink            *string `json:"ruleLink"`
	RuleImage           *string `json:"ruleImage"`
	RuleContent         *string `json:"ruleContent"`
	RuleContentNextPage *string `json:"ruleContentNextPage"`
}

func NewRssSource() *RssSource {
	return &RssSource{Enabled: true, EnabledExplore: true}
}

func (s *RssSource) UnmarshalJSON(data []byte) error {
	type alias RssSource
	def := alias(*NewRssSource())
	if err := json.Unmarshal(data, &def); err != nil {
		return err
	}
	*s = RssSource(def)
	return nil
}

// RssArticle mirrors RssArticle.kt.
type RssArticle struct {
	Origin        string  `json:"origin"`
	OriginName    string  `json:"originName"`
	Sort          string  `json:"sort"`
	Link          string  `json:"link"`
	Title         string  `json:"title"`
	Order         int64   `json:"order"`
	Author        *string `json:"author"`
	PubDate       *string `json:"pubDate"`
	Description   *string `json:"description"`
	Content       *string `json:"content"`
	Image         *string `json:"image"`
	Read          bool    `json:"read"`
	Star          bool    `json:"star"`
	Variable      *string `json:"variable"`
	DurContentPos int     `json:"durContentPos"`
}

// ReplaceRule mirrors ReplaceRule.kt.
type ReplaceRule struct {
	Name        string `json:"name"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	IsRegex     bool   `json:"isRegex"`
	IsEnabled   bool   `json:"isEnabled"`
	Order       int    `json:"order"`
	Id          int64  `json:"id"`
}

func NewReplaceRule() *ReplaceRule {
	return &ReplaceRule{IsEnabled: true, IsRegex: true}
}

func (r *ReplaceRule) UnmarshalJSON(data []byte) error {
	type alias ReplaceRule
	def := alias(*NewReplaceRule())
	if err := json.Unmarshal(data, &def); err != nil {
		return err
	}
	*r = ReplaceRule(def)
	return nil
}

// Bookmark mirrors Bookmark.kt.
type Bookmark struct {
	Name         string `json:"name"`
	BookName     string `json:"bookName"`
	BookAuthor   string `json:"bookAuthor"`
	ChapterIndex int    `json:"chapterIndex"`
	ChapterPos   int    `json:"chapterPos"`
	ChapterName  string `json:"chapterName"`
	BookText     string `json:"bookText"`
	Content      string `json:"content"`
	Time         int64  `json:"time"`
}

func NewBookmark() *Bookmark {
	return &Bookmark{Time: time.Now().UnixMilli()}
}

func (b *Bookmark) UnmarshalJSON(data []byte) error {
	type alias Bookmark
	def := alias(*NewBookmark())
	if err := json.Unmarshal(data, &def); err != nil {
		return err
	}
	*b = Bookmark(def)
	return nil
}

// TxtTocRule mirrors TxtTocRule.kt.
type TxtTocRule struct {
	Id           int64   `json:"id"`
	Enable       bool    `json:"enable"`
	Name         string  `json:"name"`
	Rule         string  `json:"rule"`
	SerialNumber int     `json:"serialNumber"`
	Example      *string `json:"example"`
}

// SearchKeyword mirrors SearchKeyword.kt.
type SearchKeyword struct {
	Word    string `json:"word"`
	Usage   int    `json:"usage"`
	LastUse int64  `json:"lastUse"`
}

// User mirrors com.htmake.reader.entity.User (snake_case).
type User struct {
	Username         string           `json:"username"`
	Password         string           `json:"password"`
	Salt             string           `json:"salt,omitempty"`
	Token            string           `json:"token"`
	LastLoginAt      int64            `json:"last_login_at"`
	CreatedAt        int64            `json:"created_at"`
	ExpireAt         int64            `json:"expire_at"`
	EnableWebdav     bool             `json:"enable_webdav"`
	EnableLocalStore bool             `json:"enable_local_store"`
	TokenMap         map[string]int64 `json:"token_map"`
}

// SearchResult mirrors SearchResult.kt (content search result).
type SearchResult struct {
	ResultCount              int    `json:"resultCount"`
	ResultCountWithinChapter int    `json:"resultCountWithinChapter"`
	ResultText               string `json:"resultText"`
	ChapterTitle             string `json:"chapterTitle"`
	Query                    string `json:"query"`
	PageSize                 int    `json:"pageSize"`
	ChapterIndex             int    `json:"chapterIndex"`
	PageIndex                int    `json:"pageIndex"`
	QueryIndexInResult       int    `json:"queryIndexInResult"`
	QueryIndexInChapter      int    `json:"queryIndexInChapter"`
}
