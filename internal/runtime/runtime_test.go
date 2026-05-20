package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestConfigRedaction(t *testing.T) {
	cfg := Config{
		DatabaseURL: "postgres://u:sup3rsecret@db/x",
		BaseURL:     "https://bw.internal",
		APIKey:      "TOPSECRETKEY",
	}
	if s := cfg.String(); strings.Contains(s, "sup3rsecret") || strings.Contains(s, "TOPSECRETKEY") {
		t.Fatalf("String leaked a secret: %s", s)
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("cfg", "config", cfg)
	out := buf.String()
	if strings.Contains(out, "sup3rsecret") || strings.Contains(out, "TOPSECRETKEY") {
		t.Fatalf("slog leaked a secret: %s", out)
	}
	if !strings.Contains(out, "bw.internal") {
		t.Fatalf("redaction hid non-secret base_url: %s", out)
	}
}

func cfgReq(kv map[string]string) *pluginv1.ConfigureRequest {
	var items []*pluginv1.ConfigEntry
	for k, v := range kv {
		s, _ := structpb.NewStruct(map[string]any{"value": v})
		items = append(items, &pluginv1.ConfigEntry{Key: k, Value: s})
	}
	return &pluginv1.ConfigureRequest{Config: items}
}

func TestConfigure_RejectsInvalidBaseURL(t *testing.T) {
	s := New(nil, func(Config) error { return nil })
	for _, bad := range []string{"not-a-url", "ftp://x", "://nohost", "https://"} {
		if _, err := s.Configure(context.Background(), cfgReq(map[string]string{
			"database_url": "postgres://x/y", "api_key": "k", "base_url": bad,
		})); err == nil {
			t.Fatalf("base_url %q accepted, want rejected", bad)
		}
	}
	if _, err := s.Configure(context.Background(), cfgReq(map[string]string{
		"database_url": "postgres://x/y", "api_key": "k", "base_url": "https://ok.example",
	})); err != nil {
		t.Fatalf("valid base_url rejected: %v", err)
	}
}

func TestConfigJSONSnakeCaseRoundTrip(t *testing.T) {
	var cfg Config
	raw := []byte(`{
		"base_url": "https://bookwarehouse.zenterprise.org",
		"api_key": "secret",
		"default_cover_size": "thumbnail",
		"request_quality_profile": "best",
		"enable_auto_monitoring": true
	}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if cfg.BaseURL != "https://bookwarehouse.zenterprise.org" {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.APIKey != "secret" {
		t.Fatalf("APIKey = %q", cfg.APIKey)
	}
	if cfg.DefaultCoverSize != "thumbnail" {
		t.Fatalf("DefaultCoverSize = %q", cfg.DefaultCoverSize)
	}
	if cfg.RequestQualityProfile != "best" {
		t.Fatalf("RequestQualityProfile = %q", cfg.RequestQualityProfile)
	}
	if !cfg.EnableAutoMonitoring {
		t.Fatal("EnableAutoMonitoring = false")
	}
}
