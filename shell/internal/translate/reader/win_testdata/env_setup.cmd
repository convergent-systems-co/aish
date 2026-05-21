@echo off
:: Per-shell config seed. Sourced by older win shells.
set TARGET=production
set PATH=C:\tools;%PATH%
if "%TARGET%"=="production" ( echo running prod ) else ( echo running dev )
goto :EOF
