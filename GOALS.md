# aish — Goals & Architecture

> The single source of truth for what aish is, why it exists, and how it is built.
> Ecosystem and federation concerns (XDAO, the *-Atoms catalog standard) live in
> separate repos — see the Ecosystem section at the end.

---


> AI native. Shell native. OS insensitive. Fully reversible.

aish is a standalone shell written in Go, compiled natively per platform. It is not a wrapper around bash or PowerShell. It is not a tool you run inside your terminal. It is a shell — your login shell — where AI is a first-class primitive, the OS underneath is an implementation detail, and nothing you do is permanent.

Olympus connects to aish as a plugin. aish works without Olympus.

-----

-----

## The Civilization-Grade Thesis

aish is part of the Convergent Systems family — alongside Brand Atoms and Olympus. The three projects share a single operating principle: take foundational software layers that have lived as opaque-and-ephemeral for decades and rebuild them as structured-and-durable infrastructure.

|Project          |Type   |What it makes civilization-grade                                                                                                                 |
|-----------------|-------|-------------------------------------------------------------------------------------------------------------------------------------------------|
|**Brand Atoms**  |Catalog|Brand guidelines — from PDFs into typed YAML, machine-consumable, composable atoms                                                               |
|**Service Atoms**|Catalog|Services — from DNS and ad-hoc APIs into typed endpoints, schemas, policies, composable services                                                 |
|**Olympus**      |Runtime|AI development — from prompt-and-hope into governance, signed emissions, structured memory, auditable trails                                     |
|**aish**         |Runtime|The shell — from text streams and irreversible operations into typed pipes, structured history, OS-insensitive translation, reversible everything|

The pattern is identical each time: identify a foundational layer that everyone depends on but treats as throwaway, then rebuild it as infrastructure others can stand on for decades.

**The `*-Atoms` naming convention** signals civilization-grade structured catalogs across the ecosystem. Atoms are reusable building blocks; compositions assemble them into higher-level artifacts (brands, services); typed rules constrain how they may be combined. The pattern is endlessly extensible — future catalogs may include Identity Atoms, Data Atoms, Workflow Atoms. Each one a typed encyclopedia in its domain.

**Catalogs vs Runtimes:** Catalogs (`*-Atoms`) define *what exists*. Runtimes (aish, Olympus) define *what operates*. Clean separation. Runtimes consume catalogs; catalogs do not depend on runtimes.

**Civilization-grade properties — every architectural decision must satisfy these:**

- **Typed, not opaque** — every input, output, and stored artifact has a schema
- **Versioned, not ephemeral** — everything has lineage, history, and rollback
- **Machine-readable, not just human-readable** — humans and AI consume the same canonical source
- **Composable, not monolithic** — atoms compose into systems; everything is replaceable
- **Open, not proprietary** — durable infrastructure requires public catalogs and standards
- **Built to outlast its category** — the design has to still make sense in 30 years

This is also what aish is **not** allowed to do:

- Cannot ship opaque binary blobs as primary artifacts
- Cannot store secrets in flat files
- Cannot have irreversible operations as the default
- Cannot be platform-specific where it can be platform-agnostic
- Cannot couple to a single AI vendor

The discipline is what keeps the scope honest. When in doubt, ask: does this decision survive the civilization-grade test? If not, redesign or defer.

## Why Go

- **Single binary per platform** — no runtime, no interpreter, no dependencies.
- **Cross-platform compilation** — `GOOS=windows GOARCH=amd64 go build` produces a native Windows binary from any machine.
- **Fast startup** — critical for a shell. Go binaries start in milliseconds.
- **Strong stdlib for OS primitives** — file I/O, process management, signals, networking.
- **CGO for platform-native APIs** — Win32, WMI, macOS Keychain, launchd where pure Go cannot reach.
- **Goroutines** — speculative execution, streaming inference, background cache warming run concurrently.
- **Proven in the space** — gh CLI, Hugo, k9s, lazygit. Go is the right language for developer tooling.

### Build Targets

```
GOOS=darwin  GOARCH=arm64  → macOS Apple Silicon
GOOS=darwin  GOARCH=amd64  → macOS Intel
GOOS=linux   GOARCH=amd64  → Linux x86_64
GOOS=linux   GOARCH=arm64  → Linux ARM64 (Raspberry Pi, cloud ARM)
GOOS=windows GOARCH=amd64  → Windows x86_64
GOOS=windows GOARCH=arm64  → Windows ARM
```

-----

## Four Pillars

Every architectural decision is evaluated against four non-negotiable properties:

|Pillar              |What it means                                                          |
|--------------------|-----------------------------------------------------------------------|
|**AI Native**       |AI is a pipeline primitive, not a feature. Every operation is AI-aware.|
|**Shell Native**    |aish IS a shell. Pipes, streams, completions — no wrappers.            |
|**OS Insensitive**  |The OS is a kernel. Windows, Linux, macOS are implementation details.  |
|**Fully Reversible**|Nothing is permanent. Structured history IS the versioning system.     |

-----

## System Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                           aish                              │
│                                                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐  │
│  │    Shell     │  │    Intent    │  │     History      │  │
│  │   Runtime    │  │    Cache     │  │     Engine       │  │
│  │  (TUI/PTY)   │  │  (Flywheel)  │  │  (Fully Rev.)    │  │
│  └──────┬───────┘  └──────┬───────┘  └────────┬─────────┘  │
│         │                 │                    │            │
│  ┌──────┴─────────────────┴────────────────────┴────────┐   │
│  │                  OS Translation Layer                │   │
│  │        Win32 | POSIX | macOS APIs | Script Conv.     │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌───────────────┐  ┌──────────────┐  ┌────────────────┐   │
│  │   Inference   │  │   Secrets    │  │    Plugin      │   │
│  │  Plugin API   │  │   Engine     │  │   Registry     │   │
│  └──────┬────────┘  └──────────────┘  └────────────────┘   │
└─────────┼───────────────────────────────────────────────────┘
          │ JSON-RPC plugin contract
          │
   ┌──────┴────────────────────────────────────┐
   │                                           │
┌──┴──────┐  ┌───────────┐  ┌──────────┐  ┌───┴──────┐
│  Ollama │  │   Cloud   │  │  WASM    │  │ Olympus  │
│ Plugin  │  │  Fallback │  │  Plugin  │  │  Plugin  │
└─────────┘  └───────────┘  └──────────┘  └──────────┘
```

-----

## Core Components

### 1. Intent Cache — The Flywheel

The intent cache is the most strategically important component in aish. It is what makes aish fast on any hardware, sustainable at scale, and increasingly capable over time — without increasing AI costs.

**Core insight:** the longer aish runs and the more instances exist, the less AI is needed. Common intents are compiled once and cached forever. The AI is never asked the same question twice.

**The flywheel:**

```
More users → warmer shared cache
Warmer cache → less AI inference needed
Less AI needed → works on constrained hardware
Works on constrained hardware → more users
```

This inverts the economics of every other AI product. Most AI products get more expensive as usage grows. aish gets cheaper. AI inference trends toward zero as the cache matures.

**Cache layers:**

|Layer       |Scope                       |Storage                  |Network                     |
|------------|----------------------------|-------------------------|----------------------------|
|L1 Personal |Your intents on your machine|`~/.aish/cache/`         |None — always offline       |
|L2 Team     |Shared team intents         |Sync via aish team config|LAN or cloud sync           |
|L3 Community|Global opt-in shared cache  |aish cache CDN           |Internet, privacy-preserving|

Community cache ships pre-populated. New installs start warm — thousands of common intents already compiled before the user types their first command.

**Cache entry schema:**

```json
{
  "id": "cache_01JXKT8",
  "intent": "delete log files older than 30 days",
  "intent_embedding": [0.023, -0.847, ...],
  "resolutions": {
    "darwin":  "find . -name '*.log' -mtime +30 -delete",
    "linux":   "find . -name '*.log' -mtime +30 -delete",
    "windows": "Get-ChildItem -Recurse -Filter *.log | Where-Object { $_.LastWriteTime -lt (Get-Date).AddDays(-30) } | Remove-Item"
  },
  "confidence": 0.97,
  "usage_count": 1847,
  "source": "community"
}
```

**Lookup tiers:**

```
Input received
    │
    ▼
Tier 0 — Known binary / built-in?
    yes → passthrough, zero latency (~70% of usage)
    no  ↓
Tier 1 — Cache hit (exact or embedding similarity > threshold)?
    yes → execute compiled invocation (<10ms)
    no  ↓
Tier 2 — Local inference model available?
    yes → infer, compile, cache, execute (<50ms)
    no  ↓
Tier 3 — Cloud inference (async, NDJSON streaming)
         infer, compile, cache, execute
         first token visible before inference completes
```

Once an intent is cached it is never sent to inference again. The model is needed only for genuinely novel intents.

**Speculative execution:** inference begins on keypress. By the time Enter is pressed, the cache lookup or model inference is done or nearly done. The shell uses working directory, recent history, and context to predict with high confidence.

-----

### 2. Shell Runtime

aish is a real shell. It registers as a login shell and replaces bash, zsh, or PowerShell as the developer’s primary interface.

**Core shell capabilities:**

- PTY allocation — proper terminal handling, signals (SIGINT, SIGTERM, SIGWINCH, SIGTSTP)
- Job control — foreground/background, `fg`, `bg`, `jobs`
- Pipe contract — typed streams with automatic boundary coercion
- Expansions — `$VAR`, `$(cmd)`, `~`, glob, brace
- Built-ins — `cd`, `exit`, `source`, `export`, `alias` + aish natives
- RC file — `~/.aish/config.toml`

**Pipe stream types:**

```
text    — plain string, compatible with all unix tools
json    — single structured object
ndjson  — newline-delimited stream (primary AI output format)
table   — structured rows, renders in TUI, pipes as ndjson
```

Boundary coercion is automatic. aish output piped to a legacy text tool coerces to `text`. Legacy tool output piped into aish is received as `text` and interpreted by the cache or local model if structured parsing is needed. Fifty years of existing tooling requires zero changes.

**TUI (BubbleTea):**

- Nerd Font prompt with configurable segments
- Syntax highlighting as-you-type
- Ghost-text auto-suggestions (cache + history)
- Fuzzy + semantic history search (`Ctrl+R`)
- Smart tab completions — flags, paths, git refs, aish commands
- File listings with Nerd Font file-type icons

**Default prompt segments (all configurable):**

```
 ~/projects/aish  main ±  󰊴 local  Δ 42  ❯
   cwd             git    ai-tier  drachma  prompt
```

**Theme system:**

```toml
# ~/.aish/config.toml
[prompt]
theme = "olympus"          # built-in theme
nerd_fonts = true
segments = ["cwd", "git", "ai_tier", "duration"]
```

-----

### 3. Inference Plugin Contract

The inference engine is a plugin. aish does not bundle a model. Any provider implementing the contract plugs in.

**JSON-RPC contract over stdin/stdout:**

```json
// request → plugin stdin
{
  "jsonrpc": "2.0",
  "method": "infer",
  "params": {
    "intent": "find large files modified today",
    "context": {
      "cwd": "/home/user/projects",
      "os": "linux",
      "history_summary": "...",
      "cache_miss": true
    },
    "stream": true
  },
  "id": "req_01"
}

// response → plugin stdout (streaming NDJSON)
{"jsonrpc":"2.0","id":"req_01","result":{"type":"token","data":"find"}}
{"jsonrpc":"2.0","id":"req_01","result":{"type":"token","data":" . -mtime -1"}}
{"jsonrpc":"2.0","id":"req_01","result":{"type":"complete","invocation":"find . -mtime -1 -size +100M","confidence":0.94}}
```

**Available plugins:**

|Plugin                 |Use case                                 |Model                          |
|-----------------------|-----------------------------------------|-------------------------------|
|`aish-inference-ollama`|Capable machines with local GPU/NPU      |User’s choice via Ollama       |
|`aish-inference-cloud` |Any machine with network                 |Claude, OpenAI, Groq, etc.     |
|`aish-inference-wasm`  |Constrained hardware, no GPU             |Purpose-built shell model, WASM|
|`aish-inference-remote`|Team machines offloading to shared server|Remote Ollama endpoint         |
|`olympus`              |Full Olympus stack                       |Hermes routing                 |

The specific model used is the plugin’s decision, not aish’s. aish defines the contract. The plugin brings the model.

-----

### 4. OS Translation Layer

The OS is a kernel. aish is the interface above it. Platform differences are resolved here — the rest of aish never knows which OS it is running on.

**Unified OS concepts:**

|Concept         |Windows       |Linux         |macOS          |aish           |
|----------------|--------------|--------------|---------------|---------------|
|Package install |winget        |apt/yum/pacman|brew           |`aish install` |
|Service control |sc / Task Mgr |systemd       |launchd        |`aish service` |
|Process list    |tasklist      |ps            |ps             |`aish process` |
|File permissions|icacls / ACLs |chmod/chown   |chmod/chown    |`aish permit`  |
|Environment vars|setx          |export        |export         |`aish env`     |
|Network config  |netsh         |ip/ifconfig   |networksetup   |`aish network` |
|Scheduled tasks |Task Scheduler|cron/systemd  |launchd        |`aish schedule`|
|Secrets         |Credential Mgr|libsecret     |Keychain       |`aish secret`  |
|System info     |wmic          |uname/lshw    |system_profiler|`aish system`  |

**Script auto-conversion:**

Any script from any shell is interpreted and executed natively on the current platform.

```bash
aish run deploy.sh          # bash → native on current OS
aish run setup.ps1          # PowerShell → native on Linux/macOS
aish run install.bat        # Windows batch → native on macOS
aish explain deploy.sh      # plain language description
aish migrate deploy.sh      # convert to aish native script
aish audit deploy.sh        # security review before executing
aish diff v1.sh v2.sh       # semantic diff — intent changes not syntax
```

**Supported source formats:** bash, zsh, fish, PowerShell, cmd/bat, Makefile targets.

**Windows is a first-class target, not a port.** Win32, WMI, COM, PowerShell object model — natively, without WSL. The translation layer understands Windows idioms with equal depth to POSIX.

-----

### 5. History Engine — The Versioning System

Structured history is the versioning system. There is no separate version control for the shell. History IS version control.

**Traditional shell history:** a flat text file of strings. Tells you what was typed. Nothing else.

**aish history:** a structured, signed, append-only event log. Tells you what happened, what changed, what the state was before, and how to undo it.

**Event schema:**

```json
{
  "id": "evt_01JXKT8",
  "timestamp": "2026-05-20T14:32:11Z",
  "command": "rm -rf ./dist",
  "intent": "delete the dist build directory",
  "tier": 0,
  "os": "darwin",
  "cwd": "~/projects/aish",
  "exit_code": 0,
  "duration_ms": 12,
  "affected": [
    {
      "path": "./dist",
      "operation": "delete",
      "before_snapshot_id": "snap_01JXKT7"
    }
  ],
  "session_id": "sess_01JXKS",
  "signature": "ed25519:3a9f..."
}
```

Every event is signed (Ed25519). The log is append-only and tamper-evident.

**What history enables:**

|Capability                |Mechanism                                     |
|--------------------------|----------------------------------------------|
|Undo last operation       |Replay event backwards using before-snapshot  |
|Rollback to checkpoint    |Unwind events to named position               |
|Recover deleted file      |Restore from before-snapshot in event         |
|Audit trail               |Signed, timestamped, attributed events        |
|Semantic search           |`aish history search "auth changes last week"`|
|Shell landscape versioning|Config/plugin/theme changes are events too    |

**History commands:**

```bash
aish history                        # structured log, rendered as table
aish history search "auth changes"  # semantic search
aish undo                           # revert last operation
aish undo 5                         # revert last 5 operations
aish checkpoint "before deploy"     # create named checkpoint
aish rollback "before deploy"       # rollback to checkpoint
aish restore ./dist                 # recover deleted path
```

-----

### 6. Identity & Secrets Engine

Identity is “who you are.” Secrets are “what you know.” aish manages both coherently — under one engine, with the same taint-tracking, signing, and reversibility properties.

**Identity material aish unifies:**

|Identity type           |Currently scattered in                  |
|------------------------|----------------------------------------|
|SSH keys                |`~/.ssh`                                |
|GPG keys                |`~/.gnupg`                              |
|Cloud profiles          |`~/.aws`, `~/.azure`, `~/.config/gcloud`|
|Kubernetes contexts     |`~/.kube/config`                        |
|Git identity            |git config (global + per-repo)          |
|OAuth tokens            |per-tool, no standard                   |
|Certificates            |per-app, scattered                      |
|Passkeys / hardware keys|OS keychain                             |

aish exposes these as first-class objects with a schema. Personas are named identity sets that swap atomically.

```bash
aish identity use work        # swaps SSH key, AWS profile, kube context,
                              # git config, OAuth bindings atomically
aish identity use personal    # swap back
aish identity list            # show available personas
aish identity show work       # inspect what work persona contains
```

Every switch is a signed history event. Reversible. Auditable.

**Secrets behavior (unchanged from prior design):**

- Secrets enter with a taint bit set (`aish secret set`, keychain sync, `.env` import)
- Tainted values are **never written to** history, logs, AI context, or pipe output
- Taint propagates through pipes — a tainted pipe chain is fully tainted
- Secrets are unsealed explicitly: `aish secret use KEY -- <command>`
- Every access is a signed event in the history engine

```bash
aish secret set DB_PASSWORD              # stored, tainted
echo $DB_PASSWORD                        # [REDACTED — tainted]
history | grep DB_PASSWORD              # no match — never written
aish secret use DB_PASSWORD -- psql -U admin -W $DB_PASSWORD
```

Identity and secrets compose. A persona may bind specific secrets that become available when that persona is active. Switching personas swaps both identity material and secret bindings together.

-----

### 7. Plugin Registry

aish plugins implement a JSON-RPC contract over stdin/stdout. WASM or native binary. Local-first registry.

```bash
aish plugin install <name>     # install from registry
aish plugin list               # list installed
aish plugin build ./my-plugin  # build and register local plugin
aish plugin remove <name>      # remove — logged as reversible history event
```

The Olympus plugin is the flagship integration — it connects aish to Hermes (model routing), Mnemosyne (cross-session memory), Aegis (emission signing), and the Drachma economy. Claude, Copilot, and any other AI provider can be connected the same way.

-----

### 8. Theming — Brand Atoms Integration

aish does not invent a theming system. It consumes Brand Atoms — the Convergent Systems machine-readable brand encyclopedia. The theming work happens once in Brand Atoms; every aish user benefits.

**The integration model:**

Brand Atoms today defines general brands — palette + font + semantic roles. aish proposes a new brand type in Brand Atoms: `brands/shell/`. A shell brand extends a general brand with shell-specific surface details.

```
brand-atoms/
  atoms/
    palettes/
    fonts/
    glyphs/                     # Nerd Font glyph sets (new atom type)
  brands/
    general/                    # current model
      nord/
      material-3/
    shell/                      # pre-cooked shell designs
      nord-powerline/
      nord-minimal/
      monokai-classic/
      olympus-default/
```

**Shell brand schema:**

```yaml
# brands/shell/nord-powerline.yml
extends: brands/general/nord
type: shell

prompt:
  segments: [cwd, git, ai_tier, drachma]
  separators: powerline
  font: jetbrainsmono-nerdfont

roles:
  ai_tier_local:    $palette.green
  ai_tier_cloud:    $palette.blue
  drachma_low:      $palette.red
  drachma_ok:       $palette.muted
  ghost_suggestion: $palette.muted at 50% opacity

glyphs:
  filetype_map: nerd-default
  git_clean: ""
  git_dirty: "±"
  prompt_char: "❯"
```

**Performance contract — nanosecond render path:**

All theme computation happens at theme load. Render path is pure memory access.

```
Theme load (one-time, ~milliseconds):
  Fetch brand from Brand Atoms
  Resolve role mappings to concrete colors
  Pre-compile ANSI escape sequences for every token
  Store in immutable theme struct with direct field access
  Cache compiled glyph runs

Render path (per character, nanosecond):
  Direct field read of pre-compiled bytes
  Write to output buffer
  Zero string formatting, zero allocations, zero lookups
```

**Theme switching is sub-millisecond:**

```bash
aish theme set nord-powerline    # ~50ms total: fetch + compile + atomic pointer swap
```

Atomic pointer swap to a new pre-compiled theme struct. No restart required.

**Theming surfaces in aish:**

|Surface            |Roles consumed                                     |
|-------------------|---------------------------------------------------|
|Prompt segments    |`primary`, `accent`, `muted`, separator style      |
|Syntax highlighting|keyword, string, number, comment, operator         |
|Ghost suggestions  |`ghost_suggestion`                                 |
|File listings      |per-filetype glyph + color                         |
|Errors / warnings  |`error`, `warning`, `success`                      |
|AI tier indicator  |`ai_tier_local`, `ai_tier_cloud`                   |
|Drachma balance    |threshold-based color (`drachma_low`, `drachma_ok`)|
|History view       |timestamps muted, intent emphasized                |
|Completion menu    |selected vs normal contrast                        |
|Pane borders       |`accent` active, `muted` inactive                  |

**Why this is right architecturally:**

- aish ships no themes — the catalog lives in Brand Atoms
- Community contributes shell brands the same way they contribute palettes today
- New shell brand in Brand Atoms is automatically available in aish
- Olympus can eventually consume the same atoms for its overlay theming
- One brand applies consistently across every Convergent Systems surface

This is civilization-grade theming: typed, versioned, machine-readable, composable, open, and shared across the ecosystem.

## Olympus Integration

Olympus is a plugin. When connected, it replaces or enhances aish’s default capabilities:

|aish default         |Olympus plugin upgrade                                        |
|---------------------|--------------------------------------------------------------|
|Local intent cache   |+ Mnemosyne cross-session semantic memory                     |
|Simple cloud fallback|→ Hermes multi-model routing (Ollama → Claude → Copilot → API)|
|Local Ed25519 signing|→ Aegis governance-grade emission signing                     |
|No governance        |→ 17-panel review on script execution                         |
|No token economy     |→ Drachma budget management                                   |

```bash
aish plugin install olympus    # connect to full Olympus stack
```

-----

-----

## aish-term — The Terminal Emulator

aish ships its own terminal emulator. Not a wrapper around an existing one. A purpose-built application that looks and behaves identically on Windows, macOS, and Linux.

### Why Not Ghostty or iTerm2

|Terminal|Problem for aish                                                                                            |
|--------|------------------------------------------------------------------------------------------------------------|
|Ghostty |No Windows support as of 2026. Platform-native UI means it looks different per OS — opposite of aish’s goal.|
|iTerm2  |macOS only.                                                                                                 |
|WezTerm |Closest existing model. Proves identical cross-platform is achievable. Inspiration, not dependency.         |

Ghostty’s philosophy is “native on each platform.” aish’s goal is “the OS disappears.” These are fundamentally incompatible. When the OS is supposed to be invisible, the terminal cannot announce which OS you’re on by looking different.

### aish-term Design

- **OpenGL renderer everywhere** — same GPU pipeline on Windows, macOS, Linux. No Metal, no GTK, no SwiftUI. Same pixels on every platform.
- **One config file** — `~/.aish/terminal.toml`. Identical on all platforms.
- **Shell integration is structural** — aish-term and aish the shell are the same product. No scripts to install. The terminal knows your prompt, directory, hostname, and history natively.
- **Built-in multiplexing** — tabs, splits, panes. No tmux dependency.
- **Nerd Font native** — not a configuration option. The default assumption.
- **Same plugin API everywhere** — a plugin runs identically on all platforms. No platform variants.

### Borrowed From Prior Art

|Source |What we take                                                                  |
|-------|------------------------------------------------------------------------------|
|Ghostty|`libghostty-vt` for VT sequence parsing — already supports Windows + WASM     |
|Ghostty|Zero-config out of the box philosophy                                         |
|iTerm2 |Deep shell integration — prompt awareness, directory tracking, command history|
|iTerm2 |Context-aware profile switching (prod vs dev, local vs SSH)                   |
|iTerm2 |Inline images via Kitty image protocol                                        |
|WezTerm|Same config and same experience on all platforms                              |
|WezTerm|Built-in multiplexing without external tools                                  |

### Feature Set

- GPU-accelerated rendering (OpenGL, cross-platform)
- Tabs, splits, panes — native, no tmux
- Nerd Font + ligature support
- True color, 256-color, synchronized rendering
- Kitty graphics and keyboard protocols
- Inline image display
- Searchable scrollback
- Context-aware profile switching
- SSH integration
- Configurable keybindings (TOML)
- Hundreds of built-in themes
- Hot-reload config on save

-----

## Command Resolution & Exec Layer

The shell’s command resolution pipeline is the mechanism that routes every input to the correct execution path. This is how existing tools like `az`, `kubectl`, `git`, and `docker` run natively without rewriting them as plugins.

### Resolution Pipeline

```
User input: "az group list --output json"
    │
    ▼
Parse — tokenize input, extract command + args + flags
    │
    ▼
Resolve (in order):
  1. aish built-in?       cd, export, alias, aish natives → execute directly
  2. Alias?               check alias table → expand and re-resolve
  3. aish plugin?         check plugin registry → invoke via JSON-RPC
  4. PATH lookup?         which az → /usr/local/bin/az → exec
  5. Intent cache hit?    natural language → compiled invocation → re-resolve
  6. Inference?           novel intent → infer → compile → cache → re-resolve
    │
    ▼
Exec or invoke
```

### Exec — Pure Go, No C

Go’s `os/exec` handles all external program execution. No CGO. No C. Works identically on all platforms.

```go
// Non-interactive — capture and type the output stream
cmd := exec.Command("az", "group", "list", "--output", "json")
cmd.Env = append(os.Environ(), shellEnv...)
stdout, _ := cmd.StdoutPipe()
stderr, _ := cmd.StderrPipe()
cmd.Start()
// feed stdout into output type detector → StreamJSON / StreamNDJSON / StreamText

// Interactive — full PTY passthrough (vim, ssh, htop, az login)
// github.com/creack/pty — pure Go, no C required
ptmx, _ := pty.Start(cmd)
io.Copy(ptmx, os.Stdin)   // stdin → process
io.Copy(os.Stdout, ptmx)  // process → terminal
```

CGO is only required for platform-native OS APIs — Win32, WMI, macOS Keychain. Never for exec.

### Output Type Detection

When an external program’s output is consumed by aish, the stream is automatically typed. This is what makes existing tools AI-native without any rewrite.

```
stdout received
    │
    ▼
Probe first 512 bytes
    │
    ├── valid JSON object?        → StreamJSON
    ├── valid NDJSON lines?       → StreamNDJSON
    ├── tab-separated columns?    → StreamTable
    └── everything else          → StreamText
```

This means:

```bash
# az outputs JSON — aish detects it, types it, AI can operate on it
az group list --output json | aish "show only production groups"

# kubectl outputs YAML — aish types it, AI interprets it
kubectl get pods -o json | aish "which pods are not running"

# git log — aish types as text, AI still works
git log --oneline | aish "summarize what changed this week"
```

Existing tooling gets AI-native pipe capabilities for free. No plugins. No rewrite.

### Plugin Boundary — Precise Definition

|Mechanism       |Used for                                      |Examples                                     |
|----------------|----------------------------------------------|---------------------------------------------|
|**Exec**        |Existing binaries in PATH                     |az, git, kubectl, docker, npm, terraform     |
|**Built-ins**   |Shell primitives                              |cd, export, alias, aish history, aish secret |
|**Plugins**     |New capabilities needing deep aish integration|Olympus, inference engines, aish-native tools|
|**Intent cache**|Natural language → any of the above           |“list my Azure resource groups” → exec az    |

Plugins are for things that do not exist yet or need the aish pipe contract natively. Existing CLI tools are exec’d. Day one aish has the entire ecosystem of existing tools available with zero migration cost.

### Signal Handling and Job Control

Exec in aish correctly handles:

- `SIGINT` (Ctrl+C) — forwarded to child process
- `SIGTSTP` (Ctrl+Z) — suspend child, return to shell
- `SIGWINCH` — window resize forwarded to PTY
- Exit codes — captured, logged as history event, available in `$?`
- Pipelines — `cmd1 | cmd2` — both processes managed, exit codes of both captured

## Scope Discipline

The full aish vision is multi-year work. Building it all at once is how projects die. This roadmap is ordered to prove or kill the central thesis as fast as possible.

**The central thesis:** the intent cache flywheel means AI inference approaches zero for common operations as usage grows. If this is true, aish is sustainable, scalable, and economically defensible. If this is false, aish is just another AI tool with bad unit economics.

**Kill criterion for the whole project:** if cache hit rate does not grow meaningfully with usage in v0.1, the core bet is wrong. We need to know that in month 3, not month 18.

Everything is sequenced against this: v0.1 exists to validate or invalidate the thesis. Nothing else.

-----

## Versioned Roadmap

```
v0.1 — Thesis Validation        (90 days)   Prove the cache flywheel
v0.2 — Layer Polish             (60 days)   Make aish feel good as a layer
v0.3 — Real Shell               (90 days)   Login shell capability
v1.0 — Windows Native           (120 days)  The differentiation play
v1.5 — aish-term                (TBD)       Custom terminal emulator
```

aish runs **inside any existing terminal** through v0.3. The custom terminal emulator (aish-term) is v1.5 — only after the shell itself is proven. WezTerm or any other terminal hosts aish during validation.

-----

## v0.1 — Thesis Validation (90 days)

**Goal:** measure intent cache hit rate growth over 30 days of typical developer usage with 100 users.

**Success criterion:** cache hit rate reaches >50% by day 30 of typical usage.
**Kill criterion:** cache hit rate stagnates below 20% or shows no growth.

What v0.1 deliberately does NOT include: PTY (no vim/ssh/htop), local model, secrets management, plugins, login shell, Windows, history search, multiplexing, terminal emulator, Olympus integration. Add nothing that does not directly test the cache thesis.

-----

### Epic v0.1-1 — Minimum Shell

> aish runs as a command inside any terminal. Parses input, resolves to exec or inference, executes.

- [ ] Go project skeleton, build pipeline for macOS + Linux
- [ ] CLI entry point — `aish` command launches interactive prompt
- [ ] Command parser — tokenize input, handle quotes, flags, pipes
- [ ] Exec via `os/exec` for non-interactive commands
- [ ] Stdin/stdout/stderr piping between commands
- [ ] Working directory tracking
- [ ] Environment variable passthrough
- [ ] Exit code capture and `$?` support
- [ ] Output type detection — probe first 512 bytes, classify as text/json/ndjson
- [ ] Basic prompt (no Nerd Font yet — that is polish)

**Risk:** Low. This is standard Go shell work.

-----

### Epic v0.1-2 — Intent Cache L1

> The flywheel. Personal cache on local disk, SQLite + embedding index.

- [ ] Cache schema design (intent, embedding, per-OS resolutions, confidence, usage count)
- [ ] SQLite cache store at `~/.aish/cache.db`
- [ ] Exact-match lookup before any inference
- [ ] Embedding generation for cache entries (use cloud inference plugin)
- [ ] Embedding similarity lookup with configurable threshold
- [ ] Cache write path — after inference succeeds, compile + store
- [ ] Per-OS resolution storage (darwin, linux entries from day one)
- [ ] Cache hit rate metric tracked locally
- [ ] `aish cache stats` command — shows hit rate, size, top intents

**Risk:** HIGH. This is the thesis. If similarity matching does not yield high hit rates, the project pivots or dies.

-----

### Epic v0.1-3 — Cloud Inference Plugin

> Single inference path for v0.1. Claude API. NDJSON streaming.

- [ ] Inference plugin contract definition (JSON-RPC over stdin/stdout)
- [ ] Claude API plugin implementation
- [ ] Streaming NDJSON response handling
- [ ] Timeout and retry logic
- [ ] API key management via env var (proper secrets engine is v0.3)
- [ ] Per-request cost tracking (logged for measurement)

**Risk:** Low. API integration work.

-----

### Epic v0.1-4 — Basic Reversibility

> The viral demo feature. Restore deleted files.

- [ ] Structured event schema (JSON, append-only)
- [ ] Event log at `~/.aish/history.db` (SQLite WAL)
- [ ] Detect `rm` and equivalent destructive commands before execution
- [ ] Pre-execution snapshot — copy file content to `~/.aish/snapshots/`
- [ ] Snapshot size limit configurable (default 100MB per file)
- [ ] `aish undo` — restore last destructive operation
- [ ] `aish restore <path>` — restore specific deleted path
- [ ] Skip snapshots for files matching `.gitignore`-style patterns (node_modules, etc.)

**Risk:** Medium. Snapshot storage growth needs management. Configurable limits are important.

**Note:** v0.1 scope is delete operations only. Modifications and moves come in v0.2.

-----

### Epic v0.1-5 — Telemetry & Measurement

> Cannot validate the thesis without measurement.

- [ ] Opt-in anonymous telemetry (clear consent on first run)
- [ ] Per-session metrics: total commands, cache hits, cache misses, inference calls
- [ ] Cache hit rate over time series
- [ ] Inference cost tracking (Drachma equivalent or USD)
- [ ] Local dashboard via `aish stats`
- [ ] Aggregate dashboard for the team to see across users (privacy-preserving)

**Risk:** Low technically. Privacy framing matters — be explicit and honest.

-----

### v0.1 Decision Gate

At end of 90 days, the team reviews:

1. What is the median cache hit rate across users after 30 days of usage?
1. Is hit rate growing or stagnating?
1. What inference costs would a typical user incur per month?

**Go decision:** hit rate >50%, still growing, costs sustainable → proceed to v0.2.
**Pivot decision:** hit rate growing slowly but encouraging → extend v0.1 with cache improvements.
**Kill decision:** hit rate stuck below 20% or costs unsustainable → abandon or fundamentally rethink.

-----

## v0.2 — Layer Polish (60 days)

**Goal:** make aish feel genuinely good to use as a layer inside any terminal. Build the experience that converts trial users to daily users.

-----

### Epic v0.2-1 — Interactive Shell UX

- [ ] BubbleTea TUI integration
- [ ] Auto-suggestions from cache + history (ghost text)
- [ ] Syntax highlighting as-you-type
- [ ] Smart tab completions (paths, flags, git refs)
- [ ] `Ctrl+R` fuzzy history search
- [ ] Nerd Font prompt with cwd + git + ai-tier segments

### Epic v0.2-2 — PTY Support

- [ ] `github.com/creack/pty` integration
- [ ] PTY allocation for interactive programs
- [ ] Signal forwarding — SIGINT, SIGTSTP, SIGWINCH
- [ ] Terminal size propagation
- [ ] Test against: vim, ssh, htop, less, top, az login

### Epic v0.2-3 — Community Cache (L3)

- [ ] Cache bundle format definition
- [ ] Curated initial community cache (1000+ common intents)
- [ ] Ship pre-populated cache with installer
- [ ] Privacy-preserving cache contribution flow (opt-in)
- [ ] Cache signing for trust

### Epic v0.2-4 — Script Translation

- [ ] bash script reader and intent extractor
- [ ] zsh script reader
- [ ] fish script reader
- [ ] `aish run <script>` — translate and execute
- [ ] `aish explain <script>` — plain language description
- [ ] `aish migrate <script>` — output aish-native script

-----

### Epic v0.2-5 — Brand Atoms Theming

- [ ] Brand Atoms `shell` brand type schema (PR to brand-atoms repo)
- [ ] Brand Atoms fetch + cache client in aish
- [ ] Theme loader — resolve brand to concrete theme struct
- [ ] ANSI escape sequence pre-compilation
- [ ] Atomic theme swap (`aish theme set <name>`)
- [ ] 10 curated shell brands published to Brand Atoms at launch
- [ ] `aish theme list` — show available brands
- [ ] `aish theme preview <name>` — show theme without applying
- [ ] Theme persistence in `~/.aish/config.toml`
- [ ] Performance test — confirm sub-50ms theme switch, sub-microsecond per-character render

**Risk:** Low for aish (consumer side is straightforward). Brand Atoms PR for `shell` brand type schema is a coordination dependency.

## v0.3 — Real Shell (90 days)

**Goal:** make aish a viable login shell on macOS and Linux. Replace bash/zsh for users who want to.

-----

### Epic v0.3-1 — Login Shell Capabilities

- [ ] Job control — fg, bg, jobs
- [ ] Process group management
- [ ] Login shell registration (`/etc/shells`, `chsh`)
- [ ] RC file loading from `~/.aish/config.toml`
- [ ] Shell built-ins: cd, export, alias, source, set, unset
- [ ] Brace expansion, glob, command substitution

### Epic v0.3-2 — Plugin Registry

- [ ] Plugin JSON-RPC contract finalized
- [ ] `aish plugin install/list/remove/build` commands
- [ ] Local plugin manifest format
- [ ] First community plugin: Ollama inference
- [ ] Plugin lifecycle management (spawn, health, restart)

### Epic v0.3-3 — Identity & Secrets Engine

- [ ] Taint bit type system in Go
- [ ] `aish secret set/get/list/remove`
- [ ] History event exclusion for tainted values
- [ ] Pipe taint propagation
- [ ] macOS Keychain integration
- [ ] Linux libsecret integration
- [ ] Identity persona schema and storage
- [ ] `aish identity use/list/show/create`
- [ ] Atomic persona switching — SSH, cloud profiles, kube context, git config
- [ ] Persona-bound secrets (secrets that activate with a persona)
- [ ] Signed history events for every persona switch and secret access

### Epic v0.3-4 — History Engine Maturity

- [ ] Ed25519 signing for all history events
- [ ] `aish checkpoint <name>` and `aish rollback <name>`
- [ ] Modification snapshots (not just deletes)
- [ ] Move/rename tracking
- [ ] Semantic history search using embeddings
- [ ] `aish history search "<query>"`

-----

## v1.0 — Windows Native (120 days)

**Goal:** the differentiation play. Make Windows a first-class developer platform without WSL.

**This is the highest-risk technical work.** Win32 CGO bindings are complex. The team must be honest about scope and willing to defer features to v1.1 if blocking.

-----

### Epic v1.0-1 — Windows Build Pipeline

- [ ] Windows binary cross-compilation from CI
- [ ] Signed Windows installer (MSI or modern equivalent)
- [ ] Windows Terminal compatibility verification
- [ ] PowerShell compatibility for hybrid users

### Epic v1.0-2 — Win32 OS Translation

- [ ] Win32 CGO bindings (subset needed)
- [ ] `aish install` via winget
- [ ] `aish service` via Win32 Service Control Manager
- [ ] `aish process` via Win32 process APIs
- [ ] `aish env` via Win32 environment APIs
- [ ] `aish network` basic via Win32 networking APIs

### Epic v1.0-3 — Windows Script Translation

- [ ] PowerShell script reader and intent extractor
- [ ] cmd/bat script reader
- [ ] Test against common Windows admin scripts

### Epic v1.0-4 — Windows Secrets

- [ ] Windows Credential Manager CGO integration
- [ ] `aish secret` parity with macOS/Linux behavior

### Epic v1.0-5 — Windows Login Shell

- [ ] Windows console host integration
- [ ] PTY support on Windows (ConPTY)
- [ ] Registry changes for default shell (advanced setup)

-----

## v1.5 — aish-term (Scope TBD)

**Gate to enter v1.5:** aish shell has >10,000 active users across all platforms.

If aish has not achieved meaningful adoption as a shell-in-any-terminal by v1.0, building a custom terminal emulator is premature. Ghostty took years of dedicated work. WezTerm took years. Do not start until the shell is genuinely successful.

When eventually built:

- [ ] OpenGL renderer (cross-platform, identical pixels)
- [ ] `libghostty-vt` integration for VT parsing
- [ ] Built-in tabs, splits, panes (no tmux dependency)
- [ ] Single TOML config across all platforms
- [ ] Native shell integration with aish
- [ ] Kitty graphics protocol support
- [ ] Nerd Font defaults

-----

## Go-to-Market Strategy

The work above is necessary but not sufficient. Adoption strategy runs in parallel.

### Distribution Channels (ordered by likely impact)

1. **Olympus user base** — every Olympus user is a warm aish prospect. Cross-promote at v0.1 launch.
1. **Windows developer communities** — biggest underserved market. Focus content here at v1.0.
1. **HackerNews / r/programming launch posts** — one well-written architecture post at each version milestone.
1. **GitHub stars and visibility** — strong README, clear value prop, active development cadence.
1. **YouTube and conference demos** — the 60-second “delete and recover a file” demo is the single most viral asset.

### Content Pillars

1. **“The shell was designed before AI existed”** — the intellectual thesis. Blog post + HN launch.
1. **“You can never accidentally delete a file again”** — the emotional hook. Video demo.
1. **“AI inference costs that decrease as you use it more”** — the economic story. Counterintuitive, shareable.
1. **“Finally a real terminal for Windows developers”** — the differentiation. Target at v1.0.

### Validation Targets

|Milestone   |Target                                                 |
|------------|-------------------------------------------------------|
|v0.1 launch |100 daily active users (mostly Olympus + invited)      |
|v0.2 launch |1,000 daily active users                               |
|v0.3 launch |10,000 daily active users                              |
|v1.0 launch |50,000 daily active users (with Windows unlock)        |
|v1.5 trigger|Sustained 10,000+ DAU before starting terminal emulator|

These are honest targets, not aspirational ones. Shells adopt slowly. If v0.1 cannot get to 100 DAU within 60 days of launch, the value proposition is not landing and we need to reposition before building more.

-----


---

## Ecosystem (other repos)

aish does not document the ecosystem here. See:

- **[github.com/convergent-systems-co/xdao](https://github.com/convergent-systems-co/xdao)** — The Convergent Systems Ecosystem overview, XDAO federation layer, the *-Atoms catalogs, catalog build order, and distributed-federation architecture.
- **[github.com/convergent-systems-co/atoms-spec](https://github.com/convergent-systems-co/atoms-spec)** — The Atoms Catalog Standard. Required repository structure, ATOMS.yml manifest schema, atom/composition/rule schemas, required exports, CI workflows, federation registration, bootstrap procedure.
- **[github.com/convergent-systems-co/atoms](https://github.com/convergent-systems-co/atoms)** — Umbrella super-project with every *-Atoms catalog as a git submodule.
- **[github.com/convergent-systems-co/atoms-tools](https://github.com/convergent-systems-co/atoms-tools)** — CLI for validation, export, bootstrap, and federation resolution.
