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

// Search must forward pagination/sort params so infinite scroll works;
// previously it sent only ?q= and always returned upstream page 1.
func TestSearch_PassesPaginationParams(t *testing.T) {
	var gotQuery string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/books/search" {
			if gotQuery == "" { // capture the first fan-out request
				gotQuery = r.URL.RawQuery
			}
			_, _ = w.Write([]byte(`{"books":[{"id":"b","title":"B"}],"pagination":{"page":2,"total_pages":3,"total_items":3}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog/search?q=hail&cursor=2&limit=5&sort=title&order=desc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	for _, want := range []string{"q=hail", "page=2", "limit=5", "sort=title", "order=desc"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("upstream query %q missing %q", gotQuery, want)
		}
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

// The portal's EbookBackend.ListLibraries GETs /api/v1/catalog/libraries and
// expects {"items":[LibraryInfo...]}. Book Warehouse is one external Calibre
// catalog, so it advertises exactly one synthetic library; without this route
// portal libsync errors and the backend can never be provisioned.
func TestLibraries_ReturnsSyntheticLibrary(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog/libraries", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Items []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			MediaType string `json:"media_type"`
			Enabled   bool   `json:"enabled"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Items) != 1 {
		t.Fatalf("items = %+v", env.Items)
	}
	it := env.Items[0]
	if it.ID != 1 || it.Name == "" || it.MediaType != "book" || !it.Enabled {
		t.Errorf("synthetic library = %+v", it)
	}
}

// /catalog?library_id=1 is the synthetic library: serve normally.
func TestList_SyntheticLibraryIDServes(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog?library_id=1&limit=10", nil)
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

// /catalog?library_id=<not 1> asks for a library this backend does not own:
// return an empty page (200) without ever calling upstream — never leak this
// catalog's books under a foreign library id.
func TestList_ForeignLibraryIDReturnsEmpty(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream must not be called for a foreign library_id; got %s", r.URL.Path)
		w.WriteHeader(404)
	}))
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog?library_id=99", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	var env catalog.PageEnvelope[catalog.EbookSummary]
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Items) != 0 {
		t.Errorf("foreign library_id must yield no items; got %+v", env.Items)
	}
}

// book_id flows from the URL into the upstream request path. A value with
// path/query metacharacters must be percent-escaped so it can't redirect the
// upstream call (SSRF / path traversal).
func TestCover_EscapesBookID(t *testing.T) {
	var gotPath, gotQuery string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("cover"))
	}))
	defer up.Close()
	c := bookwarehouse.NewClient(up.URL, "k")
	r := newRouter(c)
	// chi decodes %3F -> "a?z"; unescaped that would split off a query string.
	req := httptest.NewRequest("GET", "/cover/a%3Fz/large", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if gotPath != "/api/v1/books/a?z/cover/original" || gotQuery != "" {
		t.Errorf("upstream path=%q query=%q (book_id not escaped)", gotPath, gotQuery)
	}
}
