package app

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/LemonPG/115driver/pkg/driver"
	"golang.org/x/time/rate"
)

type Config struct {
	ListenAddr   string `json:"listenAddr"`
	UrlPrefix    string `json:"urlPrefix"`
	OutputDir    string `json:"outputDir"`
	ScanInterval int    `json:"scanIntervalMinutes"`

	// SelectedCID is the folder CID chosen by user
	SelectedCID string `json:"selectedCid"`

	// 待整理目录CID
	PendingCID string `json:"pendingCid,omitempty"`

	// 已存在目录CID
	ExistingCID string `json:"existingCid,omitempty"`

	// 冗余数据目录CID
	RedundantCID string `json:"redundantCid,omitempty"`

	// MaxDepth controls recursive listing depth. 0 means only current folder.
	MaxDepth int `json:"maxDepth"`

	// RateLimit limits request rate per second (0 means no limit)
	RateLimit float64 `json:"rateLimit"`

	// File extensions to scan (empty means all files)
	ScanExtensions []string `json:"scanExtensions,omitempty"`

	// File extensions to download directly instead of generating .strm files
	DownloadExtensions []string `json:"downloadExtensions,omitempty"`

	// Database configuration
	Database DatabaseConfig `json:"database"`

	// Auth
	Credential *driver.Credential `json:"credential,omitempty"`

	// LoginType records which app was used for login (web, android, ios, tv, alipaymini, wechatmini)
	LoginType string `json:"loginType,omitempty"`

	// Emby reverse proxy configuration
	EmbyProxy EmbyProxyConfig `json:"embyProxy,omitempty"`

	// Emby API key for authentication
	EmbyApiKey string `json:"embyApiKey,omitempty"`

	// Emby redirect configuration for 302直链功能
	EmbyRedirect EmbyRedirectConfig `json:"embyRedirect,omitempty"`

	// TMDB (The Movie Database) configuration
	TMDB TMDBConfig `json:"tmdb,omitempty"`
}

// EmbyProxyConfig holds Emby reverse proxy configuration
type EmbyProxyConfig struct {
	Enabled   bool   `json:"enabled"`
	Target    string `json:"target"`    // Emby server address, e.g., "http://localhost:8096"
	ProxyPort string `json:"proxyPort"` // Port for proxy server, e.g., ":17616"
	Path      string `json:"path"`      // Proxy path prefix, e.g., "/" for root
	StripPath bool   `json:"stripPath"` // Whether to strip the path prefix when proxying
}

// EmbyRedirectConfig holds Emby 302 redirect configuration
type EmbyRedirectConfig struct {
	Enabled                 bool                    `json:"enabled"`
	FallbackUseOriginal     bool                    `json:"fallbackUseOriginal"`
	RouteCacheEnable        bool                    `json:"routeCacheEnable"`
	RouteCacheL2Enable      bool                    `json:"routeCacheL2Enable"`
	PlaybackInfoConfig      bool                    `json:"playbackInfoConfig"`
	EmbyNotificationsAdmin  EmbyNotificationsConfig `json:"embyNotificationsAdmin"`
	EmbyRedirectSendMessage EmbyMessageConfig       `json:"embyRedirectSendMessage"`
}

// EmbyNotificationsConfig holds Emby notifications configuration
type EmbyNotificationsConfig struct {
	Enable     bool   `json:"enable"`
	Name       string `json:"name"`
	IncludeUrl bool   `json:"includeUrl"`
}

// EmbyMessageConfig holds Emby message configuration
type EmbyMessageConfig struct {
	Enable    bool   `json:"enable"`
	Header    string `json:"header"`
	TimeoutMs int    `json:"timeoutMs"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Type     string `json:"type"`     // "sqlite", "mysql", "postgres"
	Host     string `json:"host"`     // for mysql/postgres
	Port     int    `json:"port"`     // for mysql/postgres
	Name     string `json:"name"`     // database name
	User     string `json:"user"`     // username
	Password string `json:"password"` // password
	DataDir  string `json:"dataDir"`  // for sqlite, directory to store db file
}

func defaultConfig() Config {
	return Config{
		ListenAddr:   "127.0.0.1:17615",
		UrlPrefix:    "",
		OutputDir:    "",
		ScanInterval: 5,
		SelectedCID:  "",
		MaxDepth:     5,
		RateLimit:    0.0, // 0 means no limit
		Database: DatabaseConfig{
			Type:    "sqlite",
			DataDir: ".",
		},
		Credential: nil,
		EmbyProxy: EmbyProxyConfig{
			Enabled:   false,
			Target:    "http://localhost:8096",
			ProxyPort: ":17616",
			Path:      "/",
			StripPath: true,
		},
		EmbyRedirect: EmbyRedirectConfig{
			Enabled:             false,
			FallbackUseOriginal: true,
			RouteCacheEnable:    false,
			RouteCacheL2Enable:  false,
			PlaybackInfoConfig:  false,
			EmbyNotificationsAdmin: EmbyNotificationsConfig{
				Enable:     false,
				Name:       "FilmFlow",
				IncludeUrl: false,
			},
			EmbyRedirectSendMessage: EmbyMessageConfig{
				Enable:    false,
				Header:    "FilmFlow Redirect",
				TimeoutMs: 5000,
			},
		},
	}
}

func (a *App) loadConfig() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	b, err := os.ReadFile(a.cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}

	// apply defaults
	def := defaultConfig()
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = def.ListenAddr
	}
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = def.ScanInterval
	}
	if cfg.SelectedCID == "" {
		cfg.SelectedCID = def.SelectedCID
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = def.MaxDepth
	}

	a.cfg = cfg

	// initialize rate limiter
	if cfg.RateLimit > 0 {
		a.limiter = rate.NewLimiter(rate.Limit(cfg.RateLimit), 1)
	} else {
		a.limiter = nil // no limit
	}

	return nil
}

func (a *App) saveConfigLocked() error {
	b, err := json.MarshalIndent(a.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.cfgPath, b, 0o600)
}
