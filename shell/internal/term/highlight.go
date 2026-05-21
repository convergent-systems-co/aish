package term

import "unicode"

// Tier discriminates the resolution path for the first token. The
// editor passes a TierResolver and the highlighter colors the first
// token accordingly.
type Tier int

const (
	// TierAIIntent means "we don't recognize this first token as a
	// binary or built-in; the cache/inference path will own it."
	TierAIIntent Tier = iota
	// TierKnownBinary means "this first token resolves on $PATH —
	// the known-binary passthrough dispatch tier will run it."
	TierKnownBinary
	// TierBuiltin means "this is an aish built-in (cd, theme, …)."
	TierBuiltin
)

// TierResolver tells the highlighter how to color the first token.
// The production resolver is implemented in the shell package; the
// term package only depends on the interface.
type TierResolver interface {
	ResolveTier(firstToken string) Tier
}

// Role names the theme role a span should be rendered with. The
// renderer maps Role → concrete ANSI bytes via the active theme.
type Role string

const (
	RoleDefault      Role = "default"
	RoleAccent       Role = "accent"
	RoleString       Role = "string"
	RoleAITierLocal  Role = "ai_tier_local"
	RoleAITierCloud  Role = "ai_tier_cloud"
	RoleGhost        Role = "ghost_suggestion"
	RoleSearchPrompt Role = "search_prompt"
)

// Span is one contiguous run of characters in the highlighted line.
// Spans are returned in order; concatenating Span.Text reproduces the
// original input.
type Span struct {
	Text string
	Role Role
}

// Highlight tokenizes `line` and returns one Span per syntactic run.
// The implementation is intentionally simple — it's a single-pass
// classifier, not a full shell parser — because the budget is "< 1ms
// per redraw on a 120-char buffer."
//
// Rules:
//
//   - The first whitespace-separated token gets its color from the
//     TierResolver (built-in → RoleAccent, known-binary →
//     RoleAITierLocal, AI intent → RoleAITierCloud).
//   - Anything inside matching double or single quotes is RoleString.
//   - Whitespace and the remainder render as RoleDefault.
func Highlight(line string, r TierResolver) []Span {
	if line == "" {
		return nil
	}
	var spans []Span
	runes := []rune(line)

	// 1) Find first non-whitespace token.
	i := 0
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
	}
	if i > 0 {
		spans = append(spans, Span{Text: string(runes[:i]), Role: RoleDefault})
	}
	if i == len(runes) {
		return spans
	}
	firstStart := i
	for i < len(runes) && !unicode.IsSpace(runes[i]) {
		i++
	}
	firstTok := string(runes[firstStart:i])
	role := RoleAITierCloud
	if r != nil {
		switch r.ResolveTier(firstTok) {
		case TierBuiltin:
			role = RoleAccent
		case TierKnownBinary:
			role = RoleAITierLocal
		case TierAIIntent:
			role = RoleAITierCloud
		}
	}
	spans = append(spans, Span{Text: firstTok, Role: role})

	// 2) Walk the rest, peeling off quoted strings and whitespace.
	for i < len(runes) {
		c := runes[i]
		if unicode.IsSpace(c) {
			start := i
			for i < len(runes) && unicode.IsSpace(runes[i]) {
				i++
			}
			spans = append(spans, Span{Text: string(runes[start:i]), Role: RoleDefault})
			continue
		}
		if c == '"' || c == '\'' {
			quote := c
			start := i
			i++
			for i < len(runes) && runes[i] != quote {
				i++
			}
			if i < len(runes) {
				i++ // include closing quote
			}
			spans = append(spans, Span{Text: string(runes[start:i]), Role: RoleString})
			continue
		}
		// Default run: until whitespace or a quote.
		start := i
		for i < len(runes) && !unicode.IsSpace(runes[i]) && runes[i] != '"' && runes[i] != '\'' {
			i++
		}
		spans = append(spans, Span{Text: string(runes[start:i]), Role: RoleDefault})
	}
	return spans
}
