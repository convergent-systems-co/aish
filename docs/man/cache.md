---
title: cache(1)
parent: Manual pages
permalink: /man/cache/
---

# cache(1)

## NAME

**cache** — inspect or clear the intent cache.

## SYNOPSIS

```
cache stats
cache clear
```

## DESCRIPTION

`aish` keeps a local SQLite cache of natural-language **intents**
and the shell **invocations** they compile to. Every successful
plugin inference is written back so the same intent never costs
twice. The cache is the heart of the project's economic thesis:
inference cost approaches zero for common operations as usage grows.

`cache stats`
: Prints `Hits: N | Misses: M | Hit rate: P% | Entries: K` where:
  - `Hits` — intent lookups that resolved in the L1 cache.
  - `Misses` — intent lookups that fell through to the inference plugin.
  - `Hit rate` — `Hits / (Hits + Misses)` as a percentage.
  - `Entries` — distinct `(intent_hash, os)` rows currently stored.

`cache clear`
: Truncates the cache and resets the hit/miss counters. The next
  natural-language intent pays a full inference round-trip.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | Cache unavailable (failed to open `~/.aish/cache.db`). |
| 2 | Missing or unknown subcommand. |

## EXAMPLES

```
$ cache stats
Hits: 84 | Misses: 12 | Hit rate: 87% | Entries: 41
$ cache clear
```

## FILES

`~/.aish/cache.db` — the L1 store.

## SEE ALSO

[stats(1)](../stats/), [community(1)](../community/),
[plugin(1)](../plugin/).
