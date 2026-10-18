// SPDX-License-Identifier: AGPL-3.0-only

// Package config parses and validates Folio's environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Mode selects which variables are mandatory.
type Mode int

const (
	ModeServe  Mode = iota // tokens and FOLIO_PUBLIC_URL required
	ModeRender             // `folio render`: no tokens, no public URL
)

// Token is one configured access token. Value is never logged.
type Token struct {
	Name  string
	Value string
}

// String redacts the token value.
func (t Token) String() string { return t.Name + ":<redacted>" }

// LogValue keeps tokens out of slog output.
func (t Token) LogValue() slog.Value { return slog.StringValue(t.String()) }

// Config is the validated configuration (documented defaults).
type Config struct {
	Addr               string
	PublicURL          string
	DataDir            string
	Tokens             []Token
	InsecureNoAuth     bool
	Seed               bool
	RenderConcurrency  int
	RenderQueue        int
	RenderTimeout      time.Duration
	RenderMemory       uint64
	MaxMarkdownBytes   int
	MaxNesting         int
	MaxAssetBytes      int64
	MaxRequestBytes    int64
	MaxPages           int
	TrashRetention     time.Duration
	PreviewRetention   time.Duration
	PreviewPagesStored int
	MaxTableCells      int
	BlobGCGrace        time.Duration
	BackupInterval     time.Duration
	BackupKeep         int
	LogLevel           slog.Level
	LogFormat          string // json | text
	OpenRouterAPIKey   string
	ReviewModel        string
	ReviewMaxPages     int
}

var (
	nameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	tokenRE = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{32,256}$`)
)

// Load reads configuration through getenv (os.LookupEnv in production) and
// returns every problem at once (errors.Join) so operators fix them in one go.
func Load(getenv func(string) (string, bool), mode Mode) (*Config, error) {
	var errs []error
	str := func(k, def string) string {
		if v, ok := getenv(k); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return def
	}
	num := func(k string, def, min int) int {
		v := str(k, "")
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < min {
			errs = append(errs, fmt.Errorf("%s: must be an integer ≥ %d", k, min))
			return def
		}
		return n
	}
	dur := func(k string, def time.Duration, allowZero bool) time.Duration {
		v := str(k, "")
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 || (d == 0 && !allowZero) {
			errs = append(errs, fmt.Errorf("%s: must be a positive Go duration such as 60s or 24h", k))
			return def
		}
		return d
	}
	size := func(k string, def int64) int64 {
		v := str(k, "")
		if v == "" {
			return def
		}
		n, err := ParseSize(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", k, err))
			return def
		}
		return n
	}
	boolean := func(k string, def bool) bool {
		v := str(k, "")
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: must be true or false", k))
			return def
		}
		return b
	}
	c := &Config{}
	c.Addr = str("FOLIO_ADDR", "")
	if c.Addr == "" {
		c.Addr = ":8080"
		if p := str("PORT", ""); p != "" {
			c.Addr = ":" + p
		}
	}
	c.PublicURL = strings.TrimRight(str("FOLIO_PUBLIC_URL", ""), "/")
	c.DataDir = str("FOLIO_DATA_DIR", "/data")
	c.InsecureNoAuth = boolean("FOLIO_INSECURE_NO_AUTH", false)
	c.Seed = boolean("FOLIO_SEED", true)
	c.RenderConcurrency = num("FOLIO_RENDER_CONCURRENCY", 2, 1)
	c.RenderQueue = num("FOLIO_RENDER_QUEUE", 32, 0)
	c.RenderTimeout = dur("FOLIO_RENDER_TIMEOUT", 60*time.Second, false)
	c.RenderMemory = uint64(size("FOLIO_RENDER_MEMORY", 1<<30))
	c.MaxMarkdownBytes = int(size("FOLIO_MAX_MARKDOWN_BYTES", 1<<20))
	c.MaxNesting = num("FOLIO_MAX_NESTING", 32, 1)
	c.MaxAssetBytes = size("FOLIO_MAX_ASSET_BYTES", 10<<20)
	c.MaxRequestBytes = size("FOLIO_MAX_REQUEST_BYTES", 16<<20)
	c.MaxPages = num("FOLIO_MAX_PAGES", 200, 1)
	c.TrashRetention = dur("FOLIO_TRASH_RETENTION", 720*time.Hour, false)
	c.PreviewRetention = dur("FOLIO_PREVIEW_RETENTION", 24*time.Hour, false)
	c.PreviewPagesStored = num("FOLIO_PREVIEW_PAGES_STORED", 3, 0)
	c.MaxTableCells = num("FOLIO_MAX_TABLE_CELLS", 10000, 1)
	c.BlobGCGrace = dur("FOLIO_BLOB_GC_GRACE", time.Hour, false)
	c.BackupInterval = dur("FOLIO_BACKUP_INTERVAL", 24*time.Hour, true)
	c.BackupKeep = num("FOLIO_BACKUP_KEEP", 7, 1)
	c.LogFormat = str("FOLIO_LOG_FORMAT", "json")
	if c.LogFormat != "json" && c.LogFormat != "text" {
		errs = append(errs, errors.New("FOLIO_LOG_FORMAT: must be json or text"))
	}
	if err := c.LogLevel.UnmarshalText([]byte(str("FOLIO_LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, errors.New("FOLIO_LOG_LEVEL: must be debug, info, warn or error"))
	}
	c.OpenRouterAPIKey = str("FOLIO_OPENROUTER_API_KEY", "")
	c.ReviewModel = str("FOLIO_REVIEW_MODEL", "google/gemma-4-31b-it")
	c.ReviewMaxPages = num("FOLIO_REVIEW_MAX_PAGES", 20, 1)
	if c.BlobGCGrace <= 3*c.RenderTimeout {
		errs = append(errs, fmt.Errorf("FOLIO_BLOB_GC_GRACE (%s) must exceed 3 × FOLIO_RENDER_TIMEOUT (%s)", c.BlobGCGrace, 3*c.RenderTimeout))
	}
	if mode == ModeServe {
		errs = append(errs, c.loadTokens(str("FOLIO_TOKEN", ""), str("FOLIO_TOKENS", ""))...)
		if u, err := url.Parse(c.PublicURL); c.PublicURL == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, errors.New("FOLIO_PUBLIC_URL: required, an absolute http(s) URL such as https://folio.example.com"))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

func (c *Config) loadTokens(single, multi string) []error {
	var errs []error
	if single != "" {
		c.Tokens = append(c.Tokens, Token{Name: "default", Value: single})
	}
	for _, pair := range strings.Split(multi, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, tok, ok := strings.Cut(pair, ":")
		name, tok = strings.TrimSpace(name), strings.TrimSpace(tok)
		if !ok || !nameRE.MatchString(name) {
			errs = append(errs, fmt.Errorf("FOLIO_TOKENS: entries are name:token with name matching %s", nameRE))
			continue
		}
		c.Tokens = append(c.Tokens, Token{Name: name, Value: tok})
	}
	names, values := map[string]bool{}, map[string]bool{}
	for _, t := range c.Tokens {
		if !tokenRE.MatchString(t.Value) {
			errs = append(errs, fmt.Errorf("token %q: must be 32–256 characters of [A-Za-z0-9._~+/=-]", t.Name))
		}
		if names[t.Name] || values[t.Value] {
			errs = append(errs, fmt.Errorf("token %q: duplicate token name or value", t.Name))
		}
		names[t.Name], values[t.Value] = true, true
	}
	if len(c.Tokens) == 0 && !c.InsecureNoAuth {
		errs = append(errs, errors.New("no token configured: set FOLIO_TOKEN or FOLIO_TOKENS (or FOLIO_INSECURE_NO_AUTH=true for local development only)"))
	}
	return errs
}

// ParseSize parses "1GiB", "16MiB", "512KiB" or a plain byte count.
func ParseSize(s string) (int64, error) {
	units := []struct {
		suffix string
		mult   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}}
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSuffix(s, u.suffix), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 || n > (1<<62)/mult {
		return 0, errors.New("must be a positive size such as 1GiB, 16MiB or 1048576")
	}
	return n * mult, nil
}
