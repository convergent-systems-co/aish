# Installing aish on Windows

> **Audience:** Windows user installing aish from a v1.0+ release.
> **Status:** v1.0-1 ships portable `.exe` binaries with SHA256
> integrity manifests. winget publication and code-signed MSI installer
> arrive in v1.0-1b.

aish runs natively on Windows 10 (1809+) and Windows 11, on both
amd64 and arm64. It is a single statically-linked Go binary — no
.NET, no WSL, no Visual C++ runtime.

---

## Quick install (PowerShell, portable)

```powershell
# 1. Pick your architecture:
$arch = if ([System.Environment]::Is64BitOperatingSystem -and
            [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') {
    'arm64'
} else {
    'amd64'
}

# 2. Download the binary and its SHA256 sidecar to %LocalAppData%\Programs\aish:
$installDir = "$env:LocalAppData\Programs\aish"
New-Item -Path $installDir -ItemType Directory -Force | Out-Null
$base = 'https://github.com/convergent-systems-co/aish/releases/latest/download'
Invoke-WebRequest "$base/aish-windows-$arch.exe"        -OutFile "$installDir\aish.exe"
Invoke-WebRequest "$base/aish-windows-$arch.exe.sha256" -OutFile "$installDir\aish.exe.sha256"

# 3. Verify integrity:
$expected = (Get-Content "$installDir\aish.exe.sha256").Split(' ')[0]
$actual   = (Get-FileHash -Algorithm SHA256 "$installDir\aish.exe").Hash.ToLower()
if ($expected -ne $actual) { throw "SHA256 mismatch"; } else { 'integrity OK' }

# 4. Add to PATH (per-user, persistent):
[Environment]::SetEnvironmentVariable(
    'Path',
    [Environment]::GetEnvironmentVariable('Path', 'User') + ";$installDir",
    'User')

# 5. Smoke-test (open a NEW PowerShell — PATH changes do not propagate
#    to the current session):
aish --version
```

---

## Windows Terminal profile

Drop a dedicated tab into Windows Terminal. Open `Settings -> Open JSON file`
(Ctrl+Shift+,), find `"profiles" -> "list"`, and append the object
from [wt/profile.json](wt/profile.json). Remove the leading `_comment`
and `_install_notes` fields — they exist for documentation only and
will trip Windows Terminal's schema validator.

To make aish the default profile, copy the `guid` from the appended
object into the top-level `"defaultProfile"` field.

---

## PowerShell integration (hybrid users)

Append the contents of
[powershell/Profile.ps1](powershell/Profile.ps1) to your PowerShell
profile (`$PROFILE`):

```powershell
notepad $PROFILE   # creates the file if missing
```

The snippet adds:

- `aish` — pass-through invocation. `cat file.txt | aish "summarize"`
  feeds raw bytes to aish.
- `Invoke-Aish` — advanced function for object pipelines.
  `Get-Process | Invoke-Aish "which process is hot"` stringifies the
  pipeline before reaching aish.
- `ai` — alias for `aish`.

The block is delimited by `# aish-managed:` markers so a future
installer can update it in place without touching the rest of your
profile.

---

## winget (when available)

winget publication arrives in v1.0-1b. When live:

```powershell
winget install ConvergentSystems.aish
```

The manifest source-of-truth lives at [winget/aish.yaml](winget/aish.yaml).
Publication to `microsoft/winget-pkgs` is via the
[wingetcreate](https://learn.microsoft.com/en-us/windows/package-manager/package/) flow.

---

## MSI installer (when available)

The WiX 4 source lives at [wix/aish.wxs](wix/aish.wxs) and produces a
per-user MSI that installs to `%LocalAppData%\Programs\aish\` and
augments the per-user PATH. The build is dormant in v1.0-1 — see
`scripts/win/build-msi.ps1`. Lights up in v1.0-1b alongside code
signing.

---

## Verifying authenticity

**Today (v1.0-1):** SHA256 integrity only. Every binary ships with a
sidecar `<file>.exe.sha256` and a top-level
[`aish-windows-MANIFEST.txt`](../../../dist/aish-windows-MANIFEST.txt)
listing every released file with its size and hash. Compare against
the value PowerShell's `Get-FileHash` produces locally.

**v1.0-1b onward:** Authenticode signing via signtool. The signing
path is wired today; the certificate procurement is the deferred
blocker. Once live, `signtool verify /pa aish.exe` will report a
trusted chain to a public CA.

Windows SmartScreen may warn on first execution of an unsigned binary.
This is expected for v1.0-1. Click "More info" then "Run anyway" after
you have verified the SHA256.

---

## Uninstall

```powershell
# Portable install:
$installDir = "$env:LocalAppData\Programs\aish"
Remove-Item -Recurse -Force $installDir

# Remove from PATH (PowerShell):
$newPath = ([Environment]::GetEnvironmentVariable('Path', 'User') -split ';' |
            Where-Object { $_ -ne $installDir }) -join ';'
[Environment]::SetEnvironmentVariable('Path', $newPath, 'User')

# Remove the aish block from your $PROFILE:
#   Open notepad $PROFILE and delete everything between the
#   `# aish-managed: BEGIN` and `# aish-managed: END` markers.
```

---

## Reporting issues

File at https://github.com/convergent-systems-co/aish/issues with the
output of `aish --version` and your `$PSVersionTable` (PowerShell)
or `winver` (cmd) so we can reproduce.
