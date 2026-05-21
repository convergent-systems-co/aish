// Package term implements the v0.2-1 interactive line editor: raw-mode
// TTY input, ANSI key decoding, a rune-indexed line buffer, history
// navigation, ghost-text suggestion, smart tab completion, Ctrl-R
// reverse history search, and per-keystroke syntax highlighting.
//
// Scope and partition (v0.2-1):
//
//   - TTY-only. When stdin is not a TTY (script piped through aish),
//     the caller (shell.Run) keeps the existing byte-by-byte readLine
//     path and never enters this package. The issue-#167 regression
//     seatbelt continues to hold.
//
//   - Single-line editing. Wrapping is the terminal's job. Multi-line
//     entry is out of scope.
//
//   - Emacs-style key bindings, fixed. Ctrl-A/E/U/W/F/B + arrows +
//     Home/End/Backspace/Delete + Tab/Enter/Ctrl-C/Ctrl-D/Ctrl-R. No
//     vi-mode toggle in v0.2-1.
//
//   - Windows is the byte-by-byte fallback for v0.2-1 (the raw-mode
//     surface uses golang.org/x/term which IS cross-platform; the
//     ANSI decoder is what we keep behind a build-tag follow-up).
//
// Contract with shell.Run:
//
//	editor := term.NewEditor(term.Config{
//	    Stdin:   os.Stdin,
//	    Stdout:  os.Stdout,
//	    Prompt:  s.Prompt,           // re-rendered each frame
//	    History: histSource,         // session lines + (later) disk
//	    Resolve: shell.ResolveTier,  // first-token tier for highlight
//	    Theme:   themes.Active(),    // snapshot at session open
//	    Completer: term.NewDefaultCompleter(env),
//	})
//	for {
//	    line, err := editor.ReadLine(ctx)
//	    if err == io.EOF { return nil }
//	    if errors.Is(err, term.ErrInterrupt) { continue } // Ctrl-C
//	    ...
//	}
//
// All exported types are safe for concurrent reads of immutable state
// and serial mutation under the Editor.ReadLine call. Editor is NOT
// safe to call from multiple goroutines.
package term
