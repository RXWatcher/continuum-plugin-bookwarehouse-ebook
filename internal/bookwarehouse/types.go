package bookwarehouse

// Book is the upstream summary of an ebook. Some fields are optional.
type Book struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	ISBN        string   `json:"isbn,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Series      string   `json:"series,omitempty"`
	SeriesIndex float64  `json:"series_index,omitempty"`
	Year        int      `json:"year,omitempty"`
	Language    string   `json:"language,omitempty"`
	CoverURL    string   `json:"cover_url,omitempty"`
	HasCover    bool     `json:"has_cover"`
	Rating      float64  `json:"rating,omitempty"`
	Formats     []string `json:"formats,omitempty"`
}

// BookDetail extends Book with description, files, tags, genres.
type BookDetail struct {
	Book
	Description string   `json:"description,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Files       []File   `json:"files,omitempty"`
}

type File struct {
	Format    string `json:"format"`
	SizeBytes int64  `json:"file_size"`
	URL       string `json:"url,omitempty"`
}

type Paged[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      int    `json:"total,omitempty"`
}

type Author struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count,omitempty"`
}

type Series struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count,omitempty"`
}

type Genre struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count,omitempty"`
}

type Tag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count,omitempty"`
}

// ExternalSearchHit comes from BW's wrapper around Open Library + Google Books.
type ExternalSearchHit struct {
	SourceID  string   `json:"source_id"`
	Source    string   `json:"source"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors,omitempty"`
	Year      int      `json:"year,omitempty"`
	Language  string   `json:"language,omitempty"`
	Formats   []string `json:"formats,omitempty"`
	SizeBytes int64    `json:"size_bytes,omitempty"`
	CoverURL  string   `json:"cover_url,omitempty"`
}
