#!/usr/bin/env python3
"""
WinRM-driven aish smoke tests for the UTM Windows VM.

Reads WINDOWS_USERNAME / WINDOWS_PASSWORD from ~/.env/utm/.env (per
Common.md §4 — the value never enters this script's stdout/stderr; if
it ever appears in remote output it is redacted to
`[REDACTED:utm-password]` before being printed).

Usage (Mac side, after WinRM is enabled in the VM):

    python3 .artifacts/spawn/test-windows-winrm.py \
        --host 192.168.64.5 \
        --aish 'C:\\Users\\itsfwcp\\Desktop\\aish-windows-arm64.exe'

Optional:
    --port 5985        (default; use 5986 for HTTPS)
    --transport ntlm   (default; or kerberos, basic, ssl)
    --report PATH      (default: ./aish-windows-winrm-report.txt)
"""
from __future__ import annotations

import argparse
import os
import re
import sys
from pathlib import Path
from typing import Callable

try:
    import winrm  # pywinrm
except ImportError:
    sys.exit("pywinrm not installed. Run with the venv: "
             ".artifacts/spawn/winrm-venv/bin/python")

ENV_FILE = Path.home() / ".env" / "utm" / ".env"

# ---------- secret handling ----------

def load_credentials() -> tuple[str, str]:
    """Return (username, password) from the env file. The password never
    appears in this script's output; if you need to verify presence,
    use `len(password) > 0`."""
    if not ENV_FILE.exists():
        sys.exit(f"missing credentials file: {ENV_FILE}")
    creds: dict[str, str] = {}
    for line in ENV_FILE.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        # Strip optional surrounding quotes.
        v = v.strip()
        if (v.startswith('"') and v.endswith('"')) or \
           (v.startswith("'") and v.endswith("'")):
            v = v[1:-1]
        creds[k.strip()] = v
    user = creds.get("WINDOWS_USERNAME")
    pw = creds.get("WINDOWS_PASSWORD")
    if not user or not pw:
        sys.exit("WINDOWS_USERNAME or WINDOWS_PASSWORD missing in env file")
    return user, pw


def redact(text: str, password: str) -> str:
    """Replace any literal occurrence of the password in `text` with
    [REDACTED:utm-password]. Defensive — pywinrm shouldn't surface the
    password in normal output, but Common.md §4 demands the safety net."""
    if password and password in text:
        return text.replace(password, "[REDACTED:utm-password]")
    return text


# ---------- test framework ----------

class TestCase:
    def __init__(self, name: str, script: str,
                 assertion: Callable[[str, str, int], tuple[bool, str]]):
        self.name = name
        self.script = script
        self.assertion = assertion


def make_aish_script(aish_path: str, stdin_text: str, args: str = "") -> str:
    """Build the PowerShell snippet that pipes stdin_text into aish.exe
    and emits stdout. stdin_text uses literal `\n` for newlines.

    Uses a PowerShell SINGLE-QUOTED HERE-STRING (`@'…'@`) which preserves
    every character literally — no $ expansion, no backtick escapes, no
    quote doubling. The only forbidden sequence is a line beginning with
    `'@`, which terminates the here-string; none of our test inputs hit
    that. Do NOT pre-double single quotes for a here-string — that's
    inline-string escape syntax and would turn `'literal $X'` into
    `''literal $X''`, which aish reads as two empty single-quote regions
    around an unquoted `literal $X` and expands `$X`. (Found the hard
    way while wiring this suite up.)
    """
    if any(line.startswith("'@") for line in stdin_text.split("\n")):
        raise ValueError("stdin_text contains a line beginning with `'@` "
                         "which terminates the PowerShell here-string")
    return (
        f"$ErrorActionPreference = 'Continue';\n"
        f"$in = @'\n{stdin_text}\n'@;\n"
        f"$in | & '{aish_path}' {args};\n"
        f"if ($LASTEXITCODE -eq $null) {{ $LASTEXITCODE = 0 }};\n"
        f"exit $LASTEXITCODE\n"
    )


def expect_in(needle: str):
    def check(out: str, err: str, code: int) -> tuple[bool, str]:
        if needle in out:
            return True, ""
        return False, f"{needle!r} not in stdout"
    return check


def expect_all(*needles: str):
    def check(out: str, err: str, code: int) -> tuple[bool, str]:
        missing = [n for n in needles if n not in out]
        if not missing:
            return True, ""
        return False, f"missing in stdout: {missing}"
    return check


def expect_match(pattern: str):
    rx = re.compile(pattern)
    def check(out: str, err: str, code: int) -> tuple[bool, str]:
        if rx.search(out):
            return True, ""
        return False, f"pattern /{pattern}/ not in stdout"
    return check


def expect_not_in(unwanted: str):
    def check(out: str, err: str, code: int) -> tuple[bool, str]:
        if unwanted in out:
            return False, f"{unwanted!r} unexpectedly in stdout"
        return True, ""
    return check


# ---------- the test suite ----------

def build_tests(aish: str) -> list[TestCase]:
    return [
        TestCase("version-flag",
                 f"& '{aish}' --version; exit $LASTEXITCODE",
                 expect_all("aish ", "built")),
        TestCase("help-flag",
                 f"& '{aish}' --help; exit $LASTEXITCODE",
                 expect_in("Usage")),
        TestCase("empty-stdin-clean-exit",
                 make_aish_script(aish, ""),
                 lambda o, e, c: (c == 0, f"exit={c}")),
        TestCase("external-where",
                 make_aish_script(aish, "where where"),
                 expect_in("where.exe")),
        TestCase("external-cmd-echo",
                 make_aish_script(aish, "cmd /c echo hi"),
                 expect_in("hi")),
        TestCase("pipe-cmd-findstr",
                 make_aish_script(aish, "cmd /c echo hello world | findstr hello"),
                 expect_in("hello")),
        TestCase("export-and-var-inherits-to-child",
                 make_aish_script(aish, "export GREETING=childvalue\ncmd /c echo %GREETING%"),
                 expect_in("childvalue")),
        TestCase("cd-absolute",
                 make_aish_script(aish, "cd C:\\Windows\ncmd /c cd"),
                 expect_match(r"C:\\Windows")),
        # NOTE: bare `echo` isn't on Windows PATH (cmd.exe builtin, not a
        # .exe). Tests echo through `cmd /c echo` so the OS resolves a real
        # binary — aish's parsing of $? / $X is exercised before cmd ever
        # sees the args.
        TestCase("exit-code-via-question",
                 make_aish_script(aish, "cmd /c exit 42\ncmd /c echo exit=$?"),
                 expect_in("exit=42")),
        TestCase("single-quote-suppresses-expansion",
                 make_aish_script(aish, "export X=expanded\ncmd /c echo 'literal $X'"),
                 expect_in("literal $X")),
        TestCase("double-quote-expands",
                 make_aish_script(aish, "export X=expanded\ncmd /c echo \"value $X\""),
                 expect_in("value expanded")),
        TestCase("theme-list-bundled",
                 make_aish_script(aish, "theme list"),
                 expect_all("default", "nord-powerline", "monokai")),
        TestCase("theme-set-and-persist",
                 make_aish_script(aish,
                     "theme set nord-powerline\n"
                     "type $env:USERPROFILE\\.aish\\config.toml"),
                 # Won't quite work — `type` is cmd, but we're in aish.
                 # Better: use cmd /c type
                 expect_in("active = ")),
        TestCase("theme-set-via-cmd-type-check",
                 # Set theme, then read the persisted config via cmd
                 make_aish_script(aish,
                     "theme set nord-powerline\n"
                     "cmd /c type %USERPROFILE%\\.aish\\config.toml"),
                 expect_in("nord-powerline")),
        TestCase("theme-persistence-survives-restart",
                 make_aish_script(aish, "theme list"),
                 expect_in("* nord-powerline")),
        TestCase("ansi-escape-in-prompt",
                 make_aish_script(aish, "echo done"),
                 expect_match(r"\x1b\[")),
        # Reset theme at end so the host stays clean
        TestCase("reset-default-theme",
                 make_aish_script(aish, "theme set default"),
                 expect_in("active = default")),
        # Issue #167 — cat-like stdin reading.
        # Most Windows hosts don't have `cat`; use `more` which IS on PATH.
        # `more` reads stdin if not given a file arg.
        TestCase("more-reads-stdin",
                 make_aish_script(aish, "more\nthis is line one\nthis is line two"),
                 expect_all("line one", "line two")),
    ]


# ---------- main ----------

def main():
    p = argparse.ArgumentParser(description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--host", required=True,
                   help="Windows VM IP/hostname (e.g. 192.168.64.5)")
    p.add_argument("--port", type=int, default=5985,
                   help="WinRM port (default 5985 HTTP; 5986 HTTPS)")
    p.add_argument("--transport", default="ntlm",
                   choices=["ntlm", "kerberos", "basic", "ssl"],
                   help="WinRM auth transport (default ntlm)")
    p.add_argument("--aish", default=r"C:\Users\itsfwcp\Desktop\aish-windows-arm64.exe",
                   help="Path to aish.exe in the VM")
    p.add_argument("--report", default="aish-windows-winrm-report.txt",
                   help="Report output path (Mac side)")
    args = p.parse_args()

    user, pw = load_credentials()
    scheme = "https" if args.port == 5986 else "http"
    endpoint = f"{scheme}://{args.host}:{args.port}/wsman"

    print(f"target:    {endpoint}")
    print(f"username:  {user}")
    print(f"transport: {args.transport}")
    print(f"aish.exe:  {args.aish}")
    print()

    session = winrm.Session(endpoint, auth=(user, pw),
                            transport=args.transport,
                            server_cert_validation="ignore")

    # Sanity check.
    try:
        r = session.run_cmd("hostname")
        if r.status_code != 0:
            sys.exit(f"hostname check failed: rc={r.status_code} "
                     f"stderr={redact(r.std_err.decode(errors='replace'), pw)}")
        print(f"connected: {r.std_out.decode(errors='replace').strip()}")
    except Exception as e:
        sys.exit(f"WinRM connection failed: {redact(str(e), pw)}\n"
                 f"Check that the VM has WinRM enabled and reachable on "
                 f"{args.host}:{args.port}.")

    # Verify the binary is present.
    check = session.run_ps(f"Test-Path '{args.aish}'")
    if check.std_out.decode(errors='replace').strip() != "True":
        sys.exit(f"aish.exe not found at {args.aish} in the VM.\n"
                 f"Copy it from your Mac's shell/dist/ folder via the "
                 f"UTM shared folder or another mechanism, then re-run.")

    tests = build_tests(args.aish)
    results = []
    passed = failed = 0

    for tc in tests:
        try:
            r = session.run_ps(tc.script)
            out = redact(r.std_out.decode(errors='replace'), pw)
            err = redact(r.std_err.decode(errors='replace'), pw)
            ok, reason = tc.assertion(out, err, r.status_code)
        except Exception as e:
            ok, reason = False, f"exception: {redact(str(e), pw)}"
            out, err = "", ""
        if ok:
            passed += 1
            print(f"  ✓ {tc.name}")
        else:
            failed += 1
            print(f"  ✗ {tc.name}: {reason}")
        results.append({
            "name": tc.name, "ok": ok, "reason": reason,
            "exit": r.status_code if 'r' in dir() else -1,
            "stdout": out, "stderr": err,
        })

    print()
    print(f"passed: {passed}   failed: {failed}")

    # Write report.
    rep = Path(args.report)
    lines = [
        "aish WinRM smoke-test report",
        f"target: {endpoint}",
        f"aish.exe: {args.aish}",
        f"passed: {passed}   failed: {failed}",
        "─" * 60,
    ]
    for r in results:
        lines.append("")
        verdict = "PASS" if r["ok"] else "FAIL"
        lines.append(f"[{verdict}] {r['name']}")
        if not r["ok"]:
            lines.append(f"  reason: {r['reason']}")
        lines.append(f"  exit:   {r['exit']}")
        lines.append("  stdout:")
        for ln in r["stdout"].splitlines():
            lines.append(f"    | {ln}")
        if r["stderr"].strip():
            lines.append("  stderr:")
            for ln in r["stderr"].splitlines():
                lines.append(f"    | {ln}")
    rep.write_text("\n".join(lines))
    print(f"report:  {rep}")

    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    main()
