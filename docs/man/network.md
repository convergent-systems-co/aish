---
title: network(1)
parent: Manual pages
permalink: /man/network/
---

# network(1)

## NAME

**network** — inspect network adapters and routes (Windows).

## SYNOPSIS

```
network interfaces
network routes
```

## DESCRIPTION

`network interfaces`
: List every adapter with name, MAC, first IPv4 (when present),
  and operational status. Multi-IP adapters surface only the first
  IPv4 address; the full detail view is reserved for a future
  release.

`network routes`
: Not yet implemented. Requires `GetIpForwardTable2` plus a column
  model the MVP doesn't pay for.

On non-Windows hosts every subcommand exits 2 with
"not supported on this host (Windows only)".

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | List rendered. |
| 1 | Win32 error. |
| 2 | Missing or unknown subcommand, or non-Windows host. |

## EXAMPLES

```
network interfaces
```

## SEE ALSO

[service(1)](../service/), [process(1)](../process/).
