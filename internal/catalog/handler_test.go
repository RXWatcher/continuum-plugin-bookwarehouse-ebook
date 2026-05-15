package catalog_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
			_, _ = w.Write([]byte(`{"books":[{"id":"a","title":"A","file_format":"epub"}],"pagination":{"page":1,"limit":10,"total_items":1,"total_pages":1}}`))
		case "/api/v1/books/search":
			_, _ = w.Write([]byte(`{"books":[{"id":"b","title":"B","file_format":"pdf"}],"pagination":{"page":1,"total_items":1,"total_pages":1}}`))
		case "/api/v1/books/a":
			_, _ = w.Write([]byte(`{"id":"a","title":"A","file_format":"epub","file_size":1024}`))
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

func TestList_PassesGenreFilterToUpstream(t *testing.T) {
	var gotQuery string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/books" {
			gotQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"books":[],"pagination":{"page":1,"total_pages":1,"total_items":0}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog?genre=foo&author=Bar&series=Baz&tag=quux", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body = %s", w.Code, w.Body.String())
	}
	for _, want := range []string{"genre=foo", "author=Bar", "series=Baz", "tag=quux"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("upstream query %q missing %q", gotQuery, want)
		}
	}
}

func TestBrowseGenres_RemapsIDToSlug(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/genres" {
			_, _ = w.Write([]byte(`{"genres":[{"id":42,"name":"Science Fiction","slug":"science-fiction","book_count":12},{"id":7,"name":"Mystery","book_count":3}],"pagination":{"page":1,"total_pages":1,"total_items":2}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/browse/genres", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body = %s", w.Code, w.Body.String())
	}
	var out struct {
		Items []bookwarehouse.Genre `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %+v", out.Items)
	}
	// First item has a slug — id should be remapped.
	if out.Items[0].ID != "science-fiction" {
		t.Errorf("expected id=science-fiction, got %q", out.Items[0].ID)
	}
	// Second item has no slug — id stays as-is.
	if out.Items[1].ID != "7" {
		t.Errorf("expected fallback id=7, got %q", out.Items[1].ID)
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

func TestCover_StreamProxiesBytes(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/books/bw-7/cover/original" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-API-Key"); got != "k" {
			t.Errorf("X-API-Key = %q", got)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("cover"))
	}))
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/cover/bw-7/large", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if got := w.Body.String(); got != "cover" {
		t.Errorf("body = %q", got)
	}
}

func TestFile_StreamProxiesBytes(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/books/bw-7/download" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("X-API-Key"); got != "k" {
			t.Errorf("X-API-Key = %q", got)
		}
		w.Header().Set("Content-Type", "application/epub+zip")
		_, _ = w.Write([]byte("book"))
	}))
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/file/bw-7?format=epub", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	if got := w.Body.String(); got != "book" {
		t.Errorf("body = %q", got)
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
