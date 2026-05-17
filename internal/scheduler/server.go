// Package scheduler adapts our internal Reconciler.Tick to the SDK's
// scheduled_task.v1 capability. Manifest declares one task id "reconciler".
//
// Note: SDK uses Run(RunScheduledTaskRequest{TaskKey, Input}) →
// RunScheduledTaskResponse{Output}. Errors return as gRPC status errors;
// there is no ErrorMessage field.
package scheduler

import (
	"context"
	"fmt"
	"strings"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/reconciler"
)

// taskID extracts the capability id from a scheduled-task key. The Continuum
// host sends "plugin:<installationID>:<capabilityID>" (see task_registry
// pluginTaskKey); bare ids may arrive from host integration tests. Capability
// ids in this plugin's manifest contain no ':'.
func taskID(key string) string {
	if i := strings.LastIndexByte(key, ':'); i >= 0 {
		return key[i+1:]
	}
	return key
}

type Server struct {
	pluginv1.UnimplementedScheduledTaskServer
	depsFn func() *reconciler.Reconciler
}

func New(depsFn func() *reconciler.Reconciler) *Server {
	return &Server{depsFn: depsFn}
}

func (s *Server) Run(ctx context.Context, req *pluginv1.RunScheduledTaskRequest) (*pluginv1.RunScheduledTaskResponse, error) {
	if taskID(req.GetTaskKey()) != "reconciler" {
		// Genuinely-unknown key: error rather than report a silent success
		// (a misroute must surface, not masquerade as a clean no-op).
		return nil, fmt.Errorf("unknown task key %q", req.GetTaskKey())
	}
	r := s.depsFn()
	if r == nil {
		// Not configured yet — error so the host retries on the next tick
		// instead of recording a successful no-op.
		return nil, fmt.Errorf("plugin not configured yet")
	}
	if err := r.Tick(ctx); err != nil {
		return nil, err
	}
	return &pluginv1.RunScheduledTaskResponse{}, nil
}
