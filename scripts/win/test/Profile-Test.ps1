<#
.SYNOPSIS
    Pester-style fixture for data/install/windows/powershell/Profile.ps1.

.DESCRIPTION
    Verifies that the user-facing PowerShell profile snippet:
      - parses without errors,
      - defines the `aish` function,
      - defines the `Invoke-Aish` advanced function,
      - sets the `ai` alias,
      - contains the `# aish-managed:` marker comment.

    Pester is the de-facto PowerShell test framework. Install once
    with: `Install-Module Pester -Scope CurrentUser -Force`. Then run:
      pwsh scripts/win/test/Profile-Test.ps1

    Exit code 0 on full pass; non-zero on any failure. Works under
    PowerShell 7+ on any platform (Linux/macOS/Windows) — Pester's
    abstractions are portable; the snippet under test is platform-
    agnostic until `aish.exe` is invoked.
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$ProfilePath = $(Join-Path -Path $PSScriptRoot -ChildPath '..\..\..\data\install\windows\powershell\Profile.ps1')
)

$ErrorActionPreference = 'Stop'

# Resolve the profile path before delegating to Pester so a missing
# file fails fast with a clear message.
if (-not (Test-Path -Path $ProfilePath)) {
    Write-Error "Profile.ps1 not found at: $ProfilePath"
    exit 1
}
$resolved = (Resolve-Path -Path $ProfilePath).Path

if (-not (Get-Module -ListAvailable -Name Pester)) {
    Write-Error "Pester not installed. Run: Install-Module Pester -Scope CurrentUser -Force"
    exit 2
}

Import-Module Pester -MinimumVersion 5.0 -ErrorAction Stop

$result = Invoke-Pester -Container (New-PesterContainer -ScriptBlock {
    param($ProfilePath)

    Describe 'aish PowerShell profile snippet' {
        BeforeAll {
            $script:profileText = Get-Content -Path $ProfilePath -Raw
            # Dot-source into a child scope so we can probe the symbols.
            $script:scope = [scriptblock]::Create($script:profileText)
            . $script:scope
        }

        It 'parses without errors' {
            $errors = $null
            [void][System.Management.Automation.Language.Parser]::ParseInput(
                $script:profileText, [ref]$null, [ref]$errors)
            $errors.Count | Should -Be 0
        }

        It 'defines the aish function' {
            Get-Item -Path 'function:aish' -ErrorAction SilentlyContinue |
                Should -Not -BeNullOrEmpty
        }

        It 'defines the Invoke-Aish advanced function' {
            $fn = Get-Item -Path 'function:Invoke-Aish' -ErrorAction SilentlyContinue
            $fn | Should -Not -BeNullOrEmpty
            # Advanced functions carry the CmdletBinding attribute.
            $fn.ScriptBlock.Attributes |
                Where-Object { $_.GetType().Name -eq 'CmdletBindingAttribute' } |
                Should -Not -BeNullOrEmpty
        }

        It 'defines the ai alias pointing at aish' {
            $alias = Get-Alias -Name 'ai' -ErrorAction SilentlyContinue
            $alias | Should -Not -BeNullOrEmpty
            $alias.Definition | Should -Be 'aish'
        }

        It 'contains the aish-managed marker comment' {
            $script:profileText | Should -Match '# aish-managed:'
        }
    }
} -Data @{ ProfilePath = $resolved }) -PassThru -Output Detailed

if ($result.FailedCount -gt 0) {
    exit 1
}
exit 0
