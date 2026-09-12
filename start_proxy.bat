@echo off
title GBF Speed Accelerator (127.0.0.1:8124)
cd /d "%~dp0"

:: Automatically terminate any old process occupying port 8124
for /f "tokens=5" %%a in ('netstat -aon ^| findstr ":8124" ^| findstr "LISTENING"') do (
    taskkill /F /PID %%a >nul 2>&1
)

"%~dp0.venv\Scripts\python.exe" "%~dp0gbf_proxy.py"
pause
