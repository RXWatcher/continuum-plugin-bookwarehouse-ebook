package consumer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/consumer"
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

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	return s
}

func newConsumerForTest(t *testing.T, upstream *httptest.Server) (*consumer.Handler, *fakePub, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	pub := &fakePub{}
	bw := bookwarehouse.NewClient(upstream.URL, "k")
	deps := &consumer.Deps{
		Store:    st,
		Pub:      pub,
		BW:       bw,
		PluginID: "silo.bookwarehouse-ebook",
	}
	h := consumer.New(func() *consumer.Deps { return deps })
	return h, pub, st
}

func TestConsumer_SkipsWhenTargetMismatch(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called when target mismatches")
	}))
	defer up.Close()
	h, pub, _ := newConsumerForTest(t, up)
	_, _ = h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.silo.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-1",
			"target_plugin_id": "silo.something-else",
			"title":            "X",
		}),
	})
	if len(pub.pubs) != 0 {
		t.Errorf("publisher should not be called; got %d", len(pub.pubs))
	}
}

func TestConsumer_SkipsMalformedOrConflictingTargets(t *testing.T) {
	for _, payload := range []map[string]any{
		{"request_id": "r-blank", "target_plugin_id": " ", "title": "X"},
		{"request_id": "r-numeric", "target_plugin_id": float64(1), "title": "X"},
		{
			"request_id":                "r-conflict",
			"target_plugin_id":          "silo.something-else",
			"target_provider_plugin_id": "silo.bookwarehouse-ebook",
			"title":                     "X",
		},
	} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("upstream should not be called for payload %+v", payload)
		}))
		h, pub, _ := newConsumerForTest(t, up)
		_, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
			EventName: "plugin.silo.ebooks.request_submitted",
			Payload:   mustStruct(t, payload),
		})
		up.Close()
		if err != nil {
			t.Fatalf("HandleEvent: %v", err)
		}
		if len(pub.pubs) != 0 {
			t.Fatalf("publisher should not be called for %+v; got %+v", payload, pub.pubs)
		}
	}
}

func TestConsumer_HappyPath_EmitsAcknowledged(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"mon-77","status":"queued"}`))
	}))
	defer up.Close()
	h, pub, st := newConsumerForTest(t, up)
	_, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.silo.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-1",
			"target_plugin_id": "silo.bookwarehouse-ebook",
			"title":            "Project Hail Mary",
			"authors":          []any{"Andy Weir"},
			"isbn":             "9780593135204",
			"format_pref":      "epub",
			"auto_monitor":     true,
		}),
	})
	if err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_acknowledged" {
		t.Errorf("emitted = %+v", pub.pubs)
	}
	if pub.pubs[0].Payload["request_id"] != "r-1" || pub.pubs[0].Payload["external_id"] != "mon-77" {
		t.Errorf("payload = %+v", pub.pubs[0].Payload)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "r-1")
	if row.Status != "acknowledged" || row.ExternalID != "mon-77" || !row.AutoMonitor {
		t.Errorf("row = %+v", row)
	}
}

func TestConsumer_NilPublisherDoesNotPanic(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"mon-77","status":"queued"}`))
	}))
	defer up.Close()
	st := newTestStore(t)
	deps := &consumer.Deps{
		Store:    st,
		Pub:      nil,
		BW:       bookwarehouse.NewClient(up.URL, "k"),
		PluginID: "silo.bookwarehouse-ebook",
	}
	h := consumer.New(func() *consumer.Deps { return deps })
	_, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.silo.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-nil-pub",
			"target_plugin_id": "silo.bookwarehouse-ebook",
			"title":            "X",
		}),
	})
	if err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
}

func TestConsumer_UpstreamFails_EmitsRequestFailed(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer up.Close()
	h, pub, st := newConsumerForTest(t, up)
	_, _ = h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.silo.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-2",
			"target_plugin_id": "silo.bookwarehouse-ebook",
			"title":            "X",
		}),
	})
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_failed" {
		t.Errorf("emitted = %+v", pub.pubs)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "r-2")
	if row.Status != "failed" || row.ErrorText == "" {
		t.Errorf("row = %+v", row)
	}
}

// Capability servers serve before Configure runs. If depsFn returns nil the
// handler must nack (return an error) so the host redelivers the event once
// configured — previously it acked the request, dropping it permanently.
func TestConsumer_NotConfigured_Nacks(t *testing.T) {
	h := consumer.New(func() *consumer.Deps { return nil })
	resp, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.silo.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-cfg",
			"target_plugin_id": "silo.bookwarehouse-ebook",
			"title":            "X",
		}),
	})
	if err == nil {
		t.Fatal("not-configured must return an error so the host redelivers")
	}
	if resp != nil {
		t.Errorf("response must be nil on nack; got %+v", resp)
	}
}

func TestConsumer_NilDepsFn_Nacks(t *testing.T) {
	h := consumer.New(nil)
	_, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.silo.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-cfg",
			"target_plugin_id": "silo.bookwarehouse-ebook",
			"title":            "X",
		}),
	})
	if err == nil {
		t.Fatal("nil depsFn must nack so the host redelivers")
	}
}

// Non-target / nil-payload events are not ours: ack (no error) and drop so the
// host does not redeliver another plugin's event to us forever.
func TestConsumer_NonTargetEvent_Acks(t *testing.T) {
	h := consumer.New(func() *consumer.Deps { return nil })
	if _, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "some.other.event",
	}); err != nil {
		t.Fatalf("foreign event must be acked, not nacked; got err=%v", err)
	}
}
