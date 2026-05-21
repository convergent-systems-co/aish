---
title: run(1)
parent: Manual pages
permalink: /man/run/
---

# run(1)

## NAME

**run** — run a bash, zsh, or fish script through the aish dispatch tier.

## SYNOPSIS

```
run <script>
```

## DESCRIPTION

Reads `<script>`, detects the dialect from the file extension or
shebang, parses it into an aish-native AST, and runs each statement
through the shell's normal dispatch tier. Cache and history
interceptors apply, so an unrecognised command in the script still
benefits from intent resolution.

Each `run` invocation is a clean session: a fresh env copy is
built from the parent shell's env, so in-script assignments cannot
leak back into the REPL.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Script executed to completion (last statement exit 0). |
| Other | Last statement's exit code propagates. |
| 1 | Script could not be loaded. |
| 2 | Missing argument or parse failure. |

## EXAMPLES

```
run ./build.sh
run ~/scripts/deploy.fish
```

## SEE ALSO

[explain(1)](../explain/), [migrate(1)](../migrate/).
