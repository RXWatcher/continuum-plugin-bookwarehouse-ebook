package catalog_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/catalog"
)

func upstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/books":
			_, _ = w.Write([]byte(`{"items":[{"id":"a","title":"A","formats":["epub"]}],"total":1}`))
		case "/api/v1/books/search":
			_, _ = w.Write([]byte(`{"items":[{"id":"b","title":"B","formats":["pdf"]}]}`))
		case "/api/v1/books/a":
			_, _ = w.Write([]byte(`{"id":"a","title":"A","formats":["epub"],"files":[{"format":"epub","file_size":1024}]}`))
		case "/api/v1/authors":
			_, _ = w.Write([]byte(`{"items":[{"id":"a1","name":"Author One","count":3}]}`))
		case "/api/v1/external_search":
			_, _ = w.Write([]byte(`{"items":[{"source_id":"ol-1","source":"openlibrary","title":"X"}]}`))
		case "/api/v1/monitoring/mon-99":
			_, _ = w.Write([]byte(`{"id":"mon-99","status":"downloading"}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func newRouter(c *bookwarehouse.Client) *chi.Mux {
	r := chi.NewRouter()
	h := catalog.NewHandler(c)
	h.Mount(r)
	return r
}

func TestList_Returns200WithItems(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog?limit=10", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	var env catalog.PageEnvelope[catalog.EbookSummary]
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Items) != 1 || env.Items[0].ID != "a" {
		t.Errorf("env = %+v", env)
	}
}

func TestSearch_Returns200WithItems(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog/search?q=x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env catalog.PageEnvelope[catalog.EbookSummary]
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Items) != 1 || env.Items[0].ID != "b" {
		t.Errorf("env = %+v", env)
	}
}

func TestDetail_Returns200(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog/a", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body = %s", w.Code, w.Body.String())
	}
	var d catalog.EbookDetail
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if d.ID != "a" || len(d.Files) != 1 || d.Files[0].MimeType != "application/epub+zip" {
		t.Errorf("d = %+v", d)
	}
}

func TestBrowseAuthors_Returns200(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/browse/authors", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestCover_Redirects302(t *testing.T) {
	c := bookwarehouse.NewClient("https://up.example", "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/cover/bw-7/large", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d", w.Code)
	}
	if got := w.Header().Get("Location"); got != "https://up.example/api/v1/books/bw-7/cover/large" {
		t.Errorf("Location = %q", got)
	}
}

func TestFile_Redirects302(t *testing.T) {
	c := bookwarehouse.NewClient("https://up.example", "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/file/bw-7?format=epub", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d", w.Code)
	}
	if got := w.Header().Get("Location"); got != "https://up.example/api/v1/books/bw-7/files/epub" {
		t.Errorf("Location = %q", got)
	}
}

func TestExternalSearch_Returns200(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/external_search?q=weir", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body = %s", w.Code, w.Body.String())
	}
}

func TestRequestSnapshot_Returns200(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/requests/mon-99", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "downloading" {
		t.Errorf("status = %v", body["status"])
	}
}
