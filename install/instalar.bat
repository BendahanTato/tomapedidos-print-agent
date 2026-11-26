@echo off
setlocal
chcp 65001 >nul

:: Lanzador con elevacion automatica (UAC) para el instalador de TomaPedidos Print Agent.
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo Solicitando permisos de Administrador...
    powershell -NoProfile -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
    exit /b %errorlevel%
)

echo ============================================================
echo   TomaPedidos Print Agent - Instalador Paso a Paso
echo ============================================================
echo.

powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
set EXIT_CODE=%errorlevel%

echo.
if %EXIT_CODE% equ 0 (
    echo [OK] Instalacion finalizada correctamente.
) else (
    echo [ERROR] La instalacion se detuvo con codigo %EXIT_CODE%.
    echo Revisa el log en: C:\ProgramData\Tomapedidos\logs\install.log
)
echo.
pause
exit /b %EXIT_CODE%
