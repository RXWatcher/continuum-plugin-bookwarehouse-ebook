package bookwarehouse_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
)

func TestClient_APIKeyStrippedOnCrossHostRedirect(t *testing.T) {
	var gotKeyOnAttacker string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKeyOnAttacker = r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{"books":[],"pagination":{"page":1,"total_pages":1}}`))
	}))
	defer attacker.Close()

	var keptSameHost bool
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redir-cross":
			http.Redirect(w, r, attacker.URL+"/", http.StatusFound)
		case "/redir-same":
			if r.Header.Get("X-API-Key") == "secret" {
				keptSameHost = true
			}
			_, _ = w.Write([]byte(`{"books":[],"pagination":{"page":1,"total_pages":1}}`))
		default:
			http.Redirect(w, r, upstream.URL+"/redir-same", http.StatusFound)
		}
	}))
	defer upstream.Close()

	c := bookwarehouse.NewClient(upstream.URL, "secret")
	if _, err := c.Get(context.Background(), "/redir-cross"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotKeyOnAttacker != "" {
		t.Fatalf("API key leaked to cross-host redirect target: %q", gotKeyOnAttacker)
	}
	if _, err := c.Get(context.Background(), "/start"); err != nil {
		t.Fatalf("get same-host: %v", err)
	}
	if !keptSameHost {
		t.Fatal("API key wrongly stripped on a same-host redirect")
	}
}

func TestGetMonitoring_RejectsEmptyResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"detail":"not found"}`, `null`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		c := bookwarehouse.NewClient(srv.URL, "k")
		if _, err := c.GetMonitoring(context.Background(), "ext-1"); err == nil {
			t.Fatalf("body %q: expected error, got nil", body)
		}
		srv.Close()
	}
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"downloading"}`))
	}))
	defer ok.Close()
	got, err := bookwarehouse.NewClient(ok.URL, "k").GetMonitoring(context.Background(), "ext-1")
	if err != nil || got.Status != "downloading" {
		t.Fatalf("valid status-only response rejected: %+v err=%v", got, err)
	}
}

// SetDefaultCoverSize (Configure) must be race-free against concurrent
// catalog reads that build cover URLs.
func TestSetDefaultCoverSize_RaceFree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"books":[{"id":"b1","title":"T","has_cover":true}],"pagination":{"page":1,"total_pages":1,"total_items":1}}`))
	}))
	defer srv.Close()
	c := bookwarehouse.NewClient(srv.URL, "k")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); c.SetDefaultCoverSize("small") }()
		go func() {
			defer wg.Done()
			_, _ = c.ListBooks(context.Background(), bookwarehouse.ListParams{Limit: 1})
		}()
	}
	wg.Wait()
}
