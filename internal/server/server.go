// Package server constructs the chi-based HTTP handler. It is wrapped by
// internal/httproutes into the SDK's HttpRoutes.v1 RPC.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/catalog"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/request"
)

type Deps struct {
	EnableAutoMonitoring bool
	BookwarehouseClient  *bookwarehouse.Client
}

type Server struct {
	deps Deps
}

func New(d Deps) *Server { return &Server{deps: d} }

// chiPathValueShim copies chi.URLParam values into the request via
// SetPathValue, so handlers using stdlib r.PathValue work under chi.
func chiPathValueShim(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			for i, k := range rctx.URLParams.Keys {
				if i < len(rctx.URLParams.Values) {
					r.SetPathValue(k, rctx.URLParams.Values[i])
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/capabilities", s.handleCapabilities)
		if s.deps.BookwarehouseClient != nil {
			ch := catalog.NewHandler(s.deps.BookwarehouseClient)
			ch.Mount(r)
			rh := request.NewHandler(s.deps.BookwarehouseClient)
			r.Get("/requests/{external_id}", chiPathValueShim(rh.Snapshot()).ServeHTTP)
		}
	})
	return r
}
