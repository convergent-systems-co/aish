---
title: service(1)
parent: Manual pages
permalink: /man/service/
---

# service(1)

## NAME

**service** — query and control Windows services (Service Control Manager).

## SYNOPSIS

```
service list
service status <name>
service start <name>
service stop <name>
```

## DESCRIPTION

Wraps the Windows Service Control Manager (SCM).

`service list`
: Enumerate all installed services with name, display name, and
  current status.

`service status <name>`
: Report status, start type (`Auto`, `Manual`, `Disabled`), and
  display name for one service.

`service start <name>` / `service stop <name>`
: Toggle a service through the SCM. Both require an elevated
  runtime; without admin rights the Win32 error (typically
  `ERROR_ACCESS_DENIED`) is surfaced verbatim and the built-in
  exits 1.

On non-Windows hosts every subcommand exits 2 with
"not supported on this host".

## EXIT STATUS

| Code | Meaning |
|------|---------|
| 0 | Subcommand succeeded. |
| 1 | SCM error (no such service, access denied, …). |
| 2 | Missing or unknown subcommand, or non-Windows host. |

## EXAMPLES

```
service list
service status Spooler
service stop W3SVC
```

## SEE ALSO

[install(1)](../install/), [process(1)](../process/).
