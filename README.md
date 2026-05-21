# aish

An AI-native, OS-insensitive, reversible shell.

`aish` is a POSIX-style command shell that treats AI inference as a first-class dispatch tier, not a bolt-on. Natural-language intents (anything that isn't a literal command on `$PATH`) flow through a local SQLite cache; misses are compiled into shell invocations by a pluggable LLM gateway and written back so the same intent never costs twice. Destructive operations (`rm`, `mv`, `dd`, …) are intercepted, snapshotted, and reversible via `undo`. The shell speaks the OpenAI chat-completions wire shape to a Cloudflare-fronted gateway at `api.convergent-systems.co` (see [`core-infra`](https://github.com/convergent-systems-co/core-infra)).

## Status

`aish` is mid-v0.2 — the **v0.1 thesis-validation** milestone shipped end-to-end (cache, plugin, reversibility, telemetry); **v0.2 layer-polish** is partially merged (PTY, theming) with the line-editor and the API protocol fix in open PRs.

| Milestone | Status | What's in |
|---|---|---|
| **v0.1 Thesis Validation** | ✅ shipped | Minimum shell, intent cache L1, cloud inference plugin (Anthropic-shape, since rewritten — see PR #182), basic reversibility, telemetry |
| **v0.2 Layer Polish** | 🟡 in flight | PTY support, theming polish; readline + API protocol fix open as PRs |
| **v0.3 Real Shell** | 📋 backlog | Script translation, secrets engine, history search, persona |
| **v1.0 Windows Native** | 📋 backlog | ConPTY, OS translation layer, native installers |
| **v1.5 aish-term** | 📋 scope TBD | First-party terminal emulator |

See `GOALS.md` for the full roadmap.

## Quickstart

### Build

```bash
make build   # produces shell/dist/aish + plugins/cloud/dist/aish-inference-cloud
```

The build is pure Go — no CGO, no native deps. SQLite is `modernc.org/sqlite`. PTY is `creack/pty` (Unix only today; Windows ConPTY tracked under v1.0).

### Run

```bash
./shell/dist/aish
```

Without a `cs_<token>` set, the shell runs in cache-only mode (no AI inference, no natural-language intents — POSIX commands work normally).

### Configure cloud inference

The cloud plugin talks to the Convergent Systems LLM gateway. Mint a token via the `core-infra` CLI, then export it:

```bash
# from the core-infra checkout
scripts/cs-token issue aish
# the script prints the token ONCE — capture it

export ANTHROPIC_API_KEY=cs_…   # legacy env var; renaming in PR #182
./shell/dist/aish
```

The gateway today serves Cloudflare Workers AI (Llama 3.1 8B by default) on OpenAI chat-completions shape. Embeddings are not yet routed (`core-infra` issue #10).

## Dispatch tier order

Every line of input flows through five tiers, in order:

```
1. built-in           cd, export, theme, cache, undo, restore, stats
2. known binary       first token resolves on $PATH → parser + exec
3. cache hit          (intent_hash, os) → cached invocation → exec
4. plugin infer       aish-inference-cloud → invocation → cache write-back → exec
5. legacy fallback    parser + exec (exit 127 for unknown command)
```

POSIX commands stay on the hot path. Natural-language intents go through cache → plugin and the invocation is reused for everyone on this machine forever after.

## Built-ins available today

| Command | What it does |
|---|---|
| `cd <path>` | Change working directory; `~` and bare `cd` resolve to `$HOME` |
| `export NAME=VALUE` | Set shell env; quote-aware (single + double) |
| `theme list` | List bundled + synced brands |
| `theme show <name>` | Inspect a brand's roles, glyphs, segments |
| `theme set <name>` | Activate a brand; persists to `~/.aish/config.toml` |
| `theme preview <name> [--plain]` | Render a brand without activating; `--plain` strips ANSI |
| `theme sync` | Pull brands from `theme-atoms.com` |
| `cache stats` | `Hits: N \| Misses: M \| Hit rate: P% \| Entries: K` |
| `cache clear` | Truncate the intent cache |
| `undo` | Restore the last destructive operation from snapshot |
| `restore <path>` | Restore a specific path from its most recent snapshot |
| `stats` | Per-session metrics: commands, cache hits, inference calls, cost |

## State on disk

aish keeps everything under `~/.aish/`:

```
~/.aish/
├── config.toml          # active theme + opt-in flags (merge-aware writer)
├── cache.db             # SQLite intent cache (L1)
├── history.db           # SQLite event log (v0.1-4)
├── snapshots/           # Pre-execution file snapshots for undo
├── themes/              # Brands synced from theme-atoms.com
├── sessions/            # Per-session telemetry rollups
├── telemetry.toml       # Telemetry consent flags
└── cost-log.jsonl       # Per-inference cost log
```

All paths are 0700/0644; no secrets are persisted anywhere.

## Architecture

```
shell/
├── cmd/aish/                  Entry point
├── internal/
│   ├── shell/                 REPL + dispatch + built-ins
│   ├── env/                   $VAR / $? expansion (quote-aware)
│   ├── parser/                Pipeline + redirect parsing
│   ├── exec/                  Child-process I/O + PTY (Unix today)
│   ├── theme/                 Brand schema + 10 bundled brands
│   ├── cache/                 SQLite intent cache + plugin client
│   ├── history/               Event log + snapshots + undo
│   ├── telemetry/             Session counters + cost aggregation
│   └── term/                  TTY line editor (in PR #181)
plugins/
└── cloud/
    ├── cmd/aish-inference-cloud/   JSON-RPC plugin binary
    └── internal/
        ├── csllm/             CS gateway client (in PR #182; today named anthropic/)
        ├── rpc/               JSON-RPC dispatcher over NDJSON
        └── reliab/            Retries + cost log
libs/
└── proto/
    ├── inference/             Frozen JSON-RPC contract (Frame, InferParams, …)
    └── theme/                 Brand schema (Validate() per v0.2-5)
```

Three Go modules pinned by `go.work`. The plugin protocol (`libs/proto`) is the only cross-module contract; shell and plugin compile independently.

## Open PRs

| PR | Branch | Status |
|---|---|---|
| [#181](https://github.com/convergent-systems-co/aish/pull/181) | `feat/v0.2-1-ui` | Interactive shell UX (readline, history nav, ghost-text, tab completion, Ctrl-R) — needs rebase on main |
| [#182](https://github.com/convergent-systems-co/aish/pull/182) | `feat/v0.2-api-fix` | Switch cloud plugin to OpenAI chat-completions on CS gateway; rename `anthropic/` → `csllm/`; disable embeddings until `core-infra` issue #10 lands |

## Contributing

- **Conventional Commits** (`feat:`, `fix:`, `refactor:`, `chore:`, `docs:`, `test:`, `build:`, `ci:`).
- **One logical change per commit** (per `~/.ai/Code.md §11.2`).
- **Plans live in `.artifacts/plans/<milestone>.md`**; ADRs would land under `docs/decisions/` (none yet).
- **Issues**: drive everything through `gh issue` against `convergent-systems-co/aish`. The `Pipeline` single-select field on the project board (`aish Delivery`) is the distributed lock — see `.artifacts/spawn/board.py`.

## Pointers

- North-star roadmap: [`GOALS.md`](./GOALS.md)
- Stub architecture notes: [`ARCHITECTURE.md`](./ARCHITECTURE.md) (sparse)
- Cloudflare gateway: [`convergent-systems-co/core-infra`](https://github.com/convergent-systems-co/core-infra)
- Brand catalog (in development): [`theme-atoms.com`](https://theme-atoms.com)
