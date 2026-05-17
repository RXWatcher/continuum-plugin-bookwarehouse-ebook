package bookwarehouse_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
)

func TestClient_SendsAPIKeyHeader(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "secret-key")
	if _, err := c.Get(context.Background(), "/api/v1/ping"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotKey != "secret-key" {
		t.Errorf("X-API-Key = %q", gotKey)
	}
}

// A broken/hostile upstream can return a huge error body. It must not be
// inlined whole into the error string (it propagates into logs / responses).
func TestClient_Get_TruncatesErrorBody(t *testing.T) {
	big := strings.Repeat("x", 50000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	_, err := c.Get(context.Background(), "/x")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(err.Error()) > 1024 {
		t.Errorf("error not truncated: %d bytes", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status: %q", err.Error())
	}
}

func TestClient_TrimsTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL+"/", "k")
	_, _ = c.Get(context.Background(), "/api/v1/x")
	if gotPath != "/api/v1/x" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestBook_Decode(t *testing.T) {
	raw := []byte(`{
		"id": "bw-42",
		"title": "Project Hail Mary",
		"authors": ["Andy Weir"],
		"isbn": "9780593135204",
		"publisher": "Ballantine",
		"series": "",
		"year": 2021,
		"language": "en",
		"cover_url": "https://bw.example/c/42",
		"has_cover": true,
		"rating": 4.5,
		"formats": ["epub","pdf"]
	}`)
	var b bookwarehouse.Book
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if b.ID != "bw-42" || b.Title != "Project Hail Mary" {
		t.Errorf("got %+v", b)
	}
	if len(b.Formats) != 2 {
		t.Errorf("formats = %v", b.Formats)
	}
}

func TestPaged_Decode(t *testing.T) {
	raw := []byte(`{"items":[{"id":"a"},{"id":"b"}],"next_cursor":"abc","total":42}`)
	var p bookwarehouse.Paged[bookwarehouse.Book]
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Items) != 2 || p.NextCursor != "abc" || p.Total != 42 {
		t.Errorf("paged = %+v", p)
	}
}

func TestClient_ListBooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/books" || r.URL.Query().Get("limit") != "20" {
			t.Errorf("path/query = %s ?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"books":[{"id":"a","title":"A"}],"pagination":{"page":1,"limit":20,"total_items":1,"total_pages":1}}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	out, err := c.ListBooks(context.Background(), bookwarehouse.ListParams{Limit: 20})
	if err != nil {
		t.Fatalf("ListBooks: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "a" {
		t.Errorf("got %+v", out)
	}
}

func TestClient_GetBook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/books/bw-42" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"bw-42","title":"X","file_format":"epub","file_size":1000}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	d, err := c.GetBook(context.Background(), "bw-42")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if d.ID != "bw-42" || len(d.Files) != 1 || d.Files[0].Format != "epub" {
		t.Errorf("got %+v", d)
	}
}

func TestClient_ListBooks_PassesFilterParams(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"books":[],"pagination":{"page":1,"total_pages":1,"total_items":0}}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	_, err := c.ListBooks(context.Background(), bookwarehouse.ListParams{
		Author: "Andy Weir",
		Series: "Bobiverse",
		Genre:  "sci-fi",
		Tag:    "favorite",
	})
	if err != nil {
		t.Fatalf("ListBooks: %v", err)
	}
	// url.Values encodes alphabetically; spaces become '+'.
	for _, want := range []string{"author=Andy+Weir", "series=Bobiverse", "genre=sci-fi", "tag=favorite"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func TestClient_SearchBooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/books/search" || r.URL.Query().Get("q") != "hail" {
			t.Errorf("path/query = %s ?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"books":[{"id":"a","title":"A"}],"pagination":{"page":1,"total_pages":1,"total_items":1}}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	out, _ := c.ListBooks(context.Background(), bookwarehouse.ListParams{Query: "hail"})
	if len(out.Items) != 1 {
		t.Errorf("got %+v", out)
	}
}

func TestClient_ListAuthors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	out, _ := c.ListAuthors(context.Background(), "", 10)
	if len(out.Items) != 0 {
		t.Errorf("authors = %+v", out)
	}
}

func TestClient_ExternalSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/external_search" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"source_id":"ol-1","source":"openlibrary","title":"X"}]}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	hits, _ := c.ExternalSearch(context.Background(), "weir", 0)
	if len(hits) != 1 || hits[0].SourceID != "ol-1" {
		t.Errorf("hits = %+v", hits)
	}
}

func TestClient_AddMonitoring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/monitoring/add" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"mon-1","status":"queued"}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	resp, _ := c.AddMonitoring(context.Background(), bookwarehouse.MonitoringRequest{Title: "X"})
	if resp.ID != "mon-1" {
		t.Errorf("got %+v", resp)
	}
}

func TestClient_GetMonitoring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/monitoring/mon-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"mon-1","status":"downloading"}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	resp, _ := c.GetMonitoring(context.Background(), "mon-1")
	if resp.Status != "downloading" {
		t.Errorf("got %+v", resp)
	}
}

// externalID reaches GetMonitoring from a URL param and from the DB. A value
// with path/query metacharacters must be percent-escaped so it can't redirect
// the upstream request (SSRF / path traversal).
func TestClient_GetMonitoring_EscapesID(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"id":"x","status":"queued"}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	if _, err := c.GetMonitoring(context.Background(), "a?b"); err != nil {
		t.Fatalf("GetMonitoring: %v", err)
	}
	if gotPath != "/api/v1/monitoring/a?b" || gotQuery != "" {
		t.Errorf("upstream path=%q query=%q (externalID not escaped)", gotPath, gotQuery)
	}
}

// NextCursor must be derived from the page we REQUESTED, not the page
// upstream echoes back. Some upstream responses report page:0 (or omit it);
// trusting that produced NextCursor "1" forever (re-fetching page 1 in an
// infinite loop) or dropped pagination entirely.
func TestClient_ListBooks_NextCursorFromRequestedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "2" {
			t.Errorf("expected page=2, got %q", r.URL.Query().Get("page"))
		}
		// Upstream echoes page:0 (unreliable) but reports 3 total pages.
		_, _ = w.Write([]byte(`{"books":[{"id":"x","title":"X"}],"pagination":{"page":0,"limit":50,"total_items":150,"total_pages":3}}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	out, err := c.ListBooks(context.Background(), bookwarehouse.ListParams{Cursor: "2"})
	if err != nil {
		t.Fatalf("ListBooks: %v", err)
	}
	if out.NextCursor != "3" {
		t.Errorf("NextCursor = %q, want \"3\" (requested page 2 + 1)", out.NextCursor)
	}
}

func TestClient_ListBooks_NoNextCursorOnLastPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"books":[{"id":"x"}],"pagination":{"page":0,"total_items":150,"total_pages":3}}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	out, _ := c.ListBooks(context.Background(), bookwarehouse.ListParams{Cursor: "3"})
	if out.NextCursor != "" {
		t.Errorf("NextCursor = %q, want empty on the last page", out.NextCursor)
	}
}

// dedupKey must not collapse distinct works. Different volumes of a series
// share title+author but differ by series_index; they are separate books and
// must both appear, otherwise readers can never reach later volumes.
func TestListBooksDeduped_KeepsDistinctSeriesVolumes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"books":[
			{"id":"v1","title":"The Expanse","authors":[{"name":"James S. A. Corey"}],"series":"The Expanse","series_index":1},
			{"id":"v2","title":"The Expanse","authors":[{"name":"James S. A. Corey"}],"series":"The Expanse","series_index":2},
			{"id":"v2dup","title":"The Expanse","authors":[{"name":"James S. A. Corey"}],"series":"The Expanse","series_index":2}
		],"pagination":{"page":1,"limit":50,"total_items":3,"total_pages":1}}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")
	out, err := c.ListBooksDeduped(context.Background(), bookwarehouse.ListParams{Limit: 50}, 3)
	if err != nil {
		t.Fatalf("ListBooksDeduped: %v", err)
	}
	// v1 and v2 are distinct volumes (kept); v2dup is a true duplicate of v2
	// (same title+author+series+index) and must collapse.
	if len(out.Items) != 2 {
		t.Fatalf("want 2 distinct volumes, got %d: %+v", len(out.Items), out.Items)
	}
	ids := map[string]bool{out.Items[0].ID: true, out.Items[1].ID: true}
	if !ids["v1"] || !(ids["v2"] || ids["v2dup"]) {
		t.Errorf("expected volume 1 and volume 2 present; got %+v", out.Items)
	}
}
