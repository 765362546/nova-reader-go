package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds the runtime configuration of the app. Every field can be set
// through a READER_* environment variable (see README); the zero value is
// replaced by the defaults in Load.
type Config struct {
	StoragePath           string
	ShowUI                bool
	Debug                 bool
	Packaged              bool
	Secure                bool
	InviteCode            string
	SecureKey             string
	CacheChapterContent   bool
	UserLimit             int
	UserBookLimit         int
	DebugLog              bool
	AutoClearInactiveUser int
	ExportUseReplace      bool
	ExportCharset         string
	ExportNoChapterName   bool
	ExportPictureFile     bool
	Fonts                 string
}

type Server struct {
	Port   int
	WebUrl string
}

type App struct {
	Config
	Server
	WorkDir string
	WebDir  string
}

// Load builds the configuration from environment variables only. This keeps
// deployment idiomatic for Go services (12-factor style): no config file to
// ship, and containers/process managers can set everything directly.
func Load() *App {
	app := &App{
		Config: Config{
			StoragePath:         "storage",
			CacheChapterContent: true,
			UserLimit:           50,
			UserBookLimit:       200,
			ExportCharset:       "UTF-8",
		},
		Server: Server{Port: 8080},
	}

	app.StoragePath = envString("READER_STORAGE_PATH", app.StoragePath)
	app.ShowUI = envBool("READER_SHOW_UI", app.ShowUI)
	app.Debug = envBool("READER_DEBUG", app.Debug)
	app.Packaged = envBool("READER_PACKAGED", app.Packaged)
	app.Secure = envBool("READER_SECURE", app.Secure)
	app.InviteCode = envString("READER_INVITE_CODE", app.InviteCode)
	app.SecureKey = envString("READER_SECURE_KEY", app.SecureKey)
	app.CacheChapterContent = envBool("READER_CACHE_CHAPTER_CONTENT", app.CacheChapterContent)
	app.UserLimit = envInt("READER_USER_LIMIT", app.UserLimit)
	app.UserBookLimit = envInt("READER_USER_BOOK_LIMIT", app.UserBookLimit)
	app.DebugLog = envBool("READER_DEBUG_LOG", app.DebugLog)
	app.AutoClearInactiveUser = envInt("READER_AUTO_CLEAR_INACTIVE_USER", app.AutoClearInactiveUser)
	app.ExportUseReplace = envBool("READER_EXPORT_USE_REPLACE", app.ExportUseReplace)
	app.ExportCharset = envString("READER_EXPORT_CHARSET", app.ExportCharset)
	app.ExportNoChapterName = envBool("READER_EXPORT_NO_CHAPTER_NAME", app.ExportNoChapterName)
	app.ExportPictureFile = envBool("READER_EXPORT_PICTURE_FILE", app.ExportPictureFile)
	app.Fonts = envString("READER_FONTS", app.Fonts)

	app.Port = envInt("READER_PORT", app.Port)
	app.WebUrl = envString("READER_WEB_URL", app.WebUrl)

	workDir, _ := os.Getwd()
	app.WorkDir = workDir
	app.WebDir = filepath.Join(workDir, "web")

	if app.Port <= 0 {
		app.Port = 8080
	}
	if app.UserLimit <= 0 {
		app.UserLimit = 50
	}
	if app.UserBookLimit <= 0 {
		app.UserBookLimit = 200
	}
	if app.ExportCharset == "" {
		app.ExportCharset = "UTF-8"
	}
	if app.StoragePath == "" {
		app.StoragePath = "storage"
	}
	return app
}

func (a *App) StorageRoot() string {
	if filepath.IsAbs(a.StoragePath) {
		return a.StoragePath
	}
	return filepath.Join(a.WorkDir, a.StoragePath)
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}
