---
title: Manual pages
layout: default
nav_order: 2
has_children: true
permalink: /man/
---

# Manual pages

Each page in this section follows the classic Unix manpage layout:

| Section       | Purpose                                                          |
|---------------|------------------------------------------------------------------|
| **NAME**      | Command name and one-line summary.                               |
| **SYNOPSIS**  | Invocation grammar. Brackets denote optional arguments.          |
| **DESCRIPTION** | What the command does, in detail.                              |
| **OPTIONS**   | Flags and their meanings (when present).                         |
| **EXIT STATUS** | Exit codes and what they mean.                                 |
| **EXAMPLES**  | Concrete invocations.                                            |
| **FILES**     | State on disk the command reads or writes.                       |
| **SEE ALSO**  | Related manpages.                                                |

All commands documented here are **built-ins** — the shell handles
them in-process before dispatch ever reaches `$PATH`. To check
whether a name is a built-in in the running shell, observe the
prompt's syntax highlighting (built-ins are color-coded by the
active theme) or run the command with no arguments — every built-in
prints a usage line when it can.
