package scheduler

import (
	"context"
	"strings"
	"testing"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"

	"github.com/RXWatcher/continuum-plugin-bookwarehouse-ebook/internal/reconciler"
)

func TestTaskID(t *testing.T) {
	cases := map[string]string{
		"plugin:42:reconciler": "reconciler", // the real host wire format
		"plugin:1:reconciler":  "reconciler",
		"reconciler":           "reconciler", // bare (host integration tests)
		"plugin:7:other":       "other",
	}
	for in, want := range cases {
		if got := taskID(in); got != want {
			t.Errorf("taskID(%q) = %q, want %q", in, got, want)
		}
	}
}

// The host sends TaskKey="plugin:<installationID>:reconciler"; the reconciler
// must still be routed (previously an exact != "reconciler" check made it
// never fire while every tick was reported as success).
func TestRun_RoutesPrefixedKeyToReconciler(t *testing.T) {
	s := New(func() *reconciler.Reconciler { return nil })
	_, err := s.Run(context.Background(),
		&pluginv1.RunScheduledTaskRequest{TaskKey: "plugin:42:reconciler"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("prefixed key must route to the reconciler branch (nil deps -> not-configured error); got err=%v", err)
	}
}

func TestRun_UnknownKeyErrors(t *testing.T) {
	s := New(func() *reconciler.Reconciler { return nil })
	_, err := s.Run(context.Background(),
		&pluginv1.RunScheduledTaskRequest{TaskKey: "plugin:42:bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown task key") {
		t.Fatalf("unknown key must error (not silent success); got err=%v", err)
	}
}
