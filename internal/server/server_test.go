package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/server"
)

func TestHealthOK(t *testing.T) {
	h := server.New(server.Deps{})
	r := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["ok"] != true {
		t.Errorf("ok = %v", body["ok"])
	}
}

func TestCapabilitiesDeclaresFormatsAndFeatures(t *testing.T) {
	h := server.New(server.Deps{EnableAutoMonitoring: true})
	r := httptest.NewRequest("GET", "/api/v1/capabilities", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	var body struct {
		Formats                []string `json:"formats"`
		Features               []string `json:"features"`
		MaxConcurrentDownloads int      `json:"max_concurrent_downloads"`
		SupportsRange          bool     `json:"supports_range_requests"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if got, want := len(body.Formats), 4; got != want {
		t.Errorf("formats len = %d, want %d (%v)", got, want, body.Formats)
	}
	hasAuto := false
	for _, f := range body.Features {
		if f == "auto_monitoring" {
			hasAuto = true
		}
	}
	if !hasAuto {
		t.Errorf("features missing auto_monitoring: %v", body.Features)
	}
	if !body.SupportsRange {
		t.Error("supports_range_requests should be true")
	}
}

func TestCapabilities_NoAutoMonitoring_WhenDisabled(t *testing.T) {
	h := server.New(server.Deps{EnableAutoMonitoring: false})
	r := httptest.NewRequest("GET", "/api/v1/capabilities", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, r)
	var body struct {
		Features []string `json:"features"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	for _, f := range body.Features {
		if f == "auto_monitoring" {
			t.Errorf("auto_monitoring should not appear when disabled: %v", body.Features)
		}
	}
}
