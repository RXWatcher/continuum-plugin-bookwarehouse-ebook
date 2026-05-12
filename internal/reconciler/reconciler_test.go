package reconciler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/reconciler"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/store"
)

type fakePub struct {
	mu   sync.Mutex
	pubs []struct {
		Name    string
		Payload map[string]any
	}
}

func (f *fakePub) Publish(_ context.Context, name string, payload map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pubs = append(f.pubs, struct {
		Name    string
		Payload map[string]any
	}{name, payload})
}

func newReconcilerForTest(t *testing.T, upResp string, upStatus int) (*reconciler.Reconciler, *fakePub, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	pub := &fakePub{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(upStatus)
		_, _ = w.Write([]byte(upResp))
	}))
	t.Cleanup(up.Close)
	bw := bookwarehouse.NewClient(up.URL, "k")
	r := reconciler.New(reconciler.Deps{Store: st, Pub: pub, BW: bw})
	return r, pub, st
}

func TestReconciler_NoOp_WhenNoNonTerminalRows(t *testing.T) {
	r, pub, _ := newReconcilerForTest(t, `{"id":"x","status":"queued"}`, 200)
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(pub.pubs) != 0 {
		t.Errorf("no events expected; got %v", pub.pubs)
	}
}

func TestReconciler_StatusChange_EmitsRequestStatusChanged(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"mon-1","status":"downloading"}`, 200)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "mon-1", Status: "acknowledged",
		LastPolled: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
	})
	_ = r.Tick(context.Background())
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_status_changed" {
		t.Errorf("got pubs = %v", pub.pubs)
	}
	if pub.pubs[0].Payload["status"] != "downloading" {
		t.Errorf("status = %v", pub.pubs[0].Payload["status"])
	}
	row, _ := st.GetForwardedRequest(context.Background(), "req-1")
	if row.Status != "downloading" {
		t.Errorf("row.Status = %q", row.Status)
	}
}

func TestReconciler_TerminalImported_EmitsRequestFulfilled(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"mon-1","status":"imported"}`, 200)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "mon-1", Status: "downloading",
		UpdatedAt: time.Now(),
	})
	_ = r.Tick(context.Background())
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_fulfilled" {
		t.Errorf("got pubs = %v", pub.pubs)
	}
}

func TestReconciler_TerminalFailed_EmitsRequestFailed(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"mon-1","status":"failed"}`, 200)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "mon-1", Status: "downloading",
		UpdatedAt: time.Now(),
	})
	_ = r.Tick(context.Background())
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_failed" {
		t.Errorf("got pubs = %v", pub.pubs)
	}
}
