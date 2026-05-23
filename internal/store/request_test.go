package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/store"
)

func TestUpsertForwardedRequest_NewRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID:   "req-1",
		Status:      "submitted",
		AutoMonitor: true,
		UpdatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.GetForwardedRequest(ctx, "req-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "submitted" || !got.AutoMonitor {
		t.Errorf("got %+v", got)
	}
}

func TestUpsertForwardedRequest_UpdatesExisting(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: "req-1", Status: "submitted", UpdatedAt: time.Now(),
	})
	_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: "req-1", Status: "acknowledged", ExternalID: "bw-42", UpdatedAt: time.Now(),
	})
	got, _ := s.GetForwardedRequest(ctx, "req-1")
	if got.Status != "acknowledged" || got.ExternalID != "bw-42" {
		t.Errorf("after second upsert: %+v", got)
	}
}

func TestListNonTerminal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{RequestID: "a", Status: "downloading", UpdatedAt: time.Now()})
	_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{RequestID: "b", Status: "imported", UpdatedAt: time.Now()})
	_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{RequestID: "c", Status: "failed", UpdatedAt: time.Now()})

	rows, err := s.ListNonTerminal(ctx, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "a" {
		t.Errorf("non-terminal = %+v", rows)
	}
}

// Rows that have never been polled all share last_polled (epoch). Without a
// tiebreaker their order is undefined, so under LIMIT the same subset can be
// returned every tick while the rest starve. Order must be deterministic
// (last_polled ASC, request_id ASC).
func TestListNonTerminal_DeterministicTiebreak(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// Insert in reverse id order; never polled (no last_polled).
	for _, id := range []string{"req-3", "req-2", "req-1"} {
		_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: id, Status: "submitted", UpdatedAt: time.Now(),
		})
	}
	rows, err := s.ListNonTerminal(ctx, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := []string{rows[0].RequestID, rows[1].RequestID, rows[2].RequestID}
	want := []string{"req-1", "req-2", "req-3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (no deterministic tiebreaker)", got, want)
		}
	}
}

// Unsubmitted means "no external_id yet AND still in flight". A failed row
// with no external_id must count as failed only, not also as unsubmitted.
func TestRequestStats_UnsubmittedExcludesTerminal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// Genuinely unsubmitted: in flight, no external_id.
	_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: "pending", Status: "submitted", UpdatedAt: time.Now(),
	})
	// Failed before AddMonitoring returned an id: no external_id.
	_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: "dead", Status: "failed", ErrorText: "boom", UpdatedAt: time.Now(),
	})
	stats, err := s.RequestStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Unsubmitted != 1 {
		t.Errorf("Unsubmitted = %d, want 1 (failed row must not be counted)", stats.Unsubmitted)
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
}

// Event delivery is at-least-once: a duplicate/late/replayed request_submitted
// (status "submitted"/"acknowledged") must not resurrect a row that already
// reached a terminal state (imported or failed).
func TestUpsertForwardedRequest_TerminalGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, terminal := range []string{"imported", "failed"} {
		id := "term-" + terminal
		if err := s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: id, Status: terminal, ExternalID: "bw-1", UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("seed %s: %v", terminal, err)
		}
		// Replayed earlier-stage event arrives after the terminal state.
		if err := s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: id, Status: "submitted", UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("replay %s: %v", terminal, err)
		}
		got, err := s.GetForwardedRequest(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", terminal, err)
		}
		if got.Status != terminal {
			t.Errorf("%s row resurrected to %q; want %q", terminal, got.Status, terminal)
		}
	}

	// A non-terminal row must still advance normally (guard is not too broad).
	_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: "live", Status: "submitted", UpdatedAt: time.Now(),
	})
	_ = s.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: "live", Status: "downloading", UpdatedAt: time.Now(),
	})
	if got, _ := s.GetForwardedRequest(ctx, "live"); got.Status != "downloading" {
		t.Errorf("non-terminal row should advance; got %q", got.Status)
	}
}
