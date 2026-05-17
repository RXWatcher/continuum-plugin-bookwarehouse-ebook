package catalog_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/catalog"
)

func mount(c *bookwarehouse.Client) http.Handler {
	r := chi.NewRouter()
	catalog.NewHandler(c).Mount(r)
	return r
}

func TestCatalog_LimitClampedAndSortAllowlisted(t *testing.T) {
	cases := []struct {
		query     string
		wantLimit string
		wantSort  string
	}{
		{"?limit=999999999&sort=title", "100", "title"},
		{"?limit=-3", "", ""},
		{"?limit=abc", "", ""},
		{"?limit=25&sort=DROP&order=sideways", "25", ""},
		{"?sort=year&order=desc", "", "year"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			var got string
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.RawQuery
				_, _ = w.Write([]byte(`{"books":[],"pagination":{"page":1,"total_pages":1}}`))
			}))
			defer up.Close()
			h := mount(bookwarehouse.NewClient(up.URL, "k"))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/catalog"+tc.query, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("code=%d", w.Code)
			}
			if tc.wantLimit == "" && strings.Contains(got, "limit=") {
				t.Fatalf("limit should not be forwarded, got %q", got)
			}
			if tc.wantLimit != "" && !strings.Contains(got, "limit="+tc.wantLimit) {
				t.Fatalf("want limit=%s, got %q", tc.wantLimit, got)
			}
			if tc.wantSort == "" && strings.Contains(got, "sort=") {
				t.Fatalf("disallowed sort should be dropped, got %q", got)
			}
			if tc.wantSort != "" && !strings.Contains(got, "sort="+tc.wantSort) {
				t.Fatalf("want sort=%s, got %q", tc.wantSort, got)
			}
		})
	}
}

func TestCatalog_UpstreamErrorOpaque(t *testing.T) {
	h := mount(bookwarehouse.NewClient("http://127.0.0.1:1", "k"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/catalog", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("code=%d, want 502", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"127.0.0.1", "dial", "connection refused", "http://"} {
		if strings.Contains(body, leak) {
			t.Fatalf("client body leaked internal detail %q: %s", leak, body)
		}
	}
}
