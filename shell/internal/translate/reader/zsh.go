package reader

import (
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/translate"
)

// ReadZsh parses a zsh source string. For MVP zsh ≈ bash: the
// keyword grammar (`if`/`then`/`fi`, `for`/`do`/`done`, `case`/
// `esac`, function definitions) is identical at the surface level
// we cover. We delegate to ReadBash and then walk the result to
// reset the dialect tag and flag the zsh-only constructs we can
// detect lexically as Unknown.
//
// Anything more (zsh-only options like `setopt EXTENDED_GLOB`,
// `=(...)` process substitution, glob qualifiers `(.)` etc.) is
// out of MVP scope — the bash lexical classifier already catches
// the dangerous ones (process substitution, `[[ … ]]`, etc.); the
// rest are passed through as ordinary commands and may produce
// runtime errors when `aish run` invokes them. That is consistent
// with the "surface, don't bury" rule — the user can see the
// untranslated command in `aish explain`.
func ReadZsh(src string) (*translate.Script, error) {
	s, err := ReadBash(src)
	if err != nil {
		return nil, err
	}
	s.Dialect = translate.DialectZsh
	// One zsh-specific pre-pass: lines containing `=(` are process-
	// substitution in zsh and aren't caught by the bash classifier
	// (the bash classifier looks for `<(` and `>(`, not `=(`).
	for i, st := range s.Statements {
		switch v := st.(type) {
		case translate.Command:
			if hasZshProcSub(v) {
				s.Statements[i] = translate.Unknown{
					BaseStmt: translate.BaseStmt{Line: translate.Line(v)},
					Reason:   "zsh process substitution =( ) unsupported",
					Source:   commandSource(v),
				}
			}
		}
	}
	return s, nil
}

func hasZshProcSub(c translate.Command) bool {
	for _, a := range c.Args {
		if strings.Contains(a, "=(") {
			return true
		}
	}
	return false
}

func commandSource(c translate.Command) string {
	parts := []string{c.Name}
	parts = append(parts, c.Args...)
	return strings.Join(parts, " ")
}
