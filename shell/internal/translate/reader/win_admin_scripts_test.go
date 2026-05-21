package reader

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

// TestWinAdminScriptsRoundTrip is the v1.0-3 #145 smoke gate. It
// walks every fixture under win_testdata/ through:
//
//   1. translate.Detect            — by path extension.
//   2. translate.Read              — via the appropriate reader.
//   3. translate.Explain           — deterministic baseline only.
//   4. translate.Migrate           — rule-based aish-native emit.
//
// Each step must complete without error. Output must be non-empty
// where the script itself is non-empty. We deliberately don't
// assert exact strings — the engines' deterministic output is
// covered by the per-engine tests; this gate only proves the
// readers cooperate with the wider translate pipeline.
func TestWinAdminScriptsRoundTrip(t *testing.T) {
	entries, err := os.ReadDir("win_testdata")
	if err != nil {
		t.Fatalf("list win_testdata: %v", err)
	}
	readers := translate.Readers{
		Bash:       ReadBash,
		Zsh:        ReadZsh,
		Fish:       ReadFish,
		PowerShell: ReadPowerShell,
		Cmd:        ReadCmd,
	}
	var sawPS, sawCmd bool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join("win_testdata", e.Name())
		ext := strings.ToLower(filepath.Ext(e.Name()))
		switch ext {
		case ".ps1", ".psm1":
			sawPS = true
		case ".bat", ".cmd":
			sawCmd = true
		default:
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			src := string(b)
			dialect := translate.Detect(path, src)
			switch ext {
			case ".ps1", ".psm1":
				if dialect != translate.DialectPowerShell {
					t.Errorf("Detect(%s) = %q, want powershell", e.Name(), dialect)
				}
			case ".bat", ".cmd":
				if dialect != translate.DialectCmd {
					t.Errorf("Detect(%s) = %q, want cmd", e.Name(), dialect)
				}
			}
			script, err := translate.Read(dialect, src, readers)
			if err != nil {
				t.Fatalf("Read(%s): %v", dialect, err)
			}
			if script == nil {
				t.Fatalf("Read returned nil script")
			}
			if len(script.Statements) == 0 {
				t.Errorf("script parsed to zero statements")
			}
			// Explain: deterministic baseline only.
			var explainBuf bytes.Buffer
			if err := translate.Explain(context.Background(), &explainBuf, script, translate.ExplainOptions{}); err != nil {
				t.Errorf("Explain: %v", err)
			}
			if explainBuf.Len() == 0 {
				t.Errorf("Explain produced no output")
			}
			// Migrate.
			var migrateBuf bytes.Buffer
			if err := translate.Migrate(&migrateBuf, script); err != nil {
				t.Errorf("Migrate: %v", err)
			}
			if migrateBuf.Len() == 0 {
				t.Errorf("Migrate produced no output")
			}
		})
	}
	if !sawPS {
		t.Errorf("no .ps1/.psm1 fixture exercised — coverage gap")
	}
	if !sawCmd {
		t.Errorf("no .bat/.cmd fixture exercised — coverage gap")
	}
}
