<#
.SYNOPSIS
    Smoke-check aish.exe under Windows Terminal.

.DESCRIPTION
    Verifies that aish.exe runs in a Windows Terminal session: detects
    $env:WT_SESSION (Windows Terminal sets this), reads $Host.UI.RawUI.
    WindowSize, invokes `aish.exe --version`, and emits a TAP-style
    report to stdout. Exit 0 on full pass; >=1 if any sub-check fails.

    v1.0-1 documentary use: runs locally on developer machines under
    Windows Terminal. The release workflow's `windows-smoke` job is
    gated behind AISH_WINDOWS_RUNNER_ENABLED — flipping the gate lights
    the script up in CI without a re-write.

.PARAMETER BinaryPath
    Path to the aish.exe to smoke-test. Required.
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$BinaryPath
)

$ErrorActionPreference = 'Stop'

$totalTests = 0
$failed     = 0

function Test-Assert {
    param([string]$Name, [scriptblock]$Check)
    $script:totalTests++
    try {
        $result = & $Check
        if ($result) {
            Write-Output "ok $script:totalTests - $Name"
        } else {
            Write-Output "not ok $script:totalTests - $Name"
            $script:failed++
        }
    } catch {
        Write-Output "not ok $script:totalTests - $Name # ERROR: $_"
        $script:failed++
    }
}

Write-Output "TAP version 13"
Write-Output "# Windows Terminal compatibility smoke for aish"

Test-Assert -Name "aish.exe exists at $BinaryPath" -Check {
    Test-Path -Path $BinaryPath
}

Test-Assert -Name "running under Windows Terminal (WT_SESSION set)" -Check {
    -not [string]::IsNullOrWhiteSpace($env:WT_SESSION)
}

Test-Assert -Name "PowerShell version >= 5.1" -Check {
    $PSVersionTable.PSVersion -ge [version]'5.1'
}

Test-Assert -Name "Host RawUI WindowSize resolves" -Check {
    $size = $Host.UI.RawUI.WindowSize
    $null -ne $size -and $size.Width -gt 0 -and $size.Height -gt 0
}

Test-Assert -Name "aish.exe --version returns non-empty stdout" -Check {
    $out = & $BinaryPath '--version' 2>$null
    -not [string]::IsNullOrWhiteSpace($out)
}

Test-Assert -Name "aish.exe exit code is 0 for --version" -Check {
    & $BinaryPath '--version' 2>$null | Out-Null
    $LASTEXITCODE -eq 0
}

Write-Output "1..$totalTests"
Write-Output "# tests $totalTests"
Write-Output "# pass  $($totalTests - $failed)"
Write-Output "# fail  $failed"

exit $failed
