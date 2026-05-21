<#
.SYNOPSIS
    Register aish.exe as a user or machine default shell on Windows.

.DESCRIPTION
    v1.0-5 Windows Login Shell registration helper. POSIX users
    register a shell via /etc/shells + chsh; on Windows the
    equivalent is a registry write under either HKCU (per-user)
    or HKLM (machine-wide, Winlogon Shell key).

    The script is idempotent: re-running with the same arguments
    is a no-op. -Unregister reverses the change. -WhatIf shows
    what would happen without writing.

    A dated entry is appended to
    %LOCALAPPDATA%\aish\install.log for every state-changing
    invocation, satisfying the v1.0-5 plan's audit requirement.

.PARAMETER Scope
    User (default) writes to HKCU:\Software\aish.
    Machine writes to HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\Shell
    — the classic "user shell" replacement. Machine scope
    REQUIRES -Force because a broken aish.exe at next login
    can lock the user out.

.PARAMETER AishPath
    Path to aish.exe. Defaults to "aish.exe" (resolved on
    $env:PATH). Validated for existence before any write.

.PARAMETER Unregister
    Reverse a previous registration. Safe to run when no
    registration exists (no-op).

.PARAMETER Force
    Required for -Scope Machine. Acknowledges the risk of
    locking the user out if aish.exe is broken at next login.

.PARAMETER WhatIf
    Standard PowerShell dry-run switch. Prints what would be
    written without touching the registry.

.EXAMPLE
    .\register-shell.ps1 -AishPath C:\Tools\aish.exe
    Registers aish at user scope using the supplied path.

.EXAMPLE
    .\register-shell.ps1 -Unregister
    Removes any prior user-scope registration.

.EXAMPLE
    .\register-shell.ps1 -Scope Machine -AishPath C:\Tools\aish.exe -Force
    Replaces explorer.exe as the user shell at next login.
    Requires an elevated PowerShell session and -Force.

.NOTES
    Source: github.com/convergent-systems-co/aish (v1.0-5).
    Plan:   .artifacts/plans/v1.0-5.md
    See §2 alternatives table for why this is a PowerShell
    script and not a Go subcommand.

    TODO(#151): once TL_WIN_OS lands ConPTY attachment, this
    script can write a Windows Terminal profile fragment so
    aish appears in the Terminal launcher. Out of scope today.

    TODO(#152, packaging): when TL_WIN_BUILD ships an MSI, this
    script becomes the install-step floor and the MSI becomes
    the supported install path. The script remains for
    portable / dev installs.
#>
[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [ValidateSet('User', 'Machine')]
    [string]$Scope = 'User',

    [string]$AishPath = 'aish.exe',

    [switch]$Unregister,

    [switch]$Force
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# --- Constants -------------------------------------------------------------

$UserKey    = 'HKCU:\Software\aish'
$MachineKey = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
$MachineValueName = 'Shell'

# --- Helpers ---------------------------------------------------------------

function Get-InstallLogPath {
    $logDir = Join-Path $env:LOCALAPPDATA 'aish'
    if (-not (Test-Path $logDir)) {
        New-Item -ItemType Directory -Path $logDir -Force | Out-Null
    }
    Join-Path $logDir 'install.log'
}

function Write-InstallLog {
    param([string]$Message)
    $logPath = Get-InstallLogPath
    $stamp = (Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ')
    Add-Content -Path $logPath -Value "[$stamp] $Message"
}

function Resolve-AishBinary {
    param([string]$Candidate)
    # If the caller passed a literal path that exists, use it.
    if (Test-Path -LiteralPath $Candidate -PathType Leaf) {
        return (Resolve-Path -LiteralPath $Candidate).Path
    }
    # Otherwise try $env:PATH lookup.
    $resolved = Get-Command $Candidate -ErrorAction SilentlyContinue
    if ($resolved -and $resolved.Source) {
        return $resolved.Source
    }
    throw "aish.exe not found at '$Candidate' or on PATH. Pass -AishPath C:\path\to\aish.exe."
}

function Test-Elevated {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($id)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

# --- Register / Unregister at User scope -----------------------------------

function Register-UserScope {
    param([string]$BinaryPath)
    if (-not (Test-Path $UserKey)) {
        if ($PSCmdlet.ShouldProcess($UserKey, 'Create registry key')) {
            New-Item -Path $UserKey -Force | Out-Null
        }
    }
    $existing = (Get-ItemProperty -Path $UserKey -Name 'Path' -ErrorAction SilentlyContinue).Path
    if ($existing -eq $BinaryPath) {
        Write-Host "aish: user-scope registration already current ($BinaryPath); no change." -ForegroundColor Yellow
        return $false
    }
    if ($PSCmdlet.ShouldProcess($UserKey, "Set Path = $BinaryPath")) {
        Set-ItemProperty -Path $UserKey -Name 'Path' -Value $BinaryPath -Type String
        Write-InstallLog "register user-scope: $BinaryPath"
        Write-Host "aish: registered at user scope ($UserKey\Path = $BinaryPath)." -ForegroundColor Green
        return $true
    }
    return $false
}

function Unregister-UserScope {
    if (-not (Test-Path $UserKey)) {
        Write-Host "aish: no user-scope registration to remove." -ForegroundColor Yellow
        return $false
    }
    if ($PSCmdlet.ShouldProcess($UserKey, 'Remove registry key')) {
        Remove-Item -Path $UserKey -Recurse -Force
        Write-InstallLog 'unregister user-scope'
        Write-Host "aish: removed user-scope registration." -ForegroundColor Green
        return $true
    }
    return $false
}

# --- Register / Unregister at Machine scope --------------------------------

function Register-MachineScope {
    param([string]$BinaryPath)
    if (-not (Test-Elevated)) {
        throw "Machine scope requires an elevated PowerShell session. Re-run as Administrator."
    }
    if (-not $Force) {
        throw "Machine scope replaces the user shell at next login. Re-run with -Force to acknowledge the risk."
    }
    $existing = (Get-ItemProperty -Path $MachineKey -Name $MachineValueName -ErrorAction SilentlyContinue).$MachineValueName
    if ($existing -eq $BinaryPath) {
        Write-Host "aish: machine-scope registration already current; no change." -ForegroundColor Yellow
        return $false
    }
    if ($existing) {
        Write-InstallLog "machine-scope previous Shell value: $existing"
    }
    if ($PSCmdlet.ShouldProcess($MachineKey, "Set Shell = $BinaryPath")) {
        Set-ItemProperty -Path $MachineKey -Name $MachineValueName -Value $BinaryPath -Type String
        Write-InstallLog "register machine-scope: $BinaryPath"
        Write-Host "aish: registered at machine scope. Will activate at next login." -ForegroundColor Green
        Write-Host "aish: rollback with -Unregister -Scope Machine -Force." -ForegroundColor Yellow
        return $true
    }
    return $false
}

function Unregister-MachineScope {
    if (-not (Test-Elevated)) {
        throw "Machine scope requires an elevated PowerShell session. Re-run as Administrator."
    }
    if (-not $Force) {
        throw "Machine-scope -Unregister requires -Force. Re-run with -Force."
    }
    $existing = (Get-ItemProperty -Path $MachineKey -Name $MachineValueName -ErrorAction SilentlyContinue).$MachineValueName
    if (-not $existing) {
        Write-Host "aish: no machine-scope registration to remove." -ForegroundColor Yellow
        return $false
    }
    if ($PSCmdlet.ShouldProcess($MachineKey, "Reset Shell to explorer.exe")) {
        # Restore Windows' default rather than removing the value
        # entirely — leaving the key empty is more dangerous than
        # leaving it pointing at explorer.exe.
        Set-ItemProperty -Path $MachineKey -Name $MachineValueName -Value 'explorer.exe' -Type String
        Write-InstallLog 'unregister machine-scope: reset to explorer.exe'
        Write-Host "aish: machine-scope reset to explorer.exe." -ForegroundColor Green
        return $true
    }
    return $false
}

# --- Main ------------------------------------------------------------------

try {
    if ($Unregister) {
        switch ($Scope) {
            'User'    { [void](Unregister-UserScope) }
            'Machine' { [void](Unregister-MachineScope) }
        }
    } else {
        $resolved = Resolve-AishBinary -Candidate $AishPath
        Write-Host "aish: resolved binary -> $resolved"
        switch ($Scope) {
            'User'    { [void](Register-UserScope -BinaryPath $resolved) }
            'Machine' { [void](Register-MachineScope -BinaryPath $resolved) }
        }
    }
}
catch {
    Write-Error "aish: $($_.Exception.Message)"
    exit 1
}
