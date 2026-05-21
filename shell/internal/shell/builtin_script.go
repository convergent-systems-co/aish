package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"

	"github.com/convergent-systems-co/aish/shell/internal/cache"
	"github.com/convergent-systems-co/aish/shell/internal/env"
	"github.com/convergent-systems-co/aish/shell/internal/parser"
	"github.com/convergent-systems-co/aish/shell/internal/translate"
	"github.com/convergent-systems-co/aish/shell/internal/translate/reader"
)

// runScriptBuiltin implements `run <path>` — load script, detect
// dialect, parse, execute each statement through the shell's
// dispatch tier so caching and history interceptors still apply.
// Each invocation is a clean session: a fresh env copy is built
// from the parent shell's env so in-script assignments cannot leak
// back into the REPL.
func (s *Shell) runScriptBuiltin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "aish: run: usage: run <script>")
		return 2
	}
	path := args[0]
	src, err := translate.LoadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "aish: run: %v\n", err)
		return 1
	}
	dialect := translate.Detect(path, string(src))
	script, err := translate.Read(dialect, string(src), defaultReaders())
	if err != nil {
		fmt.Fprintf(stderr, "aish: run: parse %s: %v\n", dialect, err)
		return 1
	}
	// Build a fresh env copy so the script's assignments don't leak
	// back into the parent shell. We start from a snapshot of the
	// current env, mutate that snapshot inside RunOptions.EnvSet,
	// and discard it when this invocation returns.
	localEnv := env.FromSlice(s.env.Environ())
	runner := &shellRunner{
		shell:    s,
		localEnv: localEnv,
	}
	code, err := translate.Run(context.Background(), runner, script, translate.RunOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		EnvSet: func(name, value string) {
			_ = localEnv.Set(name, value)
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "aish: run: %v\n", err)
		return 1
	}
	return code
}

// explainScriptBuiltin implements `explain <path>`. The deterministic
// numbered-step description is written to stdout. With `--with-llm`
// AND an ANTHROPIC_API_KEY present AND the inference plugin started,
// an additional Summary section is appended.
func (s *Shell) explainScriptBuiltin(args []string, stdout, stderr io.Writer) int {
	withLLM := false
	rest := []string{}
	for _, a := range args {
		if a == "--with-llm" {
			withLLM = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "aish: explain: usage: explain [--with-llm] <script>")
		return 2
	}
	path := rest[0]
	src, err := translate.LoadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "aish: explain: %v\n", err)
		return 1
	}
	dialect := translate.Detect(path, string(src))
	script, err := translate.Read(dialect, string(src), defaultReaders())
	if err != nil {
		fmt.Fprintf(stderr, "aish: explain: parse %s: %v\n", dialect, err)
		return 1
	}
	opts := translate.ExplainOptions{
		WithLLM: withLLM,
		Source:  string(src),
	}
	if withLLM {
		if key, _ := s.env.Get("ANTHROPIC_API_KEY"); key != "" && s.cachePlugin != nil {
			opts.Enricher = pluginExplainEnricher{plugin: s.cachePlugin}
		} else {
			fmt.Fprintln(stderr, "aish: explain: --with-llm requested but no API key / plugin available; emitting baseline only")
		}
	}
	if err := translate.Explain(context.Background(), stdout, script, opts); err != nil {
		fmt.Fprintf(stderr, "aish: explain: %v\n", err)
		return 1
	}
	return 0
}

// migrateScriptBuiltin implements `migrate <path>`. The aish-native
// translation is written to stdout. Unknown nodes appear as
// `# aish: MIGRATE-TODO:` comments so the user sees what didn't
// translate.
func (s *Shell) migrateScriptBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "aish: migrate: usage: migrate <script>")
		return 2
	}
	path := args[0]
	src, err := translate.LoadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "aish: migrate: %v\n", err)
		return 1
	}
	dialect := translate.Detect(path, string(src))
	script, err := translate.Read(dialect, string(src), defaultReaders())
	if err != nil {
		fmt.Fprintf(stderr, "aish: migrate: parse %s: %v\n", dialect, err)
		return 1
	}
	if err := translate.Migrate(stdout, script); err != nil {
		fmt.Fprintf(stderr, "aish: migrate: %v\n", err)
		return 1
	}
	return 0
}

// defaultReaders is the set wired into translate.Read for the
// built-ins. Kept as a function so tests can swap it cheaply.
//
// PowerShell reader (v1.0-3) is wired alongside the pre-existing
// bash / zsh / fish trio; translate.Read dispatches per Dialect,
// so adding readers here is additive. Cmd is wired in a follow-up
// commit.
func defaultReaders() translate.Readers {
	return translate.Readers{
		Bash:       reader.ReadBash,
		Zsh:        reader.ReadZsh,
		Fish:       reader.ReadFish,
		PowerShell: reader.ReadPowerShell,
	}
}

// shellRunner adapts the Shell's pipeline executor to the
// translate.Runner contract. Each call parses one command line and
// runs it through runPipeline (the same path the REPL uses), with
// the streams supplied to this invocation.
//
// localEnv is the in-script environment snapshot. We pass it as the
// child-process environment so variable assignments inside the
// script are visible to subsequent commands within the same `aish
// run` invocation.
type shellRunner struct {
	shell    *Shell
	localEnv *env.Env
}

func (r *shellRunner) Run(ctx context.Context, cmdline string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if cmdline == "" {
		return 0, nil
	}
	// Expand $VAR / ${VAR} / $? using the LOCAL env (the script's
	// own assignments) — that's how a real shell would interpret
	// in-script `echo $x` after `x=foo`.
	expanded := r.localEnv.Expand(cmdline, r.shell.lastExit)
	// Cache-dispatch: try the AI-native path when configured. The
	// known-binary tier short-circuits to the legacy path below.
	if first := firstToken(expanded); first != "" && isKnownBinary(first, r.localEnv) {
		return r.execLine(expanded, stdin, stdout, stderr)
	}
	if r.shell.cache != nil {
		invocation, _, err := r.shell.cache.Resolve(ctx, cmdline, runtime.GOOS)
		switch {
		case err == nil:
			return r.execLine(invocation, stdin, stdout, stderr)
		case errors.Is(err, cache.ErrNoPlugin):
			// fall through
		default:
			fmt.Fprintf(stderr, "aish: %v\n", err)
			return 127, nil
		}
	}
	return r.execLine(expanded, stdin, stdout, stderr)
}

// execLine parses a single command line and runs it through the
// shell's pipeline executor with the script-local env.
func (r *shellRunner) execLine(cmdline string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	pipeline, parseErr := parser.Parse(cmdline)
	if parseErr != nil {
		fmt.Fprintf(stderr, "aish: parse: %v\n", parseErr)
		return 2, nil
	}
	if len(pipeline.Commands) == 0 {
		return 0, nil
	}
	// Swap the shell's env for the script-local env for the duration
	// of this single pipeline call: runPipeline reads s.env.Environ
	// at the call site, so the children see the in-script env. We
	// restore the parent env immediately after.
	parentEnv := r.shell.env
	r.shell.env = r.localEnv
	defer func() { r.shell.env = parentEnv }()
	return r.shell.runPipeline(pipeline, stdin, stdout, stderr)
}

// pluginExplainEnricher is the optional LLM-enrichment adapter.
// Implements translate.LLMEnricher by routing the request through
// the cache plugin's Infer call.
type pluginExplainEnricher struct {
	plugin *cache.PluginClient
}

func (e pluginExplainEnricher) EnrichExplain(ctx context.Context, source string, baseline string) (string, error) {
	prompt := "Explain this shell script in 2-3 sentences. The baseline numbered description is below; produce a higher-level summary, not a re-statement.\n\nScript:\n" + source + "\n\nBaseline:\n" + baseline
	out, _, err := e.plugin.Infer(ctx, prompt, runtime.GOOS)
	return out, err
}
