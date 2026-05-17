package bookwarehouse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ListParams is the query shape for ListBooks. Author/Series/Genre/Tag are
// optional filters that pass through unchanged to the upstream's GET /books
// (or /books/search) query string. Upstream filters: author/series/publisher
// hit FTS5 by name; genre matches the slug (NOT the row id); tag matches name.
type ListParams struct {
	Cursor string
	Limit  int
	Sort   string
	Order  string
	Query  string // if set, hits /books/search
	Author string
	Series string
	Genre  string // upstream matches by genre slug
	Tag    string
}

// ListBooksDeduped wraps ListBooks with a safety brake: dedup the returned
// page by (isbn13 || normalized title+author), and if the deduped page is
// empty (every book was a dupe of an earlier one in this same fan-out)
// keep advancing the upstream cursor until we get a non-empty page OR run
// out of upstream pages OR hit maxFanout iterations. The returned cursor
// is the upstream cursor *after* the last page we fanned through, so the
// next client request continues from there.
//
// Cross-call dedup (book in page 1 of request 1 vs page 1 of request 2)
// isn't handled here; that'd require per-session state. This function is
// designed to (a) eliminate visual same-page dupes and (b) prevent the
// client's infinite-scroll observer from looping on all-duplicate pages.
func (c *Client) ListBooksDeduped(ctx context.Context, p ListParams, maxFanout int) (Paged[Book], error) {
	if maxFanout <= 0 {
		maxFanout = 10
	}
	target := p.Limit
	if target <= 0 {
		target = 50
	}
	seen := make(map[string]struct{})
	out := Paged[Book]{Items: make([]Book, 0, target)}
	cursor := p.Cursor
	for i := 0; i < maxFanout; i++ {
		pp := p
		pp.Cursor = cursor
		page, err := c.ListBooks(ctx, pp)
		if err != nil {
			return out, err
		}
		out.Total = page.Total
		// Always carry forward the latest upstream cursor: even if everything
		// on this page was a dupe we want the client's next request to
		// continue past it rather than re-asking for the same upstream slice.
		out.NextCursor = page.NextCursor
		for _, b := range page.Items {
			key := dedupKey(b)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out.Items = append(out.Items, b)
		}
		// Stop when we've filled the requested page size, or upstream is
		// exhausted. Earlier code broke on len(out.Items) > 0, which gave
		// the client a near-empty page and made the IntersectionObserver
		// loop on the resulting short scroll viewport.
		if len(out.Items) >= target || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

// dedupKey treats two books as the same if their normalized title + first
// author match — regardless of ISBN. Different editions of the same book
// upstream have different ISBN-13s (hardcover vs paperback vs ebook reissue
// all get their own row), but for a customer-facing library we want one
// card per work, not one per edition. If title is empty we fall back to
// the upstream ID so two missing-title rows don't collapse.
func dedupKey(b Book) string {
	t := normalizeKey(b.Title)
	if t == "" {
		return "id:" + b.ID
	}
	a := ""
	if len(b.Authors) > 0 {
		a = normalizeKey(b.Authors[0])
	}
	// Include series + index: separate volumes of a series share the same
	// title and author but are distinct works and must NOT collapse into a
	// single card (otherwise readers can never reach later volumes). True
	// duplicate editions of one volume still share all four fields (they
	// differ only by ISBN/format, which the key ignores) and still merge.
	return "ta:" + t + "|" + a + "|" + normalizeKey(b.Series) + "|" +
		strconv.FormatFloat(b.SeriesIndex, 'f', -1, 64)
}

func normalizeKey(s string) string {
	out := make([]byte, 0, len(s))
	prevSpace := true
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r+('a'-'A')))
			prevSpace = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			out = append(out, byte(r))
			prevSpace = false
		default:
			if !prevSpace {
				out = append(out, ' ')
				prevSpace = true
			}
		}
	}
	// trim trailing space
	if n := len(out); n > 0 && out[n-1] == ' ' {
		out = out[:n-1]
	}
	return string(out)
}

func (c *Client) ListBooks(ctx context.Context, p ListParams) (Paged[Book], error) {
	q := url.Values{}
	// Upstream BookWarehouse paginates with `?page=N` (1-indexed) and reports
	// {pagination:{page, total_pages}} back. Earlier we treated `cursor` as
	// opaque and forwarded the string straight through — upstream ignored
	// it, returned page 1 every time, and our dedup fan-out got nothing new.
	if p.Cursor != "" {
		q.Set("page", p.Cursor)
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Sort != "" {
		q.Set("sort", p.Sort)
	}
	if p.Order != "" {
		q.Set("order", p.Order)
	}
	if p.Author != "" {
		q.Set("author", p.Author)
	}
	if p.Series != "" {
		q.Set("series", p.Series)
	}
	if p.Genre != "" {
		q.Set("genre", p.Genre)
	}
	if p.Tag != "" {
		q.Set("tag", p.Tag)
	}
	path := "/api/v1/books"
	if p.Query != "" {
		q.Set("q", p.Query)
		path = "/api/v1/books/search"
	}
	body, err := c.Get(ctx, path+"?"+q.Encode())
	if err != nil {
		return Paged[Book]{}, err
	}
	// Upstream BookWarehouse returns {"books":[...], "pagination":{page, limit,
	// total_items, total_pages}} not {"items":[...], "next_cursor":...}.
	// Inner book objects use authors:[{id,name}], file_format (singular string),
	// isbn13, etc. — see LibraryManager's types.ts for the canonical contract.
	var upstream struct {
		Books []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Author  string `json:"author"`
			Authors []struct {
				Name string `json:"name"`
			} `json:"authors"`
			ISBN13      string  `json:"isbn13"`
			Publisher   string  `json:"publisher"`
			PublishedAt string  `json:"published_date"`
			Language    string  `json:"language"`
			HasCover    bool    `json:"has_cover"`
			CoverURL    string  `json:"cover_url"`
			FileFormat  string  `json:"file_format"`
			SeriesName  string  `json:"series"`
			SeriesIndex float64 `json:"series_index"`
		} `json:"books"`
		Pagination struct {
			Page       int `json:"page"`
			Limit      int `json:"limit"`
			TotalItems int `json:"total_items"`
			TotalPages int `json:"total_pages"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(body, &upstream); err != nil {
		return Paged[Book]{}, fmt.Errorf("decode books: %w", err)
	}
	out := Paged[Book]{
		Total: upstream.Pagination.TotalItems,
		Items: make([]Book, 0, len(upstream.Books)),
	}
	// Derive the next cursor from the page we REQUESTED, not from the page
	// upstream echoes back: some responses report page:0 (or omit it), and
	// trusting that produced NextCursor "1" forever (re-fetching page 1 in
	// an infinite loop) or dropped pagination after the first page.
	requested := 1
	if p.Cursor != "" {
		if n, err := strconv.Atoi(p.Cursor); err == nil && n > 0 {
			requested = n
		}
	}
	switch {
	case upstream.Pagination.TotalPages > 0:
		if requested < upstream.Pagination.TotalPages {
			out.NextCursor = strconv.Itoa(requested + 1)
		}
	case p.Limit > 0 && len(upstream.Books) >= p.Limit:
		// TotalPages unknown but the page came back full — assume more.
		out.NextCursor = strconv.Itoa(requested + 1)
	}
	for _, ub := range upstream.Books {
		b := Book{
			ID:          ub.ID,
			Title:       ub.Title,
			ISBN:        ub.ISBN13,
			Publisher:   ub.Publisher,
			Series:      ub.SeriesName,
			SeriesIndex: ub.SeriesIndex,
			Language:    ub.Language,
			HasCover:    ub.HasCover,
		}
		// Build a portal-relative cover URL. The ebooks portal mounts a
		// /api/v1/cover/{id}/{size} route that proxies into our /api/v1/cover
		// (which stream-proxies upstream with the API key). Upstream cover
		// endpoint requires X-API-Key, so a direct browser URL would 401.
		if ub.HasCover {
			// Portal mounts /cover/{id}/{size} as a public route (no auth
			// header needed) so browser <img> tags can render. The portal
			// proxies through to backend's /api/v1/cover endpoint.
			b.CoverURL = "/cover/" + ub.ID + "/" + c.defaultCoverSize
		}
		if len(ub.Authors) > 0 {
			b.Authors = make([]string, len(ub.Authors))
			for i, a := range ub.Authors {
				b.Authors[i] = a.Name
			}
		} else if ub.Author != "" {
			b.Authors = []string{ub.Author}
		}
		if ub.FileFormat != "" {
			b.Formats = []string{ub.FileFormat}
		}
		if len(ub.PublishedAt) >= 4 {
			if y, err := strconv.Atoi(ub.PublishedAt[:4]); err == nil {
				b.Year = y
			}
		}
		out.Items = append(out.Items, b)
	}
	return out, nil
}

func (c *Client) GetBook(ctx context.Context, id string) (BookDetail, error) {
	body, err := c.Get(ctx, "/api/v1/books/"+url.PathEscape(id))
	if err != nil {
		return BookDetail{}, err
	}
	// Upstream Book has authors:[{id,name}], genres:[{id,name,slug}],
	// file_format (singular), file_size, isbn13, published_date — same shape
	// mismatches as ListBooks. Map them through an inline struct.
	var ub struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Author  string `json:"author"`
		Authors []struct {
			Name string `json:"name"`
		} `json:"authors"`
		ISBN13      string `json:"isbn13"`
		Publisher   string `json:"publisher"`
		PublishedAt string `json:"published_date"`
		Language    string `json:"language"`
		HasCover    bool   `json:"has_cover"`
		FileFormat  string `json:"file_format"`
		FileSize    int64  `json:"file_size"`
		Description string `json:"description"`
		Genres      []struct {
			Name string `json:"name"`
		} `json:"genres"`
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
		SeriesName  string  `json:"series"`
		SeriesIndex float64 `json:"series_index"`
	}
	if err := json.Unmarshal(body, &ub); err != nil {
		return BookDetail{}, fmt.Errorf("decode book: %w", err)
	}
	out := BookDetail{
		Book: Book{
			ID:          ub.ID,
			Title:       ub.Title,
			ISBN:        ub.ISBN13,
			Publisher:   ub.Publisher,
			Series:      ub.SeriesName,
			SeriesIndex: ub.SeriesIndex,
			Language:    ub.Language,
			HasCover:    ub.HasCover,
		},
		Description: ub.Description,
	}
	if ub.HasCover {
		out.CoverURL = "/cover/" + ub.ID + "/" + c.defaultCoverSize
	}
	if len(ub.Authors) > 0 {
		out.Authors = make([]string, len(ub.Authors))
		for i, a := range ub.Authors {
			out.Authors[i] = a.Name
		}
	} else if ub.Author != "" {
		out.Authors = []string{ub.Author}
	}
	if ub.FileFormat != "" {
		out.Formats = []string{ub.FileFormat}
		out.Files = []File{{Format: ub.FileFormat, SizeBytes: ub.FileSize}}
	}
	if len(ub.PublishedAt) >= 4 {
		if y, err := strconv.Atoi(ub.PublishedAt[:4]); err == nil {
			out.Year = y
		}
	}
	for _, g := range ub.Genres {
		out.Genres = append(out.Genres, g.Name)
	}
	for _, t := range ub.Tags {
		out.Tags = append(out.Tags, t.Name)
	}
	return out, nil
}

// FileURL returns the upstream URL for fetching a specific format of a book.
func (c *Client) FileURL(bookID, format string) string {
	return fmt.Sprintf("%s/api/v1/books/%s/files/%s", c.baseURL, url.PathEscape(bookID), url.PathEscape(format))
}

// CoverURL returns the deterministic upstream URL for a cover at a given size.
func (c *Client) CoverURL(bookID, size string) string {
	return fmt.Sprintf("%s/api/v1/books/%s/cover/%s", c.baseURL, url.PathEscape(bookID), url.PathEscape(size))
}
