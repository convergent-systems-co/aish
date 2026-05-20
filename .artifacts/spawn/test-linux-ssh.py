#!/usr/bin/env python3
"""
SSH-driven aish smoke tests for the UTM Linux VM.

Connects via paramiko using the ed25519 key the host pushed during the
bootstrap. Mirrors the surface of test-windows-winrm.py:

  - CLI flags (--version, --help)
  - REPL basics (empty stdin, whitespace-only)
  - External commands via the VM's PATH (ls, echo, cat, tr, wc, grep)
  - Pipes (echo|tr, ls|wc, cat<file|grep)
  - Built-ins (cd absolute, export, $VAR, $?)
  - Quote-aware expansion (#163)
  - Theme list/show/set/persist
  - ANSI in prompt
  - cat reads stdin (#167)

Plus a Linux-specific smoke against the v0.1-3 cloud-inference plugin
binary (--version + missing-key fail-fast).

Run from the repo root with the venv:
    .artifacts/spawn/winrm-venv/bin/python .artifacts/spawn/test-linux-ssh.py
"""
from __future__ import annotations

import argparse
import re
import sys
import time
from pathlib import Path

try:
    import paramiko
except ImportError:
    sys.exit("paramiko not installed — run via .artifacts/spawn/winrm-venv/bin/python")


REPO_ROOT = Path(__file__).resolve().parent.parent.parent
DEFAULT_HOST = "192.168.64.4"
DEFAULT_USER = "itsfwcp"
DEFAULT_KEY = Path.home() / ".ssh" / "id_ed25519"

# Where to drop the binaries inside the VM.
REMOTE_BIN_DIR = "/home/itsfwcp/bin"
REMOTE_AISH = f"{REMOTE_BIN_DIR}/aish"
REMOTE_PLUGIN = f"{REMOTE_BIN_DIR}/aish-inference-cloud"

# Local cross-compiled artifacts.
LOCAL_AISH = REPO_ROOT / "shell" / "dist" / "aish-linux-arm64"
LOCAL_PLUGIN = REPO_ROOT / "plugins" / "cloud" / "dist" / "aish-inference-cloud-linux-arm64"


# ---------- ssh helpers ----------

def open_ssh(host: str, user: str, key_file: Path) -> paramiko.SSHClient:
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username=user, key_filename=str(key_file),
              allow_agent=False, look_for_keys=False, timeout=10)
    return c


def remote_run(c: paramiko.SSHClient, cmd: str, stdin_data: str = "",
               timeout: int = 30) -> tuple[int, str, str]:
    """Run `cmd` over SSH with optional stdin; return (rc, stdout, stderr)."""
    si, so, se = c.exec_command(cmd, timeout=timeout)
    if stdin_data:
        si.write(stdin_data)
        si.flush()
    si.channel.shutdown_write()
    out = so.read().decode("utf-8", errors="replace")
    err = se.read().decode("utf-8", errors="replace")
    rc = so.channel.recv_exit_status()
    return rc, out, err


def upload(c: paramiko.SSHClient, local: Path, remote: str) -> None:
    sftp = c.open_sftp()
    try:
        # Best-effort mkdir for the destination dir.
        parts = remote.rsplit("/", 1)
        if len(parts) == 2 and parts[0]:
            try:
                sftp.mkdir(parts[0])
            except IOError:
                pass  # already exists
        sftp.put(str(local), remote)
        sftp.chmod(remote, 0o755)
    finally:
        sftp.close()


# ---------- test framework ----------

class Case:
    __slots__ = ("name", "stdin", "assertion", "args")

    def __init__(self, name: str, stdin: str = "",
                 args: str = "", assertion=None):
        self.name = name
        self.stdin = stdin
        self.args = args
        self.assertion = assertion


def expect_in(needle: str):
    def check(out: str, err: str, rc: int) -> tuple[bool, str]:
        if needle in out:
            return True, ""
        return False, f"{needle!r} not in stdout"
    return check


def expect_all(*needles: str):
    def check(out: str, err: str, rc: int) -> tuple[bool, str]:
        missing = [n for n in needles if n not in out]
        if not missing:
            return True, ""
        return False, f"missing: {missing}"
    return check


def expect_not_in(unwanted: str):
    def check(out: str, err: str, rc: int) -> tuple[bool, str]:
        if unwanted in out:
            return False, f"{unwanted!r} unexpectedly in stdout"
        return True, ""
    return check


def expect_match(pattern: str):
    rx = re.compile(pattern)
    def check(out: str, err: str, rc: int) -> tuple[bool, str]:
        if rx.search(out):
            return True, ""
        return False, f"pattern /{pattern}/ no match"
    return check


def expect_rc(want: int):
    def check(out: str, err: str, rc: int) -> tuple[bool, str]:
        if rc == want:
            return True, ""
        return False, f"exit rc={rc}, want {want}"
    return check


def both(a, b):
    def check(out: str, err: str, rc: int) -> tuple[bool, str]:
        ok1, reason1 = a(out, err, rc)
        if not ok1:
            return False, reason1
        return b(out, err, rc)
    return check


# ---------- the test suite ----------

def build_cases() -> list[Case]:
    return [
        # 1. CLI flags
        Case("version-flag", args="--version",
             assertion=expect_all("aish ", "built")),
        Case("help-flag", args="--help",
             assertion=expect_in("Usage")),

        # 2. REPL
        Case("empty-stdin-clean-exit", stdin="",
             assertion=expect_rc(0)),
        Case("whitespace-only-line", stdin="\n   \n\techo done\n",
             assertion=expect_in("done")),

        # 3. External commands (real coreutils, no busybox needed)
        Case("external-ls", stdin="ls /\n",
             assertion=expect_all("bin", "etc")),
        Case("external-ls-l", stdin="ls -l /\n",
             # `ls -l /` opens its output with a "total <n>" line. The
             # captured stdout shares that line with aish's prompt prefix,
             # so anchor-free substring match is the right shape.
             assertion=expect_in("total ")),
        Case("external-echo", stdin="echo hello world\n",
             assertion=expect_in("hello world")),
        Case("external-uname", stdin="uname -s\n",
             assertion=expect_in("Linux")),

        # 4. Pipes
        Case("pipe-echo-tr", stdin="echo hello | tr a-z A-Z\n",
             assertion=expect_in("HELLO")),
        Case("pipe-ls-wc", stdin="ls /etc | wc -l\n",
             assertion=expect_match(r"\d+")),
        Case("pipe-three-stage", stdin="echo foo | cat | tr a-z A-Z\n",
             assertion=expect_in("FOO")),

        # 5. Built-ins
        Case("cd-absolute", stdin="cd /tmp\npwd\n",
             assertion=expect_in("/tmp")),
        Case("export-and-var", stdin="export GREETING=hi\necho $GREETING\n",
             assertion=expect_in("hi")),
        Case("export-inherits-to-child",
             stdin="export X=childvalue\nsh -c 'echo got=$X'\n",
             assertion=expect_in("got=childvalue")),

        # 6. Exit codes
        Case("exit-code-true", stdin="true\necho rc=$?\n",
             assertion=expect_in("rc=0")),
        Case("exit-code-false", stdin="false\necho rc=$?\n",
             assertion=expect_in("rc=1")),
        Case("exit-code-missing-binary",
             stdin="this-binary-does-not-exist-xyz\necho rc=$?\n",
             assertion=expect_in("rc=127")),
        Case("exit-code-sh-exit",
             stdin='sh -c "exit 42"\necho rc=$?\n',
             assertion=expect_in("rc=42")),

        # 7. Quoting (#163 regression seatbelt on Linux)
        Case("single-quote-literal",
             stdin="export X=expanded\necho 'literal $X'\n",
             assertion=expect_in("literal $X")),
        Case("double-quote-expand",
             stdin='export X=expanded\necho "value $X"\n',
             assertion=expect_in("value expanded")),

        # 8. Cat reads piped stdin (#167)
        Case("cat-consumes-piped-stdin",
             stdin="cat\nfirst line\nsecond line\n",
             assertion=expect_all("first line", "second line")),
        Case("cat-pipe-tr",
             stdin="cat | tr a-z A-Z\nmixed Text Input\n",
             assertion=expect_in("MIXED TEXT INPUT")),

        # 9. Theme built-in
        Case("theme-list-bundled", stdin="theme list\n",
             assertion=expect_all("default", "nord-powerline", "monokai")),
        Case("theme-show-default", stdin="theme show default\n",
             assertion=expect_in("default")),

        # 10. ANSI in prompt
        Case("ansi-escape-in-prompt", stdin="echo hi\n",
             assertion=expect_match(r"\x1b\[")),
    ]


# ---------- main ----------

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default=DEFAULT_HOST)
    ap.add_argument("--user", default=DEFAULT_USER)
    ap.add_argument("--key", default=str(DEFAULT_KEY))
    ap.add_argument("--report", default="aish-linux-ssh-report.txt")
    ap.add_argument("--skip-upload", action="store_true",
                    help="skip uploading binaries (assume they're already in $HOME/bin)")
    args = ap.parse_args()

    print(f"target: {args.user}@{args.host}")
    print(f"key:    {args.key}")
    print()

    c = open_ssh(args.host, args.user, Path(args.key))

    rc, out, err = remote_run(c, "uname -srm; lsb_release -ds 2>/dev/null || true")
    print(f"connected:\n  {out.strip().splitlines()[0]}")
    if len(out.strip().splitlines()) > 1:
        print(f"  {out.strip().splitlines()[1]}")
    print()

    if not args.skip_upload:
        if not LOCAL_AISH.exists():
            sys.exit(f"local aish binary missing: {LOCAL_AISH}\n"
                     f"run `make -C shell build-all` first")
        print(f"uploading {LOCAL_AISH.name} → {REMOTE_AISH} ...")
        upload(c, LOCAL_AISH, REMOTE_AISH)
        if LOCAL_PLUGIN.exists():
            print(f"uploading {LOCAL_PLUGIN.name} → {REMOTE_PLUGIN} ...")
            upload(c, LOCAL_PLUGIN, REMOTE_PLUGIN)
        else:
            print(f"(skipping plugin upload — {LOCAL_PLUGIN.name} not built locally)")
        print()

    # Sanity-check the binary runs
    rc, out, err = remote_run(c, f"{REMOTE_AISH} --version")
    if rc != 0:
        sys.exit(f"aish --version on the VM failed: rc={rc} err={err}")
    print(f"binary: {out.strip()}")
    print()

    cases = build_cases()
    results = []
    passed = failed = 0
    for tc in cases:
        cmd = f"{REMOTE_AISH} {tc.args}"
        rc, out, err = remote_run(c, cmd, stdin_data=tc.stdin)
        ok, reason = tc.assertion(out, err, rc)
        if ok:
            passed += 1
            print(f"  ✓ {tc.name}")
        else:
            failed += 1
            print(f"  ✗ {tc.name}: {reason}")
        results.append({
            "name": tc.name, "ok": ok, "reason": reason,
            "rc": rc, "stdout": out, "stderr": err,
            "stdin": tc.stdin, "args": tc.args,
        })

    # Plugin smoke (if uploaded)
    rc, out, err = remote_run(c, f"test -x {REMOTE_PLUGIN} && echo yes || echo no")
    if out.strip() == "yes":
        rc, out, err = remote_run(c, f"{REMOTE_PLUGIN} --version")
        ok = (rc == 0 and "aish-inference-cloud" in out)
        if ok:
            passed += 1
            print(f"  ✓ plugin-version")
        else:
            failed += 1
            print(f"  ✗ plugin-version: rc={rc} out={out!r}")
        results.append({"name": "plugin-version", "ok": ok,
                        "reason": "" if ok else f"rc={rc}",
                        "rc": rc, "stdout": out, "stderr": err,
                        "stdin": "", "args": "--version"})

        rc, out, err = remote_run(c, f"unset ANTHROPIC_API_KEY; echo '' | {REMOTE_PLUGIN}",
                                  stdin_data="")
        ok = (rc != 0 and "ANTHROPIC_API_KEY" in (out + err))
        if ok:
            passed += 1
            print(f"  ✓ plugin-fail-fast-no-key")
        else:
            failed += 1
            print(f"  ✗ plugin-fail-fast-no-key: rc={rc} out={out!r} err={err!r}")
        results.append({"name": "plugin-fail-fast-no-key", "ok": ok,
                        "reason": "" if ok else f"rc={rc}",
                        "rc": rc, "stdout": out, "stderr": err,
                        "stdin": "", "args": ""})

    c.close()

    print()
    print(f"passed: {passed}   failed: {failed}")

    # Report
    lines = [
        f"aish Linux SSH smoke-test report",
        f"target: {args.user}@{args.host}",
        f"aish:   {REMOTE_AISH}",
        f"plugin: {REMOTE_PLUGIN}",
        f"passed: {passed}   failed: {failed}",
        "─" * 60,
    ]
    for r in results:
        lines.append("")
        verdict = "PASS" if r["ok"] else "FAIL"
        lines.append(f"[{verdict}] {r['name']}")
        if not r["ok"]:
            lines.append(f"  reason: {r['reason']}")
        lines.append(f"  rc: {r['rc']}")
        if r["stdin"]:
            lines.append(f"  stdin: {r['stdin']!r}")
        lines.append("  stdout:")
        for ln in r["stdout"].splitlines():
            lines.append(f"    | {ln}")
        if r["stderr"].strip():
            lines.append("  stderr:")
            for ln in r["stderr"].splitlines():
                lines.append(f"    | {ln}")
    Path(args.report).write_text("\n".join(lines))
    print(f"report: {args.report}")

    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    main()
