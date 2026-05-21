package term

import (
	"context"
	"errors"
	"io"
)

// ErrInterrupt is returned by Editor.ReadLine when the user pressed
// Ctrl-C mid-edit. The caller (shell.Run) is expected to discard the
// line and loop to the next prompt.
var ErrInterrupt = errors.New("term: interrupt")

// Config is the bag of dependencies an Editor needs. All fields are
// required EXCEPT Resolver and Completer which default to no-ops.
type Config struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Prompt    func() string
	History   HistorySource
	Resolver  TierResolver
	Completer Completer
	// RawTerm enters / restores termios state. In tests, pass a
	// fakeTerm. In production, pass NewRawTerminal(os.Stdin).
	RawTerm RawTerminal
}

// Editor owns one input session at a time. It is NOT safe for
// concurrent ReadLine calls.
type Editor struct {
	cfg      Config
	buf      *Buffer
	render   *Renderer
	histNav  *HistoryNav
	readBuf  []byte // un-consumed bytes from the last Read
	rawBytes [128]byte
}

// NewEditor constructs an Editor.
func NewEditor(cfg Config) *Editor {
	if cfg.Resolver == nil {
		cfg.Resolver = noResolver{}
	}
	if cfg.Completer == nil {
		cfg.Completer = noCompleter{}
	}
	if cfg.History == nil {
		cfg.History = NewMemorySource(nil)
	}
	return &Editor{
		cfg:    cfg,
		buf:    NewBuffer(),
		render: NewRenderer(cfg.Stdout),
	}
}

// ReadLine drives one input session.
//
// Returns:
//
//   - the accepted line on Enter (no trailing newline),
//   - ErrInterrupt on Ctrl-C (caller loops),
//   - io.EOF on Ctrl-D-with-empty-buffer or stdin close.
//
// The terminal is put into raw mode at entry and restored on every
// exit path via a deferred Restore. ctx is honored for cancellation
// between key reads (not mid-read; the underlying Read is blocking).
func (e *Editor) ReadLine(ctx context.Context) (string, error) {
	if e.cfg.RawTerm != nil {
		if err := e.cfg.RawTerm.Enter(); err != nil {
			return "", err
		}
		defer e.cfg.RawTerm.Restore()
	}

	e.buf.Clear()
	e.histNav = NewHistoryNav(e.cfg.History)
	var completions []string
	var completionIdx int
	var completionToken string
	var completionStart int
	var search *Search
	var searchStash string
	for {
		e.draw(search)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		key, err := e.nextKey()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if e.buf.Len() == 0 {
					return "", io.EOF
				}
				// EOF with content acts like Enter for the
				// non-interactive case.
				return e.buf.String(), nil
			}
			return "", err
		}
		// Tab is special — we want to keep `completions` across
		// successive Tab presses, but clear it on any other key.
		isTab := key.Type == KeyTab
		if !isTab && completions != nil {
			completions = nil
			completionIdx = 0
		}
		// Search mode owns most keys.
		if search != nil {
			done, accepted, line := e.handleSearchKey(search, key, searchStash)
			if done {
				if accepted {
					return line, nil
				}
				search = nil
				e.buf.SetText(searchStash)
				e.buf.End()
			}
			continue
		}
		switch key.Type {
		case KeyEnter:
			line := e.buf.String()
			// One linefeed so the next prompt renders on a fresh row.
			_, _ = io.WriteString(e.cfg.Stdout, "\r\n")
			return line, nil
		case KeyCtrlC:
			_, _ = io.WriteString(e.cfg.Stdout, "^C\r\n")
			return "", ErrInterrupt
		case KeyCtrlD:
			if e.buf.Len() == 0 {
				_, _ = io.WriteString(e.cfg.Stdout, "\r\n")
				return "", io.EOF
			}
			// Non-empty buffer: Ctrl-D acts as Delete.
			e.buf.Delete()
		case KeyBackspace:
			e.buf.Backspace()
		case KeyDelete:
			e.buf.Delete()
		case KeyCtrlA, KeyHome:
			e.buf.Home()
		case KeyCtrlE, KeyEnd:
			e.buf.End()
		case KeyCtrlB, KeyLeft:
			e.buf.MoveLeft()
		case KeyCtrlF:
			// Ctrl-F doubles as "accept ghost" when there's a ghost
			// suffix; otherwise it moves right one character.
			if g := Suggest(e.cfg.History, e.buf.String()); g != "" {
				e.buf.SetText(e.buf.String() + g)
				e.buf.End()
			} else {
				e.buf.MoveRight()
			}
		case KeyRight:
			// Right at the end of the buffer accepts the ghost
			// suggestion; elsewhere it moves the cursor right.
			if e.buf.Cursor() == e.buf.Len() {
				if g := Suggest(e.cfg.History, e.buf.String()); g != "" {
					e.buf.SetText(e.buf.String() + g)
					e.buf.End()
				}
			} else {
				e.buf.MoveRight()
			}
		case KeyCtrlU:
			// Bash semantics: kill from start of line to cursor.
			e.buf.KillToStart()
		case KeyCtrlW:
			e.buf.KillWord()
		case KeyUp:
			if e.histNav.idx == e.histNav.src.Len() {
				e.histNav.Stash(e.buf.String())
			}
			if line, ok := e.histNav.Up(); ok {
				e.buf.SetText(line)
				e.buf.End()
			}
		case KeyDown:
			if line, ok := e.histNav.Down(); ok {
				e.buf.SetText(line)
				e.buf.End()
			}
		case KeyTab:
			tok, start, isFirst := e.buf.TokenAtCursor()
			// If the current token-under-cursor matches the candidate
			// we wrote on the previous Tab, this is a repeat Tab: cycle.
			// Otherwise this is a fresh Tab: re-query the completer.
			if completions != nil && start == completionStart && tok == completionToken {
				completionIdx = (completionIdx + 1) % len(completions)
			} else {
				cands, _ := e.cfg.Completer.Complete(CompletionContext{
					Token:      tok,
					FirstToken: isFirst,
					Line:       e.buf.String(),
				})
				if len(cands) == 0 {
					completions = nil
					break
				}
				completions = cands
				completionIdx = 0
				completionStart = start
			}
			chosen := completions[completionIdx]
			// Replace the old token (the one currently at `start`)
			// with `chosen`. The old token length is the rune-count
			// of whatever is between `start` and `start + len(tok)`.
			runes := e.buf.Runes()
			oldEnd := start + len([]rune(tok))
			newText := string(runes[:start]) + chosen + string(runes[oldEnd:])
			e.buf.SetText(newText)
			e.buf.SetCursor(start + len([]rune(chosen)))
			// Remember what we just wrote so the NEXT Tab can detect
			// "this is a cycle, not a fresh prefix."
			completionToken = chosen
		case KeyCtrlR:
			searchStash = e.buf.String()
			search = NewSearch(e.cfg.History)
		case KeyEsc:
			// Outside search mode, Esc is a no-op for now.
		case KeyRune:
			e.buf.InsertRune(key.Rune)
		case KeyUnknown:
			// Ignored deliberately.
		}
	}
}

// handleSearchKey processes one key while in Ctrl-R search mode.
// Returns (done, accepted, line). When done is true, the caller exits
// search mode; when accepted is also true, ReadLine returns `line`.
func (e *Editor) handleSearchKey(s *Search, key Key, stash string) (done, accepted bool, line string) {
	switch key.Type {
	case KeyEnter:
		match := s.Current()
		if match == "" {
			// No match — restore stash and treat as cancel.
			return true, false, ""
		}
		_, _ = io.WriteString(e.cfg.Stdout, "\r\n")
		return true, true, match
	case KeyCtrlC, KeyEsc:
		return true, false, ""
	case KeyCtrlR:
		s.Next()
	case KeyBackspace:
		s.Backspace()
	case KeyRune:
		s.AppendQuery(key.Rune)
	default:
		// Any other key (arrow, Home, etc.) exits search and replays
		// is not implemented — the simplest correct thing is to exit
		// search with the current match accepted into the buffer
		// (NOT executed), leaving the user to keep editing.
		match := s.Current()
		if match != "" {
			e.buf.SetText(match)
			e.buf.End()
		} else {
			e.buf.SetText(stash)
			e.buf.End()
		}
		return true, false, ""
	}
	return false, false, ""
}

// draw renders the current state.
func (e *Editor) draw(search *Search) {
	if search != nil {
		e.render.Render(Frame{
			Search: &SearchOverlay{
				Query: search.Query(),
				Match: search.Current(),
			},
		})
		return
	}
	line := e.buf.String()
	ghost := ""
	if e.buf.Cursor() == e.buf.Len() {
		ghost = Suggest(e.cfg.History, line)
	}
	e.render.Render(Frame{
		Prompt: e.cfg.Prompt(),
		Line:   line,
		Cursor: e.buf.Cursor(),
		Ghost:  ghost,
		Spans:  Highlight(line, e.cfg.Resolver),
	})
}

// nextKey reads the next decoded key. Buffers excess bytes so multi-
// byte escape sequences are reassembled across Read calls.
func (e *Editor) nextKey() (Key, error) {
	for {
		if len(e.readBuf) > 0 {
			k, n, ok := DecodeKey(e.readBuf)
			if ok {
				e.readBuf = e.readBuf[n:]
				return k, nil
			}
		}
		n, err := e.cfg.Stdin.Read(e.rawBytes[:])
		if n > 0 {
			e.readBuf = append(e.readBuf, e.rawBytes[:n]...)
		}
		if err != nil {
			if len(e.readBuf) == 0 {
				return Key{}, err
			}
			// Drain remaining bytes as best-effort.
			k, consumed, ok := DecodeKey(e.readBuf)
			if ok {
				e.readBuf = e.readBuf[consumed:]
				return k, nil
			}
			return Key{}, err
		}
	}
}

// ---- defaults for missing Config fields ----

type noResolver struct{}

func (noResolver) ResolveTier(string) Tier { return TierAIIntent }

type noCompleter struct{}

func (noCompleter) Complete(CompletionContext) ([]string, bool) { return nil, false }
