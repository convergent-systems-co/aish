---
title: community(1)
parent: Manual pages
permalink: /man/community/
---

# community(1)

## NAME

**community** — manage the L3 community-cache bundle.

## SYNOPSIS

```
community info
community status
community install [--force] [<dir>]
community refresh
community contribute <intent>
```

## DESCRIPTION

aish ships with a three-tier cache:

- **L1** — per-machine intent cache in `~/.aish/cache.db`.
- **L2** — the inference plugin (reserved for future shared
  caches; not user-facing yet).
- **L3** — the **community bundle**, a signed read-only set of
  high-quality intents collected from opt-in contributors.

The community bundle is loaded once at shell startup and consulted
on cache miss before the plugin is called.

`community info`
: Print bundle path, version, signer identity, total intent count,
  and per-source counts.

`community status`
: One line: `installed` or `absent`.

`community install [--force] [<dir>]`
: Install a community bundle from a directory on disk (default:
  the current working directory). Refuses downgrade — pass
  `--force` to install a bundle older than what's currently
  loaded.

`community refresh`
: Alias for `install --force`.

`community contribute <intent>`
: Append `<intent>` plus its resolved invocation and the host OS
  to `~/.aish/community-contribute.jsonl` for offline review. **No
  network call** — review and submit the file yourself.

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | Subcommand failed (signature error, downgrade refused, I/O). |
| 2 | Missing or unknown subcommand. |

## EXAMPLES

```
community info
community install ./aish-community-v3.tar
community contribute "find every large file under /var"
```

## SEE ALSO

[cache(1)](../cache/), [plugin(1)](../plugin/).
