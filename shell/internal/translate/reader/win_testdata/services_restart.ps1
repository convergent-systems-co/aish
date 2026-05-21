# Restart the print spooler if it is running.
$svc = 'Spooler'
if ($svc) {
    Stop-Service $svc
    Start-Service $svc
} else {
    Write-Host 'no service named'
}
