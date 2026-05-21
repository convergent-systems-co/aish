@echo off
REM Classic errorlevel idiom.
ping -n 1 localhost
if errorlevel 1 ( echo ping failed ) else ( echo ping ok )
