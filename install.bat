@echo off
REM Wrapper script to install pvr on Windows
REM This script delegates to install.ps1

setlocal

REM Run PowerShell with ExecutionPolicy Bypass to execute install.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*

endlocal
