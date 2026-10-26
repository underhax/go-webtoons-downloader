// Package webtoon implements the HTTP client and parser for Webtoons endpoints.
package webtoon

// SeriesInfo contains metadata extracted from a Webtoons series URL and page.
type SeriesInfo struct {
	Language   string
	SeriesType string
	TitleSlug  string
	TitleName  string
	Author     string
	Genre      string
	Summary    string
	TitleNo    int
}

// Chapter represents an individual episode in a series.
type Chapter struct {
	Title     string
	ViewerURL string
	Number    int
	EpisodeNo int
}
