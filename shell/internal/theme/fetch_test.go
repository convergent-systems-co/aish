package theme

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Canned theme-atoms.com responses. These are byte-for-byte close to
// the live wire format so we exercise the same parser path users hit.

const stubIndexJSON = `{
  "schema_version": "1",
  "generated_at": "test",
  "themes": [
    {"id":"default",        "version":"1.0.0", "display_name":"Default",            "url":"%s/themes/default.toml"},
    {"id":"nord-powerline", "version":"1.2.0", "display_name":"Nord — Powerline",   "url":"%s/themes/nord-powerline.toml"},
    {"id":"dracula",        "version":"1.0.0", "display_name":"Dracula",            "url":"%s/themes/dracula.toml"}
  ]
}`

const stubNordPowerlineTOML = `schema = "https://theme-atoms.com/schemas/theme-v1.json"

[meta]
id = "nord-powerline"
version = "1.2.0"
display_name = "Nord — Powerline"
description = "Nord palette with powerline separators."
extends_brand = "nord"

[font]
family = "JetBrainsMono Nerd Font"
fallback = ["JetBrains Mono", "monospace"]
size = 13
weight = 400
ligatures = true
nerd_font = true

[palette]
nord8 = "#88c0d0"
nord11 = "#bf616a"
nord14 = "#a3be8c"
blue = "#5e81ac"
red = "#bf616a"
green = "#a3be8c"
muted = "#4c566a"

[prompt]
character = "❯"
character_color = "{palette.blue}"
segments = ["cwd", "git-status", "exit-code"]
separator = "powerline"
glyphs = "nerd-default"

[roles]
ai_tier_local = "{palette.green}"
ai_tier_cloud = "{palette.blue}"
exit_err = "{palette.red}"
exit_ok = "{palette.muted}"
`

const stubDefaultTOML = `schema = "https://theme-atoms.com/schemas/theme-v1.json"

[meta]
id = "default"
version = "1.0.0"
display_name = "Default"

[palette]
primary = "#7fb3d5"
muted   = "#6c757d"

[prompt]
character = ">"
character_color = "{palette.primary}"
segments = ["cwd"]
separator = "minimal"

[roles]
prompt = "{palette.primary}"
`

const stubDraculaTOML_Broken = `schema = "https://theme-atoms.com/schemas/theme-v1.json"
this is not valid TOML at all <<<
`

// newStubServer stands up an httptest.Server matching the real
// theme-atoms.com paths. The themes map drives per-id responses; a
// nil entry returns 404. The index is generated from the map keys.
func newStubServer(t *testing.T, themes map[string]string, missing []string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == indexPath:
			w.Header().Set("Content-Type", "application/json")
			body := stubIndexJSON
			for i := 0; i < 3; i++ {
				body = strings.Replace(body, "%s", srv.URL, 1)
			}
			_, _ = w.Write([]byte(body))
		case strings.HasPrefix(r.URL.Path, "/themes/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/themes/"), ".toml")
			for _, m := range missing {
				if m == id {
					w.WriteHeader(http.StatusNotFound)
					return
				}
			}
			body, ok := themes[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/toml")
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---------- v1 parser ----------

func TestParseV1_NordPowerline_RoundTripsCoreFields(t *testing.T) {
	brand, err := parseV1([]byte(stubNordPowerlineTOML))
	if err != nil {
		t.Fatalf("parseV1: %v", err)
	}
	if brand.Name != "nord-powerline" {
		t.Errorf("Name = %q, want %q", brand.Name, "nord-powerline")
	}
	if brand.Extends != "nord" {
		t.Errorf("Extends = %q, want %q", brand.Extends, "nord")
	}
	if got := brand.Palette["blue"]; got != "#5e81ac" {
		t.Errorf("Palette[blue] = %q, want #5e81ac", got)
	}
	if got := brand.Glyphs.Static["prompt_char"]; got != "❯" {
		t.Errorf("Glyphs.prompt_char = %q, want ❯", got)
	}
	if got := brand.Prompt.Separators; got != "powerline" {
		t.Errorf("Prompt.Separators = %q, want powerline", got)
	}
	if got := brand.Prompt.Font; got != "JetBrainsMono Nerd Font" {
		t.Errorf("Prompt.Font = %q, want JetBrainsMono Nerd Font", got)
	}
	// Roles should have the {palette.X} → $palette.X conversion.
	if got := brand.Roles["ai_tier_local"]; got != "$palette.green" {
		t.Errorf("Roles[ai_tier_local] = %q, want $palette.green", got)
	}
	// The prompt's character_color becomes the `prompt` role.
	if got := brand.Roles["prompt"]; got != "$palette.blue" {
		t.Errorf("Roles[prompt] (from character_color) = %q, want $palette.blue", got)
	}
}

func TestParseV1_MissingMetaID_ReturnsError(t *testing.T) {
	noID := `[meta]
version = "1.0.0"
[palette]
fg = "#ffffff"
`
	if _, err := parseV1([]byte(noID)); err == nil {
		t.Error("expected error for missing [meta].id, got nil")
	}
}

func TestParseV1_MalformedTOML_ReturnsError(t *testing.T) {
	if _, err := parseV1([]byte(stubDraculaTOML_Broken)); err == nil {
		t.Error("expected error for malformed TOML, got nil")
	}
}

func TestTemplateToDollar(t *testing.T) {
	cases := []struct{ in, want string }{
		{"{palette.blue}", "$palette.blue"},
		{"{palette.nord8}", "$palette.nord8"},
		{"$palette.blue", "$palette.blue"}, // pass-through (already converted)
		{"plain-string", "plain-string"},
		{"", ""},
		{"{not-closed", "{not-closed"},
		{"not-opened}", "not-opened}"},
	}
	for _, tc := range cases {
		if got := templateToDollar(tc.in); got != tc.want {
			t.Errorf("templateToDollar(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------- FetchIndex ----------

func TestFetchIndex_ReturnsCatalog(t *testing.T) {
	srv := newStubServer(t,
		map[string]string{"default": stubDefaultTOML, "nord-powerline": stubNordPowerlineTOML},
		nil)
	c := NewClient(srv.URL, srv.Client())
	idx, err := c.FetchIndex(context.Background())
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if idx.SchemaVersion != "1" {
		t.Errorf("SchemaVersion = %q, want 1", idx.SchemaVersion)
	}
	if got := len(idx.Themes); got != 3 {
		t.Errorf("len(Themes) = %d, want 3", got)
	}
	// Order from the stub: default, nord-powerline, dracula.
	if idx.Themes[0].ID != "default" {
		t.Errorf("Themes[0].ID = %q, want default", idx.Themes[0].ID)
	}
}

func TestFetchIndex_ServerError_Surfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, srv.Client())
	_, err := c.FetchIndex(context.Background())
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error doesn't mention status code: %v", err)
	}
}

// ---------- FetchTheme ----------

func TestFetchTheme_ReturnsRawAndBrand(t *testing.T) {
	srv := newStubServer(t,
		map[string]string{"nord-powerline": stubNordPowerlineTOML}, nil)
	c := NewClient(srv.URL, srv.Client())
	raw, brand, err := c.FetchTheme(context.Background(), "nord-powerline")
	if err != nil {
		t.Fatalf("FetchTheme: %v", err)
	}
	if len(raw) == 0 {
		t.Error("raw bytes empty")
	}
	if brand.Name != "nord-powerline" {
		t.Errorf("Brand.Name = %q, want nord-powerline", brand.Name)
	}
	// Raw should match the source byte-for-byte (cache persists verbatim).
	if string(raw) != stubNordPowerlineTOML {
		t.Errorf("raw bytes don't match source\nwant: %q\ngot:  %q",
			stubNordPowerlineTOML[:80], string(raw)[:80])
	}
}

func TestFetchTheme_404_ReturnsError(t *testing.T) {
	srv := newStubServer(t, map[string]string{}, []string{"missing-theme"})
	c := NewClient(srv.URL, srv.Client())
	_, _, err := c.FetchTheme(context.Background(), "missing-theme")
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

// ---------- Sync ----------

func TestSync_WritesCacheAndRegisters(t *testing.T) {
	tmp := t.TempDir()
	srv := newStubServer(t,
		map[string]string{
			"default":        stubDefaultTOML,
			"nord-powerline": stubNordPowerlineTOML,
		},
		[]string{"dracula"}) // dracula listed in index but the file 404s

	c := NewClient(srv.URL, srv.Client())
	reg := NewRegistry()
	res, err := c.Sync(context.Background(),
		[]string{"HOME=" + tmp},
		reg,
		SyncOptions{},
	)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// default + nord-powerline cached + registered; dracula errored.
	if got := len(res.Cached); got != 2 {
		t.Errorf("Cached count = %d, want 2 (got %v)", got, res.Cached)
	}
	if got := len(res.Registered); got != 2 {
		t.Errorf("Registered count = %d, want 2 (got %v)", got, res.Registered)
	}
	if _, ok := res.Errors["dracula"]; !ok {
		t.Errorf("expected dracula in Errors; got %v", res.Errors)
	}

	// Files exist at the documented path.
	for _, id := range res.Cached {
		path := filepath.Join(tmp, ".aish", CacheDirName, id+".toml")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("cache file missing at %s: %v", path, err)
		}
	}

	// Registry has the fetched themes alongside the bundled ones.
	if _, ok := reg.Lookup("nord-powerline"); !ok {
		t.Error("Registry missing nord-powerline after sync")
	}
}

func TestSync_OnlyFilter(t *testing.T) {
	tmp := t.TempDir()
	srv := newStubServer(t,
		map[string]string{
			"default":        stubDefaultTOML,
			"nord-powerline": stubNordPowerlineTOML,
		},
		nil)
	c := NewClient(srv.URL, srv.Client())
	reg := NewRegistry()
	res, err := c.Sync(context.Background(),
		[]string{"HOME=" + tmp},
		reg,
		SyncOptions{Only: []string{"default"}},
	)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Cached) != 1 || res.Cached[0] != "default" {
		t.Errorf("expected exactly [default] cached, got %v", res.Cached)
	}
}

func TestSync_NoHomeReturnsError(t *testing.T) {
	srv := newStubServer(t, map[string]string{}, nil)
	c := NewClient(srv.URL, srv.Client())
	reg := NewRegistry()
	_, err := c.Sync(context.Background(), nil, reg, SyncOptions{})
	if err == nil || !strings.Contains(err.Error(), "HOME") {
		t.Errorf("expected HOME-unset error, got %v", err)
	}
}

// ---------- LoadCacheDir ----------

func TestLoadCacheDir_ReadsExistingTOMLs(t *testing.T) {
	tmp := t.TempDir()
	// Pre-seed two themes in the cache directly.
	if err := os.WriteFile(filepath.Join(tmp, "default.toml"), []byte(stubDefaultTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "nord-powerline.toml"), []byte(stubNordPowerlineTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	registered, errs := LoadCacheDir(tmp, reg)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if len(registered) != 2 {
		t.Errorf("registered count = %d, want 2 (got %v)", len(registered), registered)
	}
}

func TestLoadCacheDir_MissingDirIsNoOp(t *testing.T) {
	reg := NewRegistry()
	registered, errs := LoadCacheDir(filepath.Join(t.TempDir(), "doesnotexist"), reg)
	if len(registered) != 0 || len(errs) != 0 {
		t.Errorf("expected silent no-op; got registered=%v errs=%v", registered, errs)
	}
}

// ---------- homeDir helper ----------

func TestHomeDir_PrefersHOME(t *testing.T) {
	if got := homeDir([]string{"HOME=/home/u", "USERPROFILE=C:\\Users\\u"}); got != "/home/u" {
		t.Errorf("homeDir prefers HOME; got %q", got)
	}
}

func TestHomeDir_FallsBackToUSERPROFILE(t *testing.T) {
	if got := homeDir([]string{"USERPROFILE=C:\\Users\\u"}); got != "C:\\Users\\u" {
		t.Errorf("homeDir falls back to USERPROFILE; got %q", got)
	}
}

func TestHomeDir_EmptyEnvReturnsEmpty(t *testing.T) {
	if got := homeDir(nil); got != "" {
		t.Errorf("homeDir on empty env returned %q, want empty", got)
	}
}

// ---------- error type sanity (defensive) ----------

func TestFetchIndex_BadURL_ReturnsError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", nil) // unused port
	_, err := c.FetchIndex(context.Background())
	if err == nil {
		t.Fatal("expected error on unreachable server, got nil")
	}
	// Be lenient: connect-refused or context-deadline both count.
	if !(strings.Contains(err.Error(), "connect") ||
		strings.Contains(err.Error(), "refused") ||
		strings.Contains(err.Error(), "deadline") ||
		errors.Is(err, context.DeadlineExceeded)) {
		t.Logf("note: error message: %v", err)
	}
}
