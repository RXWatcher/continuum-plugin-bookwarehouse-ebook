package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/tokens"
)

// maxCatalogLimit caps the page size forwarded upstream. limit is
// attacker-controlled and was forwarded verbatim — limit=999999999 drove a
// giant upstream fetch (amplified ×fanout) that overran the 10 MiB read cap
// and failed to decode, turning into endpoint denial.
const maxCatalogLimit = 100

// clampLimit reads ?limit, returning def when absent/invalid/<=0 and capping
// at maxCatalogLimit otherwise.
func clampLimit(r *http.Request, def int) int {
	l := r.URL.Query().Get("limit")
	if l == "" {
		return def
	}
	n, err := strconv.Atoi(l)
	if err != nil || n <= 0 {
		return def
	}
	if n > maxCatalogLimit {
		return maxCatalogLimit
	}
	return n
}

// parseListParams builds the common catalog params: opaque cursor, clamped
// limit, and allowlisted sort/order (forwarded as upstream control params).
func parseListParams(r *http.Request) bookwarehouse.ListParams {
	q := r.URL.Query()
	p := bookwarehouse.ListParams{Cursor: q.Get("cursor"), Limit: clampLimit(r, 0)}
	switch s := strings.ToLower(q.Get("sort")); s {
	case "title", "author", "year", "series", "added", "rating":
		p.Sort = s
	}
	switch o := strings.ToLower(q.Get("order")); o {
	case "asc", "desc":
		p.Order = o
	}
	return p
}

// upstreamError logs the real error (the ebookdb transport error wraps a
// *url.Error containing the internal upstream base URL) and returns a generic
// 502 to the client.
func upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("bookwarehouse upstream error",
		"method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, "upstream unavailable", http.StatusBadGateway)
}

// syntheticLibraryID is the single library this backend advertises. Book
// Warehouse is one external Calibre catalog with no native library concept,
// so it presents exactly one logical library to the portal. The portal maps
// this id to its provisioned row and echoes it back as ?library_id=.
const syntheticLibraryID int64 = 1

type Handler struct {
	client *bookwarehouse.Client
	secret string
}

// NewHandler constructs a Handler bound to a typed upstream client. secret
// is the HMAC key shared with the ebooks portal — Cover() and File() each
// require a valid signed ?token= matching the book id and file_idx.
func NewHandler(c *bookwarehouse.Client, secret string) *Handler {
	return &Handler{client: c, secret: secret}
}

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
		p := parseListParams(r)
		p.Author = r.URL.Query().Get("author")
		p.Series = r.URL.Query().Get("series")
		p.Genre = r.URL.Query().Get("genre")
		p.Tag = r.URL.Query().Get("tag")
		// Use the dedup wrapper so visual duplicates (multiple editions
		// of the same book stored as separate upstream rows) don't show
		// twice in a single page, and so the client's infinite-scroll
		// observer never sees an empty-after-filter page (which would
		// keep it firing forever).
		out, err := h.client.ListBooksDeduped(r.Context(), p, 5)
		if err != nil {
			upstreamError(w, r, err)
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
		p := parseListParams(r)
		p.Query = r.URL.Query().Get("q")
		// Same dedup safety brake as List so search infinite-scroll doesn't
		// loop on an all-duplicate page and pagination params are honored
		// (previously only ?q= was forwarded, pinning results to page 1).
		out, err := h.client.ListBooksDeduped(r.Context(), p, 5)
		if err != nil {
			upstreamError(w, r, err)
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
			upstreamError(w, r, err)
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
		limit := clampLimit(r, 50)
		out, err := h.client.ListGenres(r.Context(), cursor, limit)
		if err != nil {
			upstreamError(w, r, err)
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
		limit := clampLimit(r, 50)
		out, err := fetch(r.Context(), cursor, limit)
		if err != nil {
			upstreamError(w, r, err)
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
		// Route is declared public on the host plugin proxy; this handler is
		// the only auth gate. file_idx=-1 is the sentinel for cover tokens.
		if _, err := tokens.Verify(h.secret, r.URL.Query().Get("token"), bookID, tokens.CoverFileIdx); err != nil {
			writeTokenError(w, err)
			return
		}
		// Stream-proxy upstream cover; redirecting won't work because the
		// upstream cover endpoint requires X-API-Key auth that the browser
		// won't send. Upstream maps `large` to `original`.
		upstreamSize := size
		if upstreamSize == "large" {
			upstreamSize = "original"
		}
		resp, err := h.client.GetStream(r.Context(), "/api/v1/books/"+url.PathEscape(bookID)+"/cover/"+url.PathEscape(upstreamSize))
		if err != nil {
			upstreamError(w, r, err)
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
		// Route is declared public on the host plugin proxy; this handler is
		// the only auth gate. Ebooks are single-file per book; file_idx=0.
		if _, err := tokens.Verify(h.secret, r.URL.Query().Get("token"), bookID, tokens.FileFileIdx); err != nil {
			writeTokenError(w, err)
			return
		}
		// Upstream BookWarehouse: GET /api/v1/books/{id}/download → bytes of
		// the single stored file (`format` is informational — upstream chose
		// the file_format at ingest). API key auth required, so we stream-
		// proxy rather than 302.
		// Forward the client's Range so seek/resume (readers, Kindle) gets a
		// 206 instead of silently re-downloading the whole file — we already
		// advertise Accept-Ranges below.
		resp, err := h.client.GetStreamWithRange(r.Context(), "/api/v1/books/"+url.PathEscape(bookID)+"/download", r.Header.Get("Range"))
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		defer resp.Body.Close()
		for _, k := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Content-Range", "ETag", "Cache-Control", "Last-Modified", "Accept-Ranges"} {
			if v := resp.Header.Get(k); v != "" {
				w.Header().Set(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

func writeTokenError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	if errors.Is(err, tokens.ErrSecretUnconfigured) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "media signing secret not configured"})
		return
	}
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}

func (h *Handler) ExternalSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "q required", http.StatusBadRequest)
			return
		}
		limit := clampLimit(r, 0)
		hits, err := h.client.ExternalSearch(r.Context(), q, limit)
		if err != nil {
			upstreamError(w, r, err)
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
			upstreamError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"external_id": snap.ID,
			"status":      snap.Status,
		})
	}
}
