<#
  Bulk install via a pipeline: list every package, filter on prefix,
  forward to install. Used as a smoke test for the PS pipeline shape.
#>
Get-Process | Where-Object Name | Out-Host
