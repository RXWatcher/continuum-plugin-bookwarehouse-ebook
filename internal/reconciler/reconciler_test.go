package reconciler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/reconciler"
	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/store"
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

// An unknown/unmapped upstream status must NOT regress the request to
// "acknowledged" and must NOT emit a status-changed event. We hold the
// current status and just record that we polled.
func TestReconciler_UnknownUpstreamStatus_HoldsNoEvent(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"mon-1","status":"paused"}`, 200)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "mon-1", Status: "downloading",
		LastPolled: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
	})
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(pub.pubs) != 0 {
		t.Errorf("unknown status must not emit an event; got %v", pub.pubs)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "req-1")
	if row.Status != "downloading" {
		t.Errorf("status regressed to %q; want it held at downloading", row.Status)
	}
	if !row.LastPolled.After(time.Now().Add(-time.Minute)) {
		t.Errorf("last_polled was not advanced: %v", row.LastPolled)
	}
}

// A transient upstream failure sets error_text. Once polling succeeds again
// the request is healthy, so error_text must be cleared — otherwise it sticks
// forever and RequestStats.WithErrors over-counts permanently.
func TestReconciler_SuccessfulPoll_ClearsStickyError(t *testing.T) {
	r, _, st := newReconcilerForTest(t, `{"id":"mon-1","status":"downloading"}`, 200)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "mon-1", Status: "downloading",
		ErrorText: "boom: upstream 503", LastPolled: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
	})
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "req-1")
	if row.ErrorText != "" {
		t.Errorf("error_text should be cleared after a successful poll; got %q", row.ErrorText)
	}
	stats, _ := st.RequestStats(context.Background())
	if stats.WithErrors != 0 {
		t.Errorf("WithErrors should be 0 after recovery; got %d", stats.WithErrors)
	}
}

// A cancelled context must short-circuit the tick: no upstream calls, no
// events, no DB writes.
func TestReconciler_CancelledContext_NoProcessing(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"mon-1","status":"imported"}`, 200)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "mon-1", Status: "downloading",
		UpdatedAt: time.Now(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = r.Tick(ctx)
	if len(pub.pubs) != 0 {
		t.Errorf("cancelled context must not publish; got %v", pub.pubs)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "req-1")
	if row.Status != "downloading" {
		t.Errorf("cancelled context must not write; status = %q", row.Status)
	}
}
