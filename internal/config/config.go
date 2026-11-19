package config

import (
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config mirrors reader.app / reader.server from application.yml.
type Config struct {
	StoragePath         string `yaml:"storagePath"`
	ShowUI              bool   `yaml:"showUI"`
	Debug               bool   `yaml:"debug"`
	Packaged            bool   `yaml:"packaged"`
	Secure              bool   `yaml:"secure"`
	InviteCode          string `yaml:"inviteCode"`
	SecureKey           string `yaml:"secureKey"`
	CacheChapterContent bool   `yaml:"cacheChapterContent"`
	UserLimit           int    `yaml:"userLimit"`
	UserBookLimit       int    `yaml:"userBookLimit"`
	DebugLog            bool   `yaml:"debugLog"`
	AutoClearInactiveUser int  `yaml:"autoClearInactiveUser"`
	ExportUseReplace    bool   `yaml:"exportUseReplace"`
	ExportCharset       string `yaml:"exportCharset"`
	ExportNoChapterName bool   `yaml:"exportNoChapterName"`
	ExportPictureFile   bool   `yaml:"exportPictureFile"`
}

type Server struct {
	Port   int    `yaml:"port"`
	WebUrl string `yaml:"webUrl"`
}

type fileConfig struct {
	Reader struct {
		App    Config `yaml:"app"`
		Server Server `yaml:"server"`
	} `yaml:"reader"`
}

type App struct {
	Config
	Server
	WorkDir string
	WebDir  string
}

func Load() *App {
	app := &App{
		Config: Config{
			StoragePath:         "storage",
			UserLimit:           50,
			UserBookLimit:       200,
			ExportCharset:       "UTF-8",
			CacheChapterContent: true,
		},
		Server: Server{Port: 8080},
	}

	workDir, _ := os.Getwd()
	app.WorkDir = workDir
	app.WebDir = filepath.Join(workDir, "web")

	// Match Spring Boot's config file names.
	for _, name := range []string{"application.yml", "application.yaml", filepath.Join("server", "conf", "application.properties")} {
		path := filepath.Join(workDir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if filepath.Ext(path) == ".yml" || filepath.Ext(path) == ".yaml" {
			var fc fileConfig
			if b, err := os.ReadFile(path); err == nil {
				if err := yaml.Unmarshal(b, &fc); err == nil {
					app.Config = fc.Reader.App
					app.Server = fc.Reader.Server
				}
			}
		}
	}
	// Environment overrides (useful in containers).
	if v := os.Getenv("READER_STORAGE_PATH"); v != "" {
		app.StoragePath = v
	}
	if v := os.Getenv("READER_SECURE"); v != "" {
		app.Secure = v == "true" || v == "1"
	}
	if v := os.Getenv("READER_SECURE_KEY"); v != "" {
		app.SecureKey = v
	}
	if v := os.Getenv("READER_INVITE_CODE"); v != "" {
		app.InviteCode = v
	}
	if v := os.Getenv("READER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			app.Port = p
		}
	}
	if app.Server.Port == 0 {
		app.Server.Port = 8080
	}
	if app.UserLimit <= 0 {
		app.UserLimit = 50
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
