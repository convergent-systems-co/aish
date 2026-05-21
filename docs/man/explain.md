---
title: explain(1)
parent: Manual pages
permalink: /man/explain/
---

# explain(1)

## NAME

**explain** — describe a bash/zsh/fish script statement by statement.

## SYNOPSIS

```
explain [--with-llm] <script>
```

## DESCRIPTION

Reads `<script>`, parses it into the aish AST, and renders a
numbered description of every top-level statement. The default
output is **deterministic** — same input yields the same output
across runs. With `--with-llm`, the descriptions are enriched by
the inference plugin (if an API key is configured); the LLM is
asked to elaborate on each statement's effect.

`explain` is intended for code review and onboarding — print a
plain-English version of a script before you run it.

## OPTIONS

`--with-llm`
: Pass each statement through the inference plugin for richer
  descriptions. Requires a configured plugin (see [plugin(1)](../plugin/)).

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Script described. |
| 1 | Script not loadable, or LLM enrichment failed. |
| 2 | Missing argument or parse failure. |

## EXAMPLES

```
explain ./deploy.sh
explain --with-llm ~/scripts/build.fish
```

## SEE ALSO

[run(1)](../run/), [migrate(1)](../migrate/).
