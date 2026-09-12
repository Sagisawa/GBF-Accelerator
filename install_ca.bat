@echo off
title Install GBF CA Certificate
cd /d "%~dp0"
echo ========================================================
echo   GBF Speed Proxy - Install Root CA Certificate
echo ========================================================
echo.
echo [*] Installing Root CA to CurrentUser Trusted Store...
echo [*] Windows will show a Security Warning dialog.
echo [*] Please click [ Yes ] to confirm installing the certificate!
echo.
certutil -addstore -user Root "%~dp0certs\ca.crt"
echo.
echo ========================================================
echo   Installation finished! Press any key to exit.
echo ========================================================
pause
