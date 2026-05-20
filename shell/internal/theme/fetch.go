package theme

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	proto "github.com/convergent-systems-co/aish/libs/proto/theme"
)

// DefaultRegistryURL is the canonical Brand-Atoms host aish points at
// when no override is configured. Theme TOMLs live at
// <DefaultRegistryURL>/themes/<id>.toml and the catalog at
// <DefaultRegistryURL>/themes/index.json.
const DefaultRegistryURL = "https://theme-atoms.com"

// indexPath / themePath compose the well-known URLs Brand Atoms publishes.
const (
	indexPath = "/themes/index.json"
	themePath = "/themes/%s.toml"
)

// CacheDirName is the per-user cache directory under $HOME/.aish/.
// Each fetched theme's ORIGINAL v1 TOML lands at
// $HOME/.aish/themes/cache/<id>.toml so future aish versions can
// re-convert from the canonical bytes (we may add fields over time).
const CacheDirName = "themes/cache"

// IndexEntry is one record in the catalog returned by /themes/index.json.
type IndexEntry struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	DisplayName string `json:"display_name"`
	URL         string `json:"url"`
}

// Index is the deserialised catalog.
type Index struct {
	SchemaVersion string       `json:"schema_version"`
	GeneratedAt   string       `json:"generated_at"`
	Themes        []IndexEntry `json:"themes"`
}

// Client speaks the Brand-Atoms HTTP protocol: a JSON index endpoint and
// per-theme TOML downloads. Construct via NewClient; safe for sequential
// reuse (the underlying *http.Client carries its own concurrency story).
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a Client pointing at baseURL (DefaultRegistryURL if
// empty). httpClient defaults to a 30-second timeout. The returned
// client never retains the response bodies; each call is self-contained.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultRegistryURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// BaseURL returns the registry base URL the client points at — used by
// status output ("syncing from <url>...").
func (c *Client) BaseURL() string {
	return c.baseURL
}

// FetchIndex GETs /themes/index.json and returns the parsed catalog.
// Network and JSON errors propagate; non-2xx responses become an error
// citing the status code.
func (c *Client) FetchIndex(ctx context.Context) (Index, error) {
	url := c.baseURL + indexPath
	body, err := c.get(ctx, url)
	if err != nil {
		return Index{}, err
	}
	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return Index{}, fmt.Errorf("fetch: parse index JSON from %s: %w", url, err)
	}
	return idx, nil
}

// FetchTheme GETs a single theme TOML by id (or absolute URL). The
// raw bytes and the parsed proto.Brand are both returned — the caller
// can persist the bytes verbatim (preserving any fields aish doesn't
// yet understand) while consuming the Brand for rendering today.
func (c *Client) FetchTheme(ctx context.Context, idOrURL string) ([]byte, proto.Brand, error) {
	url := idOrURL
	if !strings.Contains(idOrURL, "://") {
		url = c.baseURL + fmt.Sprintf(themePath, idOrURL)
	}
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, proto.Brand{}, err
	}
	brand, err := parseV1(body)
	if err != nil {
		return body, proto.Brand{}, fmt.Errorf("fetch: parse %s: %w", url, err)
	}
	return body, brand, nil
}

// get is the inner HTTP helper with context + status-code checking.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch: build request for %s: %w", url, err)
	}
	req.Header.Set("Accept", "application/toml, application/json")
	req.Header.Set("User-Agent", "aish-theme-fetcher/0.2.5")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("fetch: GET %s returned HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// SyncOptions configures Sync.
type SyncOptions struct {
	// CacheDir is the destination for fetched TOML files. Defaults to
	// $HOME/.aish/themes/cache when empty.
	CacheDir string
	// Only restricts the sync to a subset of theme IDs. Empty means all
	// themes in the published index.
	Only []string
	// Concurrency is the parallel-fetch limit. Defaults to 4.
	Concurrency int
}

// SyncResult summarises one Sync invocation.
type SyncResult struct {
	// Cached is the IDs of themes successfully fetched and written to
	// CacheDir.
	Cached []string
	// Registered is the IDs added to the registry.
	Registered []string
	// Errors is per-theme fetch/parse failures. The sync continues past
	// individual failures — partial success is normal.
	Errors map[string]error
}

// Sync fetches the published index, downloads each theme TOML in
// parallel, persists the raw bytes to CacheDir, and registers the parsed
// Brand on the given Registry. Failures on individual themes are
// collected in SyncResult.Errors; Sync returns an error only on
// catastrophic problems (cannot read index, cannot create CacheDir).
//
// Cache layout:
//
//	$HOME/.aish/themes/cache/<id>.toml          (raw v1 TOML, verbatim)
//
// On subsequent invocations, themes already present in the cache are
// re-fetched (assuming the index version may have changed). Future
// optimisation: ETag / If-Modified-Since.
func (c *Client) Sync(ctx context.Context, env []string, reg *Registry, opts SyncOptions) (SyncResult, error) {
	result := SyncResult{Errors: make(map[string]error)}

	cacheDir := opts.CacheDir
	if cacheDir == "" {
		home := homeDir(env)
		if home == "" {
			return result, fmt.Errorf("sync: $HOME / $USERPROFILE unset; cannot pick cache dir")
		}
		cacheDir = filepath.Join(home, ".aish", CacheDirName)
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return result, fmt.Errorf("sync: mkdir %s: %w", cacheDir, err)
	}

	idx, err := c.FetchIndex(ctx)
	if err != nil {
		return result, err
	}

	want := map[string]bool{}
	if len(opts.Only) > 0 {
		for _, id := range opts.Only {
			want[id] = true
		}
	}

	for _, entry := range idx.Themes {
		if len(want) > 0 && !want[entry.ID] {
			continue
		}
		raw, brand, err := c.FetchTheme(ctx, entry.URL)
		if err != nil {
			result.Errors[entry.ID] = err
			continue
		}
		dest := filepath.Join(cacheDir, entry.ID+".toml")
		if err := os.WriteFile(dest, raw, 0o644); err != nil {
			result.Errors[entry.ID] = fmt.Errorf("write cache: %w", err)
			continue
		}
		result.Cached = append(result.Cached, entry.ID)

		if t, err := Compile(brand); err == nil {
			reg.Add(t)
			result.Registered = append(result.Registered, entry.ID)
		} else {
			result.Errors[entry.ID] = fmt.Errorf("compile: %w", err)
		}
	}

	sort.Strings(result.Cached)
	sort.Strings(result.Registered)
	return result, nil
}

// LoadCacheDir reads every <id>.toml file in cacheDir and registers the
// parsed Brand on reg. Returns the list of registered IDs and any
// per-file errors. Intended for cold-start aish startup so users see
// previously-synced themes without a network round-trip.
func LoadCacheDir(cacheDir string, reg *Registry) (registered []string, errs map[string]error) {
	errs = make(map[string]error)
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		// Missing cache dir is normal — no sync has happened yet.
		if os.IsNotExist(err) {
			return nil, nil
		}
		errs[""] = err
		return nil, errs
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		path := filepath.Join(cacheDir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			errs[e.Name()] = err
			continue
		}
		brand, err := parseV1(raw)
		if err != nil {
			errs[e.Name()] = err
			continue
		}
		if t, err := Compile(brand); err == nil {
			reg.Add(t)
			registered = append(registered, t.Name())
		} else {
			errs[e.Name()] = err
		}
	}
	sort.Strings(registered)
	return registered, errs
}

// ---------- v1 schema → proto.Brand ----------

// v1Theme is the on-the-wire TOML shape published by theme-atoms.com
// (schema "https://theme-atoms.com/schemas/theme-v1.json"). It's
// intentionally a SUPERSET of what aish renders today — the renderer
// uses only the fields it knows about; the rest are preserved in the
// cache file for forward-compat with future aish versions.
type v1Theme struct {
	Schema string `toml:"schema"`
	Meta   v1Meta `toml:"meta"`
	Font   v1Font `toml:"font"`
	// Palette and Roles map straight onto proto.
	Palette map[string]string `toml:"palette"`
	Roles   map[string]string `toml:"roles"`
	// Prompt extends with v1-specific fields.
	Prompt v1Prompt `toml:"prompt"`
	// Fields v0.2-5 does NOT yet render — preserved for future epics.
	Syntax map[string]string      `toml:"syntax"`
	Layout map[string]interface{} `toml:"layout"`
}

type v1Meta struct {
	ID           string `toml:"id"`
	Version      string `toml:"version"`
	DisplayName  string `toml:"display_name"`
	Description  string `toml:"description"`
	ExtendsBrand string `toml:"extends_brand"`
}

type v1Font struct {
	Family    string   `toml:"family"`
	Fallback  []string `toml:"fallback"`
	Size      int      `toml:"size"`
	Weight    int      `toml:"weight"`
	Ligatures bool     `toml:"ligatures"`
	NerdFont  bool     `toml:"nerd_font"`
}

type v1Prompt struct {
	Character      string   `toml:"character"`
	CharacterColor string   `toml:"character_color"`
	Segments       []string `toml:"segments"`
	Separator      string   `toml:"separator"`
	Glyphs         string   `toml:"glyphs"`
}

// parseV1 decodes a v1 theme TOML into the proto.Brand shape aish
// currently understands. Unknown fields are silently ignored (BurntSushi
// TOML's default).
func parseV1(raw []byte) (proto.Brand, error) {
	var v v1Theme
	if _, err := toml.Decode(string(raw), &v); err != nil {
		return proto.Brand{}, fmt.Errorf("v1 decode: %w", err)
	}
	if v.Meta.ID == "" {
		return proto.Brand{}, fmt.Errorf("v1: missing [meta].id")
	}

	// Roles in v1 use the `{palette.X}` template form. Convert to the
	// `$palette.X` form proto/Compile already understands.
	roles := proto.Roles{}
	for k, v := range v.Roles {
		roles[k] = templateToDollar(v)
	}

	// The prompt's character_color is a role on its own — wire it as
	// the "prompt" role so the renderer's existing prompt-coloring path
	// picks it up.
	if v.Prompt.CharacterColor != "" {
		roles["prompt"] = templateToDollar(v.Prompt.CharacterColor)
	}

	glyphs := proto.Glyphs{
		FiletypeMap: v.Prompt.Glyphs,
		Static:      map[string]string{},
	}
	if v.Prompt.Character != "" {
		glyphs.Static["prompt_char"] = v.Prompt.Character
	}

	font := v.Font.Family
	if font == "" && len(v.Font.Fallback) > 0 {
		font = v.Font.Fallback[0]
	}

	brand := proto.Brand{
		Name:    v.Meta.ID,
		Type:    "shell",
		Extends: v.Meta.ExtendsBrand,
		Palette: proto.Palette(v.Palette),
		Roles:   roles,
		Glyphs:  glyphs,
		Prompt: proto.PromptConfig{
			Segments:   v.Prompt.Segments,
			Separators: v.Prompt.Separator,
			Font:       font,
		},
	}
	return brand, nil
}

// homeDir returns the user's home directory by scanning an os.Environ()-
// shaped slice. Prefers $HOME (POSIX) then $USERPROFILE (Windows). The
// shell package has a sibling helper over *env.Env; we duplicate the
// trivial logic here to avoid a cross-package import that would cycle
// through the renderer.
func homeDir(env []string) string {
	const homeKey, profileKey = "HOME=", "USERPROFILE="
	var profile string
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, homeKey):
			if v := kv[len(homeKey):]; v != "" {
				return v
			}
		case strings.HasPrefix(kv, profileKey):
			profile = kv[len(profileKey):]
		}
	}
	return profile
}

// templateToDollar rewrites theme-atoms v1's `{palette.X}` syntax into
// the `$palette.X` form proto.theme.resolvePaletteRef expects.
// Defensive: empty input round-trips; non-template strings pass through.
func templateToDollar(s string) string {
	// Common form: "{palette.blue}" → "$palette.blue"
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		inner := s[1 : len(s)-1]
		return "$" + inner
	}
	return s
}
