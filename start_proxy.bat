@echo off
title GBF Speed Accelerator (127.0.0.1:8124)
cd /d "%~dp0"

:: Safe port and zombie cleanup is handled internally by gbf_proxy via config_manager.kill_process_on_port

"%~dp0.venv\Scripts\python.exe" "%~dp0gbf_proxy.py"
pause
