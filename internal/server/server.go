// Package server constructs the chi-based HTTP handler. It is wrapped by
// internal/httproutes into the SDK's HttpRoutes.v1 RPC.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Deps holds dependencies for the HTTP layer. Fields are filled in across
// Phase 1 (capabilities), Phase 2/3 (BookwarehouseClient), Phase 5 (request
// handler), etc. The server treats nil dependencies as "feature not mounted".
type Deps struct {
	EnableAutoMonitoring bool

	// CatalogRoutes is a hook installed in Phase 3 to mount /catalog/*,
	// /browse/*, /cover, /file, /external_search, /requests routes.
	CatalogRoutes func(chi.Router)
}

type Server struct {
	deps Deps
}

func New(d Deps) *Server { return &Server{deps: d} }

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/capabilities", s.handleCapabilities)
		if s.deps.CatalogRoutes != nil {
			s.deps.CatalogRoutes(r)
		}
	})
	return r
}
