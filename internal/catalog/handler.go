package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
)

type Handler struct {
	client *bookwarehouse.Client
}

func NewHandler(c *bookwarehouse.Client) *Handler { return &Handler{client: c} }

// Mount installs all catalog/browse/cover/file/external_search/request routes
// onto the given chi.Router under /api/v1.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/catalog", h.List())
	r.Get("/catalog/search", h.Search())
	r.Get("/catalog/{id}", h.Detail())
	r.Get("/browse/authors", h.BrowseAuthors())
	r.Get("/browse/series", h.BrowseSeries())
	r.Get("/browse/genres", h.BrowseGenres())
	r.Get("/browse/tags", h.BrowseTags())
	r.Get("/cover/{book_id}/{size}", h.Cover())
	r.Get("/file/{book_id}", h.File())
	r.Get("/external_search", h.ExternalSearch())
	r.Get("/requests/{external_id}", h.RequestSnapshot())
}

// List handles GET /api/v1/catalog
func (h *Handler) List() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := bookwarehouse.ListParams{
			Cursor: r.URL.Query().Get("cursor"),
			Sort:   r.URL.Query().Get("sort"),
			Order:  r.URL.Query().Get("order"),
		}
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				p.Limit = n
			}
		}
		out, err := h.client.ListBooks(r.Context(), p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeEnvelope(w, out)
	}
}

func (h *Handler) Search() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		out, err := h.client.ListBooks(r.Context(), bookwarehouse.ListParams{Query: q})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeEnvelope(w, out)
	}
}

func (h *Handler) Detail() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		d, err := h.client.GetBook(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ToDetail(d))
	}
}

func writeEnvelope(w http.ResponseWriter, p bookwarehouse.Paged[bookwarehouse.Book]) {
	out := PageEnvelope[EbookSummary]{
		NextCursor: p.NextCursor,
		Total:      p.Total,
		Items:      make([]EbookSummary, len(p.Items)),
	}
	for i, b := range p.Items {
		out.Items[i] = ToSummary(b)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h *Handler) BrowseAuthors() http.HandlerFunc {
	return browseHandler(func(ctx context.Context, cursor string, limit int) (any, error) {
		return h.client.ListAuthors(ctx, cursor, limit)
	})
}

func (h *Handler) BrowseSeries() http.HandlerFunc {
	return browseHandler(func(ctx context.Context, cursor string, limit int) (any, error) {
		return h.client.ListSeries(ctx, cursor, limit)
	})
}

func (h *Handler) BrowseGenres() http.HandlerFunc {
	return browseHandler(func(ctx context.Context, cursor string, limit int) (any, error) {
		return h.client.ListGenres(ctx, cursor, limit)
	})
}

func (h *Handler) BrowseTags() http.HandlerFunc {
	return browseHandler(func(ctx context.Context, cursor string, limit int) (any, error) {
		return h.client.ListTags(ctx, cursor, limit)
	})
}

func browseHandler(fetch func(context.Context, string, int) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		out, err := fetch(r.Context(), cursor, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func (h *Handler) Cover() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookID := chi.URLParam(r, "book_id")
		size := chi.URLParam(r, "size")
		http.Redirect(w, r, h.client.CoverURL(bookID, size), http.StatusFound)
	}
}

func (h *Handler) File() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookID := chi.URLParam(r, "book_id")
		format := r.URL.Query().Get("format")
		if format == "" {
			format = "epub"
		}
		http.Redirect(w, r, h.client.FileURL(bookID, format), http.StatusFound)
	}
}

func (h *Handler) ExternalSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "q required", http.StatusBadRequest)
			return
		}
		limit := 0
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		hits, err := h.client.ExternalSearch(r.Context(), q, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": hits})
	}
}

func (h *Handler) RequestSnapshot() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eid := chi.URLParam(r, "external_id")
		if eid == "" {
			http.Error(w, "external_id required", http.StatusBadRequest)
			return
		}
		snap, err := h.client.GetMonitoring(r.Context(), eid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"external_id": snap.ID,
			"status":      snap.Status,
		})
	}
}
