package request_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RXWatcher/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/RXWatcher/continuum-plugin-bookwarehouse-ebook/internal/request"
)

func TestSnapshot_ReturnsUpstreamStatus(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/monitoring/mon-99" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"mon-99","status":"downloading"}`))
	}))
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	h := request.NewHandler(c)
	r := httptest.NewRequest("GET", "/mon-99", nil)
	r.SetPathValue("external_id", "mon-99")
	w := httptest.NewRecorder()
	h.Snapshot().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "downloading" {
		t.Errorf("status = %v", body["status"])
	}
	if body["external_id"] != "mon-99" {
		t.Errorf("external_id = %v", body["external_id"])
	}
}

func TestSnapshot_MissingExternalIDIs400(t *testing.T) {
	c := bookwarehouse.NewClient("http://nowhere", "k")
	h := request.NewHandler(c)
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.Snapshot().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d", w.Code)
	}
}

func TestSnapshot_UpstreamErrorIs502(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	h := request.NewHandler(c)
	r := httptest.NewRequest("GET", "/x", nil)
	r.SetPathValue("external_id", "x")
	w := httptest.NewRecorder()
	h.Snapshot().ServeHTTP(w, r)
	if w.Code != http.StatusBadGateway {
		t.Errorf("code = %d", w.Code)
	}
}
