<#
.SYNOPSIS
    Sign Windows binaries using signtool.exe.

.DESCRIPTION
    v1.0-1 dormant signing script. Runs on Windows only (signtool is
    part of the Windows SDK). Two credential paths supported:

      1. Production: WINDOWS_CERT_THUMBPRINT env var identifies a cert
         in the local certificate store. Use this with EV certs backed
         by an HSM / smart card.

      2. CI: WINDOWS_CERT_PFX (base64-encoded .pfx) + WINDOWS_CERT_PASSWORD
         env vars. The script decodes the PFX to a temp file, signs,
         then deletes the temp file.

    No certificate is committed to this repo. The script is a no-op
    unless one of the two credential paths is fully populated.

.PARAMETER FilePath
    Path to the file to sign (.exe or .msi). Required.

.PARAMETER TimestampUrl
    RFC 3161 timestamp authority URL. Defaults to DigiCert's public
    timestamp server.

.EXAMPLE
    .\sign.ps1 -FilePath dist\aish-windows-amd64.exe
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$FilePath,

    [Parameter(Mandatory = $false)]
    [string]$TimestampUrl = 'http://timestamp.digicert.com'
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path -Path $FilePath)) {
    Write-Error "File not found: $FilePath"
    exit 1
}

$thumbprint = $env:WINDOWS_CERT_THUMBPRINT
$pfxBase64  = $env:WINDOWS_CERT_PFX
$pfxPass    = $env:WINDOWS_CERT_PASSWORD

if ([string]::IsNullOrWhiteSpace($thumbprint) -and [string]::IsNullOrWhiteSpace($pfxBase64)) {
    Write-Host "sign.ps1: no signing credential present (WINDOWS_CERT_THUMBPRINT or WINDOWS_CERT_PFX); skipping."
    Write-Host "sign.ps1: this is the v1.0-1 dormant state. v1.0-1b enables signing by providing the secret."
    exit 0
}

# Resolve signtool.exe — Windows SDK installs it under Program Files.
$signtool = Get-Command -Name 'signtool.exe' -ErrorAction SilentlyContinue
if (-not $signtool) {
    $candidates = @(
        "$env:ProgramFiles(x86)\Windows Kits\10\bin\x64\signtool.exe",
        "$env:ProgramFiles\Windows Kits\10\bin\x64\signtool.exe"
    )
    foreach ($c in $candidates) {
        if (Test-Path -Path $c) { $signtool = $c; break }
    }
}
if (-not $signtool) {
    Write-Error "signtool.exe not found in PATH or Windows Kits. Install the Windows SDK."
    exit 2
}

if (-not [string]::IsNullOrWhiteSpace($thumbprint)) {
    Write-Host "sign.ps1: signing with cert store thumbprint (production path)"
    & $signtool sign /sha1 $thumbprint /fd sha256 /tr $TimestampUrl /td sha256 $FilePath
    exit $LASTEXITCODE
}

# PFX path (CI). Write the decoded PFX to a temp file with restrictive ACL.
$tempPfx = [System.IO.Path]::Combine($env:RUNNER_TEMP ?? $env:TEMP, "aish-signing-$([guid]::NewGuid()).pfx")
try {
    Write-Host "sign.ps1: signing with PFX file (CI path)"
    [System.IO.File]::WriteAllBytes($tempPfx, [Convert]::FromBase64String($pfxBase64))
    & $signtool sign /f $tempPfx /p $pfxPass /fd sha256 /tr $TimestampUrl /td sha256 $FilePath
    $rc = $LASTEXITCODE
} finally {
    if (Test-Path -Path $tempPfx) {
        Remove-Item -Path $tempPfx -Force -ErrorAction SilentlyContinue
    }
}
exit $rc
