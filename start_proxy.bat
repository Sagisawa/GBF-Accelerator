@echo off
title GBF Speed Accelerator (127.0.0.1:8124)
cd /d "%~dp0"

:: Safe port and zombie cleanup is handled internally by GBF_Accelerator via process package

if exist "%~dp0bin\GBF_Accelerator.exe" (
    start "" "%~dp0bin\GBF_Accelerator.exe" %*
) else (
    echo [*] Binary not found in bin\, running Go engine directly...
    cd "%~dp0engine" && go run . %*
)
