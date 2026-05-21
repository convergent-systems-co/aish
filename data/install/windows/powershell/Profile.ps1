# aish — PowerShell profile snippet (v1.0-1, #134)
#
# Hybrid users keep object-pipeline semantics on the PowerShell side
# and stream-pipeline semantics on the aish side. The two functions
# below bridge them:
#
#   - `aish`        — thin pass-through. Bytes in, bytes out. Use for
#                     `cat file.txt | aish "summarize this"`.
#   - `Invoke-Aish` — advanced function. Accepts pipeline input and
#                     stringifies it via Out-String before reaching
#                     aish. Use for `Get-Process | Invoke-Aish "which
#                     of these is using the most CPU"`.
#
# Install:
#   1. Open your PowerShell profile:    notepad $PROFILE
#      (if the file does not exist:     New-Item -Path $PROFILE -Force)
#   2. Append everything between BEGIN and END markers below.
#   3. Reload:                          . $PROFILE
#
# Upgrade-safe: the `# aish-managed:` marker line below delimits the
# block. A future aish installer can locate it and replace the block
# in place without disturbing the rest of your $PROFILE.

# aish-managed: BEGIN — do not edit this block by hand; aish manages it.

function aish {
    <#
    .SYNOPSIS
        Pass-through invocation of aish.exe with byte-stream stdin/stdout.
    #>
    [CmdletBinding()]
    param(
        [Parameter(ValueFromRemainingArguments = $true)]
        [string[]]$ArgumentList
    )
    # NB: native binary invocation. PowerShell's "automatic encoding"
    # of stdin can mangle non-UTF-8 bytes; recent pwsh (7.4+) defaults
    # to UTF-8 — older Windows PowerShell users may need to set
    # [Console]::InputEncoding = [System.Text.Encoding]::UTF8 manually.
    & aish.exe @ArgumentList
}

function Invoke-Aish {
    <#
    .SYNOPSIS
        Send PowerShell objects to aish as stringified pipeline input.

    .DESCRIPTION
        Wraps `aish.exe` so PowerShell-native object pipelines
        (Get-Process, Get-ChildItem, etc.) become text streams aish
        can read. Object-to-text conversion uses Out-String -Stream,
        which matches what the console renders.

    .EXAMPLE
        Get-Process | Invoke-Aish "which process is using the most CPU"

    .EXAMPLE
        Get-ChildItem -Recurse *.log | Invoke-Aish "delete files older than 30 days"
    #>
    [CmdletBinding()]
    param(
        [Parameter(Position = 0, ValueFromRemainingArguments = $true)]
        [string[]]$ArgumentList,

        [Parameter(ValueFromPipeline = $true)]
        $InputObject
    )
    begin {
        $accumulator = New-Object System.Collections.Generic.List[object]
    }
    process {
        if ($null -ne $InputObject) {
            $accumulator.Add($InputObject)
        }
    }
    end {
        if ($accumulator.Count -gt 0) {
            $accumulator | Out-String -Stream | & aish.exe @ArgumentList
        } else {
            & aish.exe @ArgumentList
        }
    }
}

Set-Alias -Name ai -Value aish -Scope Global -Force

# aish-managed: END
