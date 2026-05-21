# scripts/win/ — Windows release tooling

Operator-facing scripts that the v1.0-1 Windows release pipeline calls.
Most are dormant in v1.0-1: the wiring is in place; the run-time gates
are off pending procurement (cert) or Windows-runner cost approval.

| Script | Status | Run-time host | Triggers |
|---|---|---|---|
| `sign.ps1` | Dormant. Active when `WINDOWS_CERT_PFX` or `WINDOWS_CERT_THUMBPRINT` is present. | Windows | release-windows.yml |
| `build-msi.ps1` | Dormant. Active when `vars.AISH_WINDOWS_RUNNER_ENABLED == 'true'`. | Windows | release-windows.yml (`windows-msi` job) |
| `wt-compat-check.ps1` | Dormant in CI; runnable today on a developer's Windows Terminal. | Windows | release-windows.yml (`windows-smoke` job) |
| `hydrate-winget.sh` | Active. Used at release-time to populate the winget manifest's SHA256 placeholders. | macOS/Linux | release-windows.yml (release step) |
| `test/Profile-Test.ps1` | Active. Pester fixture for the user-facing PowerShell profile snippet. | Any (PowerShell 7+) | manual; CI when pwsh step lands |

## Local invocation

### PowerShell profile fixture

```sh
# Install Pester once:
pwsh -c 'Install-Module Pester -Scope CurrentUser -Force'

# Run the fixture:
pwsh scripts/win/test/Profile-Test.ps1
```

### winget manifest hydration

```sh
scripts/win/hydrate-winget.sh \
    --version 1.0.0 \
    --dist dist/ \
    --out dist/winget/aish.yaml
```

The committed template at `data/install/windows/winget/aish.yaml` carries
`@@VERSION@@`, `@@AMD64_SHA256@@`, and `@@ARM64_SHA256@@` placeholders.
Hydration produces the file `wingetcreate submit` consumes.

### Local Windows Terminal smoke

```pwsh
# In a Windows Terminal session on Windows:
./scripts/win/wt-compat-check.ps1 -BinaryPath dist/aish-windows-amd64.exe
```

Output is TAP version 13. Exit 0 on full pass.

## Signing path (when v1.0-1b activates)

Two credential sources, in order of preference:

1. **Production (EV + HSM):** set `WINDOWS_CERT_THUMBPRINT` in the
   workflow's environment from a repo secret. The cert lives in the
   local certificate store on a hardware-token-equipped runner.
2. **CI (PFX file):** base64-encode the `.pfx` to
   `secrets.WINDOWS_CERT_PFX` and the password to
   `secrets.WINDOWS_CERT_PASSWORD`. `sign.ps1` decodes the PFX to a
   per-run temp file and deletes it after signing.

Per `Common.md §4`: the password is never logged, never echoed, and
the temp PFX is best-effort removed via `try/finally`.

## MSI build (when v1.0-1b activates)

Requires Windows + WiX 4:

```pwsh
dotnet tool install --global wix
./scripts/win/build-msi.ps1 -BinaryPath dist\aish-windows-amd64.exe -Arch amd64
```

WiX source lives at `data/install/windows/wix/aish.wxs`. Product GUID
is the v1.0-1b regeneration target — the stub uses a placeholder GUID
documented in the wxs file.
