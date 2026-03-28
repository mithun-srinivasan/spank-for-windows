@echo off
setlocal enabledelayedexpansion
title Spank Installer

set "BUILD_ONLY=0"
if /I "%~1"=="--help" goto :usage
if /I "%~1"=="-h" goto :usage
if /I "%~1"=="--build-only" set "BUILD_ONLY=1"

goto :main

:usage
echo Usage: build.bat [--build-only]
echo.
echo   --build-only   Build spank.exe without local install or shortcut creation
echo   -h, --help     Show this help
echo.
exit /b 0

:main

echo ============================================================
echo  Spank Installer
echo ============================================================
echo.

where gcc >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] GCC not found. Download MinGW-W64 from:
    echo         https://github.com/brechtsanders/winlibs_mingw/releases/latest
    echo         Get: winlibs-x86_64-posix-seh-gcc-*.zip, extract to C:\mingw64
    echo         Add C:\mingw64\bin to top of System PATH
    pause & exit /b 1
)
for /f "tokens=3" %%v in ('gcc --version ^| findstr /r "gcc"') do set GCCVER=%%v & goto :gccok
:gccok
echo [OK] GCC %GCCVER%

where go >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Go not found. Download from https://go.dev/dl/
    pause & exit /b 1
)
for /f "tokens=3" %%v in ('go version') do set GOVERSION=%%v
echo [OK] Go %GOVERSION%

where git >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] git not found. Download from https://git-scm.com/download/win
    pause & exit /b 1
)
echo [OK] git found

echo.
echo [*] Downloading audio from taigrr/spank...
if exist "%TEMP%\spank-src" rmdir /s /q "%TEMP%\spank-src"
git clone --depth=1 --filter=blob:none --sparse https://github.com/taigrr/spank.git "%TEMP%\spank-src" >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Failed to clone audio. Check internet.
    pause & exit /b 1
)
cd /d "%TEMP%\spank-src"
git sparse-checkout set audio >nul 2>&1
cd /d "%~dp0"

echo [*] Copying audio files...
set i=1
for %%f in ("%TEMP%\spank-src\audio\pain\*.mp3") do (
    if !i! lss 10 (set "n=0!i!") else (set "n=!i!")
    copy /y "%%f" "%~dp0audio\pain\!n!.mp3" >nul
    set /a i+=1
)
set i=1
for %%f in ("%TEMP%\spank-src\audio\halo\*.mp3") do (
    if !i! lss 10 (set "n=0!i!") else (set "n=!i!")
    copy /y "%%f" "%~dp0audio\halo\!n!.mp3" >nul
    set /a i+=1
)
set i=1
for %%f in ("%TEMP%\spank-src\audio\sexy\*.mp3") do (
    if !i! lss 10 (set "n=0!i!") else (set "n=!i!")
    copy /y "%%f" "%~dp0audio\sexy\!n!.mp3" >nul
    set /a i+=1
)
rmdir /s /q "%TEMP%\spank-src" >nul 2>&1
echo [OK] Audio ready

echo.
echo [*] Embedding icon into executable...
where windres >nul 2>&1
if %ERRORLEVEL% equ 0 (
    windres -i "%~dp0resource.rc" -o "%~dp0resource.syso" 2>nul
    echo [OK] Icon embedded
) else (
    echo [SKIP] windres not found, skipping icon embedding
)

echo.
echo [*] Installing build tools...
go install fyne.io/fyne/v2/cmd/fyne@latest >nul 2>&1
echo [OK] fyne tool ready

echo.
echo [*] Fetching Go dependencies...
cd /d "%~dp0"
go get . >nul 2>&1
go mod tidy >nul 2>&1
echo [OK] Dependencies ready

echo.
echo [*] Building...
go build -ldflags="-s -w -H windowsgui" -o spank.exe .
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Build failed.
    pause & exit /b 1
)
echo [OK] Build successful

if "%BUILD_ONLY%"=="1" (
    echo.
    echo [OK] Build-only mode complete. Output: %~dp0spank.exe
    echo.
    pause
    exit /b 0
)

echo.
echo [*] Installing...
set "INSTALL=%LOCALAPPDATA%\Spank"
if not exist "%INSTALL%" mkdir "%INSTALL%"
copy /y "%~dp0spank.exe" "%INSTALL%\spank.exe" >nul
copy /y "%~dp0icon.ico" "%INSTALL%\icon.ico" >nul 2>&1
echo [OK] Installed to %INSTALL%

echo [*] Creating shortcuts with icon...
set "PS=%TEMP%\spank_sc.ps1"
(
    echo $ws = New-Object -ComObject WScript.Shell
    echo $s = $ws.CreateShortcut^("$env:USERPROFILE\Desktop\Spank.lnk"^)
    echo $s.TargetPath = "%INSTALL%\spank.exe"
    echo $s.WorkingDirectory = "%INSTALL%"
    echo $s.IconLocation = "%INSTALL%\icon.ico,0"
    echo $s.Description = "Slap your laptop, it yells back"
    echo $s.Save^(^)
    echo $s2 = $ws.CreateShortcut^("%APPDATA%\Microsoft\Windows\Start Menu\Programs\Spank.lnk"^)
    echo $s2.TargetPath = "%INSTALL%\spank.exe"
    echo $s2.WorkingDirectory = "%INSTALL%"
    echo $s2.IconLocation = "%INSTALL%\icon.ico,0"
    echo $s2.Description = "Slap your laptop, it yells back"
    echo $s2.Save^(^)
) > "%PS%"
powershell -NoProfile -ExecutionPolicy Bypass -File "%PS%" >nul 2>&1
del "%PS%" >nul 2>&1
echo [OK] Shortcuts created with icon

echo.
echo ============================================================
echo  Spank installed! Double-click the Desktop shortcut to open.
echo ============================================================
echo.
pause
