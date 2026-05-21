<#
.SYNOPSIS
    Build an MSI installer for aish using WiX 4.

.DESCRIPTION
    v1.0-1 dormant MSI builder. Runs on Windows only; requires the
    WiX 4 toolset (`dotnet tool install --global wix`). The release
    workflow gates the call behind the AISH_WINDOWS_RUNNER_ENABLED repo
    variable — this PR commits the structure but does not run it in CI.

.PARAMETER BinaryPath
    Path to the aish.exe to package. Required.

.PARAMETER Arch
    Architecture for the MSI: amd64 or arm64. Required.

.PARAMETER Version
    Product version (semver). Default: read from the binary's git
    describe via `aish.exe --version`.

.PARAMETER OutDir
    Output directory. Default: dist/.

.EXAMPLE
    .\build-msi.ps1 -BinaryPath dist\aish-windows-amd64.exe -Arch amd64
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$BinaryPath,
    [Parameter(Mandatory = $true)][ValidateSet('amd64','arm64')][string]$Arch,
    [Parameter(Mandatory = $false)][string]$Version,
    [Parameter(Mandatory = $false)][string]$OutDir = 'dist'
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path -Path $BinaryPath)) {
    Write-Error "Binary not found: $BinaryPath"
    exit 1
}

if (-not (Get-Command -Name 'wix' -ErrorAction SilentlyContinue)) {
    Write-Error "WiX 4 not installed. Run: dotnet tool install --global wix"
    exit 2
}

if ([string]::IsNullOrWhiteSpace($Version)) {
    $rawVersion = & $BinaryPath '--version' 2>$null
    # aish --version output format: "aish <version> (built <buildTime>)"
    if ($rawVersion -match 'aish\s+(\S+)') {
        $Version = $Matches[1] -replace '^v',''
    } else {
        $Version = '0.0.0'
    }
}

# WiX wants four-component versions (a.b.c.d).
if ($Version -notmatch '^\d+\.\d+\.\d+\.\d+$') {
    $parts = ($Version -split '[.\-+]')[0..2]
    while ($parts.Count -lt 3) { $parts += '0' }
    $Version = "$($parts[0]).$($parts[1]).$($parts[2]).0"
}

$repoRoot   = (Resolve-Path -Path "$PSScriptRoot\..\..").Path
$wxsSource  = Join-Path -Path $repoRoot -ChildPath 'data\install\windows\wix\aish.wxs'
$msiName    = "aish-windows-$Arch.msi"
$msiPath    = Join-Path -Path $OutDir -ChildPath $msiName

if (-not (Test-Path -Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir | Out-Null }

Write-Host "build-msi.ps1: building $msiName (version $Version, arch $Arch)"
$wixArch = if ($Arch -eq 'amd64') { 'x64' } else { 'arm64' }

& wix build `
    -arch $wixArch `
    -define "AishVersion=$Version" `
    -define "AishBinary=$BinaryPath" `
    -out $msiPath `
    $wxsSource

if ($LASTEXITCODE -ne 0) {
    Write-Error "wix build failed with exit code $LASTEXITCODE"
    exit $LASTEXITCODE
}

Write-Host "build-msi.ps1: wrote $msiPath"
exit 0
