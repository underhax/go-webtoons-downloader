// Package config handles command-line arguments, environment variable overrides, and validation.
package config

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/underhax/go-webtoons-downloader/internal/validator"
)

// Application version and validation errors.
var (
	AppVersion = "dev"

	ErrInvalidFormat      = validator.ErrInvalidFormat
	ErrInvalidNaming      = validator.ErrInvalidNaming
	ErrInvalidConcurrency = validator.ErrInvalidConcurrency
	ErrInvalidDelay       = validator.ErrInvalidDelay
	ErrInvalidTimeout     = validator.ErrInvalidTimeout
	ErrInvalidIndex       = validator.ErrInvalidIndex
	ErrInvalidStart       = validator.ErrInvalidStart
	ErrConflictingOptions = validator.ErrConflictingOptions
	ErrInvalidURL         = validator.ErrInvalidURL
	ErrInvalidPath        = validator.ErrInvalidPath
	ErrSymlinkForbidden   = validator.ErrSymlinkForbidden
	ErrPathTraversal      = validator.ErrPathTraversal
	ErrInvalidEnvVar      = validator.ErrInvalidEnvVar
)

// Supported formats and folder naming styles.
const (
	FormatPDF    = validator.FormatPDF
	FormatCBZ    = validator.FormatCBZ
	FormatZIP    = validator.FormatZIP
	FormatImages = validator.FormatImages

	NamingSlug  = validator.NamingSlug
	NamingTitle = validator.NamingTitle

	defaultDirectory   = "./data/webtoons"
	defaultConcurrency = 120
	defaultDelay       = 0
	defaultTimeout     = 30 * time.Second
)

// Config holds runtime options resolved from flags, environment variables, and defaults.
type Config struct {
	Directory   string
	LinksFile   string
	Format      string
	URL         string
	Naming      string
	Index       int
	Start       int
	Concurrency int
	Delay       time.Duration
	Timeout     time.Duration
	Version     bool
	Update      bool
	Silent      bool
	SeriesJSON  bool
}

type rawFlags struct {
	visited     map[string]bool
	dir         string
	links       string
	format      string
	url         string
	naming      string
	index       int
	start       int
	concurrency int
	delay       time.Duration
	timeout     time.Duration
	version     bool
	update      bool
	silent      bool
	seriesJSON  bool
}

func (r *rawFlags) has(names ...string) bool {
	for _, name := range names {
		if r.visited[name] {
			return true
		}
	}
	return false
}

// Parse parses arguments and environment variables into a validated Config.
// Flag values take precedence over environment variables, which take precedence over defaults.
func Parse(args []string, output io.Writer) (*Config, error) {
	fs := flag.NewFlagSet("wtd", flag.ContinueOnError)
	fs.SetOutput(output)

	rf := &rawFlags{
		visited:     make(map[string]bool),
		concurrency: defaultConcurrency,
	}

	fs.StringVar(&rf.dir, "dir", "", "")
	fs.StringVar(&rf.dir, "d", "", "")
	fs.StringVar(&rf.links, "links", "", "")
	fs.StringVar(&rf.links, "l", "", "")
	fs.StringVar(&rf.format, "format", "", "")
	fs.StringVar(&rf.format, "f", "", "")
	fs.StringVar(&rf.url, "url", "", "")
	fs.StringVar(&rf.url, "u", "", "")
	fs.StringVar(&rf.naming, "naming", "", "")
	fs.IntVar(&rf.index, "index", 0, "")
	fs.IntVar(&rf.index, "i", 0, "")
	fs.IntVar(&rf.start, "start", 0, "")
	fs.IntVar(&rf.start, "s", 0, "")
	fs.IntVar(&rf.concurrency, "concurrency", defaultConcurrency, "")
	fs.IntVar(&rf.concurrency, "c", defaultConcurrency, "")
	fs.DurationVar(&rf.delay, "delay", 0, "")
	fs.DurationVar(&rf.timeout, "timeout", 0, "")
	fs.BoolVar(&rf.version, "version", false, "")
	fs.BoolVar(&rf.version, "v", false, "")
	fs.BoolVar(&rf.update, "update", false, "")
	fs.BoolVar(&rf.silent, "silent", false, "")
	fs.BoolVar(&rf.silent, "q", false, "")
	fs.BoolVar(&rf.seriesJSON, "series-json", false, "")

	fs.Usage = func() {
		printCustomUsage(output)
	}

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("parse flags: %w", err)
	}

	if rf.version || rf.update {
		return &Config{
			Directory:   defaultDirectory,
			Format:      FormatPDF,
			Naming:      NamingSlug,
			Concurrency: defaultConcurrency,
			Delay:       defaultDelay,
			Timeout:     defaultTimeout,
			Version:     rf.version,
			Update:      rf.update,
		}, nil
	}

	fs.Visit(func(f *flag.Flag) {
		rf.visited[f.Name] = true
	})

	return buildValidatedConfig(rf)
}

func buildValidatedConfig(rf *rawFlags) (*Config, error) {
	dir, _ := resolveStringOption(rf.has("dir", "d"), rf.dir, "WEBTOONS_DIR", defaultDirectory)
	links, explicitLinks := resolveStringOption(rf.has("links", "l"), rf.links, "WEBTOONS_LINKS", defaultLinksPath())
	rawFormat, _ := resolveStringOption(rf.has("format", "f"), rf.format, "WEBTOONS_FORMAT", FormatPDF)
	url, explicitURL := resolveStringOption(rf.has("url", "u"), rf.url, "WEBTOONS_URL", "")
	rawNaming, _ := resolveStringOption(rf.has("naming"), rf.naming, "WEBTOONS_NAMING", NamingSlug)

	index, err := resolveOptionalPositiveInt(rf.has("index", "i"), rf.index, "WEBTOONS_INDEX", validator.ValidateIndex)
	if err != nil {
		return nil, err
	}

	start, err := resolveOptionalPositiveInt(rf.has("start", "s"), rf.start, "WEBTOONS_START", validator.ValidateStart)
	if err != nil {
		return nil, err
	}

	concurrency, err := resolveConcurrency(rf.has("concurrency", "c"), rf.concurrency)
	if err != nil {
		return nil, err
	}

	delay, err := resolveDurationOption(rf.has("delay"), rf.delay, "WEBTOONS_DELAY", defaultDelay, validator.ValidateDelay)
	if err != nil {
		return nil, err
	}

	timeout, err := resolveDurationOption(rf.has("timeout"), rf.timeout, "WEBTOONS_TIMEOUT", defaultTimeout, validator.ValidateTimeout)
	if err != nil {
		return nil, err
	}

	silent, err := resolveBoolOption(rf.silent, "WEBTOONS_SILENT")
	if err != nil {
		return nil, err
	}

	seriesJSON, err := resolveBoolOption(rf.seriesJSON, "WEBTOONS_SERIES_JSON")
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Directory:   dir,
		LinksFile:   links,
		Format:      strings.ToLower(rawFormat),
		URL:         url,
		Naming:      strings.ToLower(rawNaming),
		Index:       index,
		Start:       start,
		Concurrency: concurrency,
		Delay:       delay,
		Timeout:     timeout,
		Silent:      silent,
		SeriesJSON:  seriesJSON,
	}

	if err := cfg.validate(explicitURL, explicitLinks); err != nil {
		return nil, err
	}

	return cfg, nil
}

func printCustomUsage(w io.Writer) {
	usageText := fmt.Sprintf(`wtd - Webtoons Downloader & Syncer (%s)

Usage:
  wtd --url '<WEBTOON_URL>' [--start <NUMBER>] [GLOBAL OPTIONS]
  wtd --links <PATH> [--index <NUMBER> [--start <NUMBER>]] [GLOBAL OPTIONS]
  wtd --update
  wtd --version
  wtd --help

Target Selection:
  -u, --url '<WEBTOON_URL>'         Download a single webtoon series directly (env: WEBTOONS_URL)
  -l, --links <PATH>                Synchronize series from a links file (env: WEBTOONS_LINKS, default: ./links.txt)

Dependent Options:
  -i, --index <NUMBER>              Series line number in links.txt (skipping comments/empty lines) (requires --links) (env: WEBTOONS_INDEX)
  -s, --start <NUMBER>              Start chapter number (requires --url or --index) (env: WEBTOONS_START)

Global Options (apply to both --url and --links):
  -d, --dir <PATH>                  Target storage directory (env: WEBTOONS_DIR, default: ./data/webtoons)
  -f, --format <pdf|cbz|zip|images> Output format (env: WEBTOONS_FORMAT, default: pdf)
      --naming <slug|title>         Folder naming style: URL slug or official title (env: WEBTOONS_NAMING, default: slug)
  -c, --concurrency <NUMBER>        Concurrent page downloads (1-200) (env: WEBTOONS_CONCURRENCY, default: 120)
      --delay <DURATION<ms|s|m>>    Request delay to avoid rate limiting (default: 0s) (env: WEBTOONS_DELAY)
      --timeout <DURATION<ms|s|m>>  Network request timeout (default: 30s) (env: WEBTOONS_TIMEOUT)
  -q, --silent                      Silent mode, suppress progress output (env: WEBTOONS_SILENT)
      --series-json                 Generate series.json metadata file (always created for images format) (env: WEBTOONS_SERIES_JSON)

Other Options:
      --update                      Update executable to latest GitHub release
  -v, --version                     Show application version
  -h, --help                        Show this help message
`, AppVersion)
	if _, err := io.WriteString(w, usageText); err != nil {
		return
	}
}

func defaultLinksPath() string {
	cleanLocal := filepath.Clean("links.txt")
	if _, err := os.Stat(cleanLocal); err == nil {
		return cleanLocal
	}

	if userCfg, err := os.UserConfigDir(); err == nil && userCfg != "" {
		return filepath.Clean(filepath.Join(userCfg, "wtd", "links.txt"))
	}

	return cleanLocal
}

func resolveStringOption(setByFlag bool, flagVal, envKey, defaultVal string) (string, bool) {
	if setByFlag {
		return flagVal, true
	}
	if envVal, ok := os.LookupEnv(envKey); ok && envVal != "" {
		return envVal, true
	}
	return defaultVal, false
}

func resolveOptionalPositiveInt(setByFlag bool, flagVal int, envKey string, validateFn func(int) error) (int, error) {
	if setByFlag {
		if err := validateFn(flagVal); err != nil {
			return 0, fmt.Errorf("validate %s: %w", envKey, err)
		}
		return flagVal, nil
	}
	if envVal, ok := os.LookupEnv(envKey); ok && envVal != "" {
		parsed, err := strconv.Atoi(envVal)
		if err != nil {
			return 0, fmt.Errorf("%w: %s=%q: %w", ErrInvalidEnvVar, envKey, envVal, err)
		}
		if err := validateFn(parsed); err != nil {
			return 0, fmt.Errorf("validate %s: %w", envKey, err)
		}
		return parsed, nil
	}
	return 0, nil
}

func resolveConcurrency(setByFlag bool, flagVal int) (int, error) {
	val := defaultConcurrency
	if setByFlag {
		val = flagVal
	} else if envVal, ok := os.LookupEnv("WEBTOONS_CONCURRENCY"); ok && envVal != "" {
		parsed, err := strconv.Atoi(envVal)
		if err != nil {
			return 0, fmt.Errorf("%w: WEBTOONS_CONCURRENCY=%q: %w", ErrInvalidEnvVar, envVal, err)
		}
		val = parsed
	}
	if err := validator.ValidateConcurrency(val); err != nil {
		return 0, fmt.Errorf("validate concurrency: %w", err)
	}
	return val, nil
}

func resolveDurationOption(
	setByFlag bool,
	flagVal time.Duration,
	envKey string,
	defaultVal time.Duration,
	validateFn func(time.Duration) error,
) (time.Duration, error) {
	val := defaultVal
	if setByFlag {
		val = flagVal
	} else if envVal, ok := os.LookupEnv(envKey); ok && envVal != "" {
		parsed, err := time.ParseDuration(envVal)
		if err != nil {
			return 0, fmt.Errorf("%w: %s=%q: %w", ErrInvalidEnvVar, envKey, envVal, err)
		}
		val = parsed
	}
	if err := validateFn(val); err != nil {
		return 0, fmt.Errorf("validate %s: %w", envKey, err)
	}
	return val, nil
}

func resolveBoolOption(flagVal bool, envKey string) (bool, error) {
	if flagVal {
		return true, nil
	}
	if envVal, ok := os.LookupEnv(envKey); ok && envVal != "" {
		parsed, err := strconv.ParseBool(envVal)
		if err != nil {
			return false, fmt.Errorf("%w: %s=%q: %w", ErrInvalidEnvVar, envKey, envVal, err)
		}
		return parsed, nil
	}
	return false, nil
}

func (c *Config) validate(explicitURL, explicitLinks bool) error {
	if err := validator.ValidateFormat(c.Format); err != nil {
		return fmt.Errorf("validate format: %w", err)
	}

	if err := validator.ValidateNaming(c.Naming); err != nil {
		return fmt.Errorf("validate naming: %w", err)
	}

	if err := validator.ValidateOptionConflicts(c.URL, c.Index, c.Start, explicitLinks); err != nil {
		return fmt.Errorf("validate option conflicts: %w", err)
	}

	if explicitURL || c.URL != "" {
		if err := validator.ValidateSeriesURL(c.URL); err != nil {
			return fmt.Errorf("validate url: %w", err)
		}
	}

	if _, err := validator.ValidateDirPath(c.Directory); err != nil {
		return fmt.Errorf("validate directory: %w", err)
	}

	if c.URL == "" {
		if _, err := validator.ValidateLinksFilePath(c.LinksFile); err != nil {
			return fmt.Errorf("validate links file: %w", err)
		}
	}

	return nil
}
