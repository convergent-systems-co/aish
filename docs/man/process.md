---
title: process(1)
parent: Manual pages
permalink: /man/process/
---

# process(1)

## NAME

**process** — list or terminate processes (Windows).

## SYNOPSIS

```
process list
process kill <pid>
```

## DESCRIPTION

`process list`
: Print every process with PID, parent PID, and process name.
  Columns `CPU%` and memory are reserved for a future release —
  they require per-PID `QueryFullProcessImageName` plus a CPU
  sampling interval the MVP doesn't pay for.

`process kill <pid>`
: Open the target process with `PROCESS_TERMINATE` and call
  `TerminateProcess`. The Win32 error (typically
  `ERROR_ACCESS_DENIED` without `SeDebugPrivilege`) is surfaced
  verbatim.

On non-Windows hosts every subcommand exits 2 with
"not supported on this host".

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | List rendered, or process terminated. |
| 1 | Win32 error (access denied, no such PID, …). |
| 2 | Missing or unknown subcommand, or non-Windows host. |

## EXAMPLES

```
process list
process kill 4144
```

## SEE ALSO

[service(1)](../service/), [install(1)](../install/).
