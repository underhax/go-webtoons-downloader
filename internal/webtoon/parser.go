package webtoon

import (
	"errors"
	"fmt"
	"html"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Sentinels returned when URL parsing or document extraction fails.
var (
	ErrInvalidURL      = errors.New("invalid webtoon url")
	ErrTitleNoNotFound = errors.New("series title_no not found")
	ErrNoImagesFound   = errors.New("no image urls found in chapter html")

	reCanonicalTitleNo     = regexp.MustCompile(`(?i)<link[^>]*rel=["']canonical["'][^>]*href=["'][^"']*title_no=([0-9]+)`)
	reOGTitlePropertyFirst = regexp.MustCompile(`(?i)<meta[^>]*property=["']og:title["'][^>]*content=(?:"([^"]*)"|'([^']*)')`)
	reOGTitleContentFirst  = regexp.MustCompile(`(?i)<meta[^>]*content=(?:"([^"]*)"|'([^']*)')[^>]*property=["']og:title["']`)
	reSubjH1               = regexp.MustCompile(`(?i)<h1[^>]*class=(?:"[^"]*\bsubj\b[^"]*"|'[^']*\bsubj\b[^']*')[^>]*>([^<]+)</h1>`)
	reSubjH3               = regexp.MustCompile(`(?i)<h3[^>]*class=(?:"[^"]*\bsubj\b[^"]*"|'[^']*\bsubj\b[^']*')[^>]*>([^<]+)</h3>`)
	reGenre                = regexp.MustCompile(`(?i)<h2[^>]*class=(?:"[^"]*\bgenre\b[^"]*"|'[^']*\bgenre\b[^']*')[^>]*>([^<]+)</h2>`)
	reAuthorMeta           = regexp.MustCompile(`(?i)<meta[^>]*(?:property|name)=["'](?:com-linewebtoon:webtoon:author|author)["'][^>]*content=(?:"([^"]*)"|'([^']*)')`)
	reAuthorArea           = regexp.MustCompile(`(?i)<div[^>]*class=(?:"[^"]*\bauthor_area\b[^"]*"|'[^']*\bauthor_area\b[^']*')[^>]*>([\s\S]*?)</div>`)
	reSummaryP             = regexp.MustCompile(`(?i)<p[^>]*class=(?:"[^"]*\bsummary\b[^"]*"|'[^']*\bsummary\b[^']*')[^>]*>([\s\S]*?)</p>`)
	reSummaryMeta          = regexp.MustCompile(`(?i)<meta[^>]*(?:property=["']og:description["']|name=["']description["'])[^>]*content=(?:"([^"]*)"|'([^']*)')`)
	reButtonTag            = regexp.MustCompile(`(?i)<button[\s\S]*?</button>`)
	reHTMLTags             = regexp.MustCompile(`<[^>]+>`)
	reViewerDivStart       = regexp.MustCompile(`(?i)<div[^>]*(?:class=["'][^"']*_img_viewer_area[^"']*["']|id=["']_viewerBox["'])[^>]*>`)
	reImgDataURL           = regexp.MustCompile(`(?i)<img[^>]*data-url=["']([^"']+)["']`)
	reLanguage             = regexp.MustCompile(`^[a-zA-Z]{2}(?:-[a-zA-Z0-9]+)?$`)
	reSegment              = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
)

func isAllowedWebtoonHost(rawHost string) bool {
	host := strings.TrimRight(strings.ToLower(rawHost), ".")
	if h, port, err := net.SplitHostPort(host); err == nil {
		if port != "443" {
			return false
		}
		host = strings.TrimRight(h, ".")
	}
	return host == "webtoons.com" || host == "www.webtoons.com" || host == "m.webtoons.com"
}

// IsValidSeriesURL returns true if the raw URL strictly satisfies the Webtoon series URL format.
func IsValidSeriesURL(rawURL string) bool {
	_, err := ParseSeriesURL(rawURL)
	return err == nil
}

func validatePathSegments(path, rawURL string) (lang, genre, slug string, err error) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) != 4 {
		return "", "", "", fmt.Errorf("%w: invalid path structure in %s", ErrInvalidURL, rawURL)
	}

	lang = strings.ToLower(segments[0])
	if !reLanguage.MatchString(lang) {
		return "", "", "", fmt.Errorf("%w: invalid language segment %s", ErrInvalidURL, segments[0])
	}

	genre = segments[1]
	if !reSegment.MatchString(genre) {
		return "", "", "", fmt.Errorf("%w: invalid genre segment %s", ErrInvalidURL, segments[1])
	}

	slug = segments[2]
	if !reSegment.MatchString(slug) {
		return "", "", "", fmt.Errorf("%w: invalid title slug %s", ErrInvalidURL, segments[2])
	}

	if !strings.EqualFold(segments[3], "list") {
		return "", "", "", fmt.Errorf("%w: expected list endpoint, got %s", ErrInvalidURL, segments[3])
	}

	return lang, genre, slug, nil
}

func parseTitleNo(rawQuery, rawURL string) (int, error) {
	parsedQuery, err := url.ParseQuery(rawQuery)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid query in %s", ErrInvalidURL, rawURL)
	}

	titleNoStr := parsedQuery.Get("title_no")
	titleNo, err := strconv.Atoi(titleNoStr)
	if err != nil || titleNo <= 0 {
		return 0, fmt.Errorf("%w: missing or invalid title_no in %s", ErrInvalidURL, rawURL)
	}

	return titleNo, nil
}

// ParseSeriesURL parses the provided Webtoon URL and extracts structural metadata.
func ParseSeriesURL(rawURL string) (*SeriesInfo, error) {
	trimmed := strings.TrimSpace(rawURL)
	trimmed = strings.Trim(trimmed, "\"'")
	if trimmed == "" {
		return nil, ErrInvalidURL
	}

	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidURL, rawURL)
	}

	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: unsupported scheme %s", ErrInvalidURL, parsed.Scheme)
	}

	if parsed.User != nil {
		return nil, fmt.Errorf("%w: userinfo not allowed", ErrInvalidURL)
	}

	if !isAllowedWebtoonHost(parsed.Host) {
		return nil, fmt.Errorf("%w: invalid domain %s", ErrInvalidURL, parsed.Host)
	}

	lang, genre, titleSlug, err := validatePathSegments(parsed.Path, rawURL)
	if err != nil {
		return nil, err
	}

	titleNo, err := parseTitleNo(parsed.RawQuery, rawURL)
	if err != nil {
		return nil, err
	}

	seriesType := "webtoon"
	if strings.EqualFold(genre, "canvas") {
		seriesType = "canvas"
	}

	return &SeriesInfo{
		Language:   lang,
		SeriesType: seriesType,
		TitleSlug:  titleSlug,
		TitleName:  titleSlug,
		Genre:      genre,
		TitleNo:    titleNo,
	}, nil
}

// ExtractTitleNo parses canonical link metadata from series HTML to locate title_no.
func ExtractTitleNo(pageHTML string) (int, error) {
	matches := reCanonicalTitleNo.FindStringSubmatch(pageHTML)
	if len(matches) < 2 {
		return 0, ErrTitleNoNotFound
	}

	titleNo, err := strconv.Atoi(matches[1])
	if err != nil || titleNo <= 0 {
		return 0, fmt.Errorf("%w: invalid title_no parsed: %s", ErrTitleNoNotFound, matches[1])
	}

	return titleNo, nil
}

// ExtractSeriesTitle extracts the human-readable comic name from page markup.
func ExtractSeriesTitle(pageHTML, fallbackSlug string) string {
	if title := extractMetaOGTitle(pageHTML); title != "" {
		return title
	}

	if matches := reSubjH1.FindStringSubmatch(pageHTML); len(matches) >= 2 {
		if cleaned := strings.TrimSpace(matches[1]); cleaned != "" {
			return html.UnescapeString(cleaned)
		}
	}

	if matches := reSubjH3.FindStringSubmatch(pageHTML); len(matches) >= 2 {
		if cleaned := strings.TrimSpace(matches[1]); cleaned != "" {
			return html.UnescapeString(cleaned)
		}
	}

	return fallbackSlug
}

func extractMetaOGTitle(pageHTML string) string {
	if matches := reOGTitlePropertyFirst.FindStringSubmatch(pageHTML); len(matches) >= 2 {
		val := matches[1]
		if val == "" && len(matches) >= 3 {
			val = matches[2]
		}
		if cleaned := strings.TrimSpace(val); cleaned != "" {
			return html.UnescapeString(cleaned)
		}
	}

	if matches := reOGTitleContentFirst.FindStringSubmatch(pageHTML); len(matches) >= 2 {
		val := matches[1]
		if val == "" && len(matches) >= 3 {
			val = matches[2]
		}
		if cleaned := strings.TrimSpace(val); cleaned != "" {
			return html.UnescapeString(cleaned)
		}
	}

	return ""
}

// ExtractGenre retrieves the comic genre tag from the series header markup.
func ExtractGenre(pageHTML string) string {
	matches := reGenre.FindStringSubmatch(pageHTML)
	if len(matches) >= 2 {
		return html.UnescapeString(strings.TrimSpace(matches[1]))
	}
	return ""
}

// ExtractAuthor retrieves author and artist credits from meta tags or header container.
func ExtractAuthor(pageHTML string) string {
	if matches := reAuthorMeta.FindStringSubmatch(pageHTML); len(matches) >= 2 {
		val := matches[1]
		if val == "" && len(matches) >= 3 {
			val = matches[2]
		}
		if cleaned := strings.TrimSpace(val); cleaned != "" {
			return html.UnescapeString(cleaned)
		}
	}

	matches := reAuthorArea.FindStringSubmatch(pageHTML)
	if len(matches) < 2 {
		return ""
	}

	stripped := reButtonTag.ReplaceAllString(matches[1], " ")
	stripped = reHTMLTags.ReplaceAllString(stripped, " ")
	fields := strings.Fields(stripped)
	joined := strings.Join(fields, " ")
	normalized := strings.ReplaceAll(joined, " , ", ", ")
	normalized = strings.ReplaceAll(normalized, " ,", ",")
	return html.UnescapeString(strings.Trim(normalized, " ,"))
}

// ExtractSummary retrieves the series synopsis from description tags or paragraphs.
func ExtractSummary(pageHTML string) string {
	if matches := reSummaryP.FindStringSubmatch(pageHTML); len(matches) >= 2 {
		stripped := reHTMLTags.ReplaceAllString(matches[1], " ")
		cleaned := strings.Join(strings.Fields(stripped), " ")
		if cleaned != "" {
			return html.UnescapeString(cleaned)
		}
	}

	if matches := reSummaryMeta.FindStringSubmatch(pageHTML); len(matches) >= 2 {
		val := matches[1]
		if val == "" && len(matches) >= 3 {
			val = matches[2]
		}
		if cleaned := strings.TrimSpace(val); cleaned != "" {
			return html.UnescapeString(cleaned)
		}
	}

	return ""
}

// ExtractImageURLs parses viewer images from the chapter viewer DOM.
func ExtractImageURLs(pageHTML string) ([]string, error) {
	loc := reViewerDivStart.FindStringIndex(pageHTML)
	if loc == nil {
		return nil, ErrNoImagesFound
	}

	containerContent := extractViewerContainer(pageHTML[loc[1]:])
	matches := reImgDataURL.FindAllStringSubmatch(containerContent, -1)
	if len(matches) == 0 {
		return nil, ErrNoImagesFound
	}

	urls := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) >= 2 {
			imgURL := strings.TrimSpace(m[1])
			if imgURL != "" {
				imgURL = strings.Replace(imgURL, "?type=q90", "", 1)
				urls = append(urls, imgURL)
			}
		}
	}

	if len(urls) == 0 {
		return nil, ErrNoImagesFound
	}

	return urls, nil
}

func extractViewerContainer(s string) string {
	depth := 1
	idx := 0
	lower := strings.ToLower(s)

	for idx < len(s) {
		nextOpen := strings.Index(lower[idx:], "<div")
		nextClose := strings.Index(lower[idx:], "</div>")

		if nextClose == -1 {
			return s
		}

		if nextOpen != -1 && nextOpen < nextClose {
			depth++
			idx += nextOpen + 4
		} else {
			depth--
			if depth == 0 {
				return s[:idx+nextClose]
			}
			idx += nextClose + 6
		}
	}

	return s
}
