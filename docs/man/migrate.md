---
title: migrate(1)
parent: Manual pages
permalink: /man/migrate/
---

# migrate(1)

## NAME

**migrate** — translate a bash/zsh/fish script to aish-native syntax.

## SYNOPSIS

```
migrate <script>
```

## DESCRIPTION

Reads `<script>`, parses it into the aish AST, and emits an
equivalent aish-native script on stdout. Translation is **rule-based
(no LLM)** so the output is reproducible — the same input always
emits the same output.

`migrate` is the "port my old shell scripts" tool. It does not
attempt to fix logic bugs in the source; it only rewrites syntax.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Translation emitted. |
| 1 | Script not loadable. |
| 2 | Missing argument or parse failure (unsupported construct). |

## EXAMPLES

```
$ migrate ./old.sh > ./new.aish
```

## SEE ALSO

[run(1)](../run/), [explain(1)](../explain/).
