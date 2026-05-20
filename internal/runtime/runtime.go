// Package runtime implements the plugin's Runtime gRPC server. It embeds
// runtimedefault.Server (which handles BindHostBroker) and adds GetManifest
// and Configure handlers. Configure invokes a callback supplied by main.go
// so the plugin can (re)wire its pool/store/clients.
package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"
	"github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginsdk/runtimedefault"
)

// Config is the parsed plugin global config.
type Config struct {
	DatabaseURL           string `json:"database_url,omitempty"`
	BaseURL               string `json:"base_url"`
	APIKey                string `json:"api_key,omitempty"`
	DefaultCoverSize      string `json:"default_cover_size"`
	RequestQualityProfile string `json:"request_quality_profile,omitempty"`
	EnableAutoMonitoring  bool   `json:"enable_auto_monitoring"`
}

func (c Config) Configured() bool {
	return c.DatabaseURL != ""
}

func (c Config) ProviderConfigured() bool {
	return c.BaseURL != "" && c.APIKey != ""
}

func mask(s string) string {
	if s == "" {
		return ""
	}
	return "***redacted***"
}

// LogValue implements slog.LogValuer so slog.Any("cfg", c) never serializes
// the API key or the DSN (which embeds the DB password).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("database_url", mask(c.DatabaseURL)),
		slog.String("base_url", c.BaseURL),
		slog.String("api_key", mask(c.APIKey)),
		slog.String("default_cover_size", c.DefaultCoverSize),
		slog.String("request_quality_profile", c.RequestQualityProfile),
		slog.Bool("enable_auto_monitoring", c.EnableAutoMonitoring),
	)
}

// String implements fmt.Stringer with the same redaction.
func (c Config) String() string { return c.LogValue().String() }

// Server implements the plugin's Runtime service.
type Server struct {
	runtimedefault.Server
	manifest *pluginv1.PluginManifest
	onCfg    func(Config) error

	mu  sync.RWMutex
	cfg Config
}

func New(manifest *pluginv1.PluginManifest, onConfig func(Config) error) *Server {
	return &Server{manifest: manifest, onCfg: onConfig}
}

func (s *Server) GetManifest(_ context.Context, _ *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.manifest}, nil
}

func (s *Server) Configure(_ context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	cfg := Config{}
	for _, e := range req.GetConfig() {
		v := e.GetValue()
		if v == nil {
			continue
		}
		m := v.AsMap()
		switch e.GetKey() {
		case "database_url":
			cfg.DatabaseURL = stringFromValue(m["value"])
		case "base_url":
			cfg.BaseURL = stringFromValue(m["value"])
		case "api_key":
			cfg.APIKey = stringFromValue(m["value"])
		case "default_cover_size":
			cfg.DefaultCoverSize = stringFromValue(m["value"])
		case "request_quality_profile":
			cfg.RequestQualityProfile = stringFromValue(m["value"])
		case "enable_auto_monitoring":
			if b, ok := m["value"].(bool); ok {
				cfg.EnableAutoMonitoring = b
			}
		}
	}
	if cfg.DatabaseURL == "" {
		s.mu.Lock()
		s.cfg = cfg
		s.mu.Unlock()
		return &pluginv1.ConfigureResponse{}, nil
	}
	if cfg.BaseURL != "" {
		if u, err := url.Parse(cfg.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("base_url must be a valid http(s) URL")
		}
	}
	if s.onCfg != nil {
		if err := s.onCfg(cfg); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return &pluginv1.ConfigureResponse{}, nil
}

func (s *Server) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func stringFromValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
