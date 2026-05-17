package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
)

// syntheticLibraryID is the single library this backend advertises. Book
// Warehouse is one external Calibre catalog with no native library concept,
// so it presents exactly one logical library to the portal. The portal maps
// this id to its provisioned row and echoes it back as ?library_id=.
const syntheticLibraryID int64 = 1

type Handler struct {
	client *bookwarehouse.Client
}

func NewHandler(c *bookwarehouse.Client) *Handler { return &Handler{client: c} }

// Mount installs all catalog/browse/cover/file/external_search/request routes
// onto the given chi.Router under /api/v1.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/catalog", h.List())
	r.Get("/catalog/libraries", h.Libraries())
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

// List handles GET /api/v1/catalog. Optional filter query params author,
// series, genre, tag pass through to the upstream books endpoint untouched.
// genre must be the upstream genre slug (NOT the row id) — see BrowseGenres
// for how this surface remaps id→slug for downstream consumers.
func (h *Handler) List() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The portal echoes the provisioned library's id back as library_id.
		// We own exactly one library; a request scoped to a different library
		// is not ours — return an empty page (200) and never call upstream,
		// so this catalog's books can't surface under a foreign library id.
		if v := r.URL.Query().Get("library_id"); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil && id != syntheticLibraryID {
				writeEnvelope(w, bookwarehouse.Paged[bookwarehouse.Book]{})
				return
			}
		}
		p := bookwarehouse.ListParams{
			Cursor: r.URL.Query().Get("cursor"),
			Sort:   r.URL.Query().Get("sort"),
			Order:  r.URL.Query().Get("order"),
			Author: r.URL.Query().Get("author"),
			Series: r.URL.Query().Get("series"),
			Genre:  r.URL.Query().Get("genre"),
			Tag:    r.URL.Query().Get("tag"),
		}
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				p.Limit = n
			}
		}
		// Use the dedup wrapper so visual duplicates (multiple editions
		// of the same book stored as separate upstream rows) don't show
		// twice in a single page, and so the client's infinite-scroll
		// observer never sees an empty-after-filter page (which would
		// keep it firing forever).
		out, err := h.client.ListBooksDeduped(r.Context(), p, 5)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeEnvelope(w, out)
	}
}

// Libraries handles GET /api/v1/catalog/libraries. The portal's ebook backend
// contract expects {"items":[LibraryInfo...]}; this backend advertises one
// synthetic library so the portal can provision and scope to it.
func (h *Handler) Libraries() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{
				"id":         syntheticLibraryID,
				"name":       "Book Warehouse",
				"media_type": "book",
				"enabled":    true,
			}},
		})
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

// BrowseGenres returns each genre with its SLUG in the id field (not the
// upstream row id), because downstream consumers use this value as the
// genre filter on /catalog, and the upstream books endpoint matches genres
// by slug. See ListBooks query: g.slug = ? in bookwarehouse/handlers/books.go.
func (h *Handler) BrowseGenres() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		out, err := h.client.ListGenres(r.Context(), cursor, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		// Remap id→slug so the returned id is the value the /catalog?genre=
		// filter expects. Fall back to the original id if slug is empty
		// (defensive — upstream always populates slug today).
		for i := range out.Items {
			if out.Items[i].Slug != "" {
				out.Items[i].ID = out.Items[i].Slug
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
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
		// Stream-proxy upstream cover; redirecting won't work because the
		// upstream cover endpoint requires X-API-Key auth that the browser
		// won't send. Upstream maps `large` to `original`.
		upstreamSize := size
		if upstreamSize == "large" {
			upstreamSize = "original"
		}
		resp, err := h.client.GetStream(r.Context(), "/api/v1/books/"+url.PathEscape(bookID)+"/cover/"+url.PathEscape(upstreamSize))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for _, k := range []string{"Content-Type", "Content-Length", "ETag", "Cache-Control", "Last-Modified"} {
			if v := resp.Header.Get(k); v != "" {
				w.Header().Set(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

func (h *Handler) File() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookID := chi.URLParam(r, "book_id")
		// Upstream BookWarehouse: GET /api/v1/books/{id}/download → bytes of
		// the single stored file (`format` is informational — upstream chose
		// the file_format at ingest). API key auth required, so we stream-
		// proxy rather than 302.
		resp, err := h.client.GetStream(r.Context(), "/api/v1/books/"+url.PathEscape(bookID)+"/download")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for _, k := range []string{"Content-Type", "Content-Length", "Content-Disposition", "ETag", "Cache-Control", "Last-Modified", "Accept-Ranges"} {
			if v := resp.Header.Get(k); v != "" {
				w.Header().Set(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
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
