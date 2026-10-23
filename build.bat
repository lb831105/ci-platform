@echo off
rem ============================================================================
rem  ci-platform - Windows build script (cmd.exe)
rem
rem  What it does:
rem    1. builds the Vue3 frontend (vite) into web\dist
rem    2. cross-compiles the Go server for linux/amd64 and windows/amd64
rem       (the frontend is embedded into the binary via go:embed)
rem    3. compresses the artifacts with UPX
rem    4. prints a result table
rem
rem  Usage:
rem    build.bat                        build linux + windows, then UPX
rem    build.bat --single               build only for the host OS/arch
rem    build.bat --platforms linux/amd64,linux/arm64,windows/amd64
rem    build.bat --skip-web             reuse the existing web\dist
rem    build.bat --no-upx               skip UPX
rem    build.bat --upx-level "--best --lzma"
rem    build.bat -o out                 output directory (default: dist)
rem    build.bat -h                     show this help
rem
rem  Notes:
rem    - messages are ASCII-only on purpose: cmd.exe codepage handling of
rem      UTF-8 batch files is unreliable across Windows locales.
rem    - run it from anywhere; it cd's to its own directory first.
rem ============================================================================
setlocal EnableExtensions EnableDelayedExpansion
chcp 65001 >nul 2>nul

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"
cd /d "%ROOT%" || (
    echo [ERROR] cannot enter script directory: %ROOT%
    exit /b 1
)

set "APPNAME=ci-platform"
set "PKG=./cmd/server"
set "OUTDIR=dist"
set "PLATFORMS=linux/amd64 windows/amd64"
set "SKIP_WEB=0"
set "NO_UPX=0"
set "SINGLE=0"
set "UPX_LEVEL=--best --lzma"

rem ---------------------------------------------------------------- arg parsing
:parse_args
if "%~1"=="" goto args_done
if /i "%~1"=="-h"         goto usage
if /i "%~1"=="--help"     goto usage
if /i "%~1"=="/?"         goto usage
if /i "%~1"=="--single"   ( set "SINGLE=1"   & shift & goto parse_args )
if /i "%~1"=="--skip-web" ( set "SKIP_WEB=1" & shift & goto parse_args )
if /i "%~1"=="--no-upx"   ( set "NO_UPX=1"   & shift & goto parse_args )
if /i "%~1"=="-o"         ( set "OUTDIR=%~2" & shift & shift & goto parse_args )
if /i "%~1"=="--output-dir" ( set "OUTDIR=%~2" & shift & shift & goto parse_args )
if /i "%~1"=="--upx-level"  ( set "UPX_LEVEL=%~2" & shift & shift & goto parse_args )
if /i "%~1"=="--platforms" (
    if "%~2"=="" (
        echo [ERROR] --platforms needs a value, e.g. --platforms linux/amd64,windows/amd64
        exit /b 2
    )
    set "PLATFORMS=%~2"
    rem accept both comma and space separated lists
    set "PLATFORMS=!PLATFORMS:,= !"
    shift & shift & goto parse_args
)
echo [ERROR] unknown option: %~1
echo.
goto usage_err

:args_done

rem ------------------------------------------------------------------- preflight
echo ============================================================
echo  ci-platform build
echo ============================================================
echo   repo dir  : %ROOT%
echo   output    : %OUTDIR%
echo   package   : %PKG%
echo   upx       : %UPX_LEVEL%  (skip=%NO_UPX%)
echo.

where go >nul 2>nul
if errorlevel 1 (
    echo [ERROR] "go" not found in PATH. Install Go 1.24+ first:
    echo         https://go.dev/dl/
    exit /b 1
)
where node >nul 2>nul
if errorlevel 1 (
    if "%SKIP_WEB%"=="0" (
        echo [ERROR] "node" not found in PATH. Install Node.js 18+ first:
        echo         https://nodejs.org/
        echo         Or pass --skip-web to reuse an existing web\dist.
        exit /b 1
    )
)

if "%SINGLE%"=="1" (
    for /f "delims=" %%o in ('go env GOOS')   do set "HOSTOS=%%o"
    for /f "delims=" %%a in ('go env GOARCH') do set "HOSTARCH=%%a"
    set "PLATFORMS=!HOSTOS!/!HOSTARCH!"
)

rem -------------------------------------------------------------- [1/5] frontend
if "%SKIP_WEB%"=="1" (
    echo [1/5] frontend build : SKIPPED ^(--skip-web^)
    goto web_done
)

echo [1/5] frontend build : running vite ...
pushd web
if not exist package.json (
    popd
    echo [ERROR] web\package.json not found. Is this the right project root?
    exit /b 1
)
if exist package-lock.json (
    echo       npm ci
    call npm ci
) else (
    echo       npm install
    call npm install
)
if errorlevel 1 (
    popd
    echo [ERROR] npm dependency install failed.
    exit /b 1
)
echo       npm run build
call npm run build
if errorlevel 1 (
    popd
    echo [ERROR] frontend build failed.
    exit /b 1
)
popd
echo       frontend build : OK

:web_done

rem --------------------------------------------------- [2/5] embed precondition
echo [2/5] checking embedded assets precondition ...
if not exist "web\dist\index.html" (
    echo [ERROR] web\dist\index.html is missing.
    echo         The server embeds the frontend with go:embed, so web\dist MUST
    echo         exist before compiling. Run without --skip-web, or do:
    echo             cd web ^&^& npm install ^&^& npm run build
    exit /b 1
)
echo       web\dist\index.html : OK

rem ---------------------------------------------------------- [3/5] compile
set "GOTOOLCHAIN=local"
set "CGO_ENABLED=0"

set "VERSION=dev"
set "GIT_COMMIT="
for /f "delims=" %%i in ('git rev-parse --short HEAD 2^>nul') do set "GIT_COMMIT=%%i"
if not defined GIT_COMMIT set "GIT_COMMIT=none"

set "BUILD_DATE="
rem build date must not contain spaces: ldflags splits -X values on spaces
for /f "delims=" %%i in ('powershell -NoProfile -Command "Get-Date -Format yyyy-MM-ddTHH:mm:ssZ" 2^>nul') do set "BUILD_DATE=%%i"
if not defined BUILD_DATE set "BUILD_DATE=unknown"

set "LDFLAGS=-s -w -X main.buildVersion=%VERSION% -X main.buildCommit=%GIT_COMMIT% -X main.buildDate=%BUILD_DATE%"

echo [3/5] compiling ...
echo       version=%VERSION% commit=%GIT_COMMIT% built=%BUILD_DATE%
if not exist "%OUTDIR%" mkdir "%OUTDIR%"

for %%P in (%PLATFORMS%) do (
    call :build_one "%%P"
    if errorlevel 1 exit /b 1
)

rem --------------------------------------------------------------- [4/5] upx
echo [4/5] compressing with UPX ...
if "%NO_UPX%"=="1" (
    echo       SKIPPED ^(--no-upx^)
    goto upx_done
)
where upx >nul 2>nul
if errorlevel 1 (
    echo       [WARN] "upx" not found in PATH - skipping compression.
    echo              Download: https://github.com/upx/upx/releases
    echo              then add upx.exe to PATH and re-run.
    goto upx_done
)
for /f "delims=" %%v in ('upx --version 2^>nul') do (
    if not defined UPX_VER set "UPX_VER=%%v"
)
echo       %UPX_VER%
for %%P in (%PLATFORMS%) do (
    call :upx_one "%%P"
)

:upx_done

rem --------------------------------------------------------------- [5/5] report
echo [5/5] artifacts
echo ------------------------------------------------------------
for %%P in (%PLATFORMS%) do call :report_one "%%P"
echo ------------------------------------------------------------
echo.
echo Done. Run the server with a command line port, for example:
echo     %OUTDIR%\%APPNAME%-windows-amd64.exe -port 9090
echo     %OUTDIR%\%APPNAME%-windows-amd64.exe -h
echo.
endlocal
exit /b 0

rem ============================================================================
rem  subroutines
rem ============================================================================

rem :derive_platform <os>/<arch>  -> sets GOOS GOARCH EXT OUTFILE
:derive_platform
set "SPEC=%~1"
set "GOOS="
set "GOARCH="
for /f "tokens=1,2 delims=/" %%A in ("%SPEC%") do (
    set "GOOS=%%A"
    set "GOARCH=%%B"
)
if not defined GOARCH set "GOARCH=amd64"
if not defined GOOS (
    echo [ERROR] invalid platform spec: "%SPEC%" ^(expected os/arch^)
    exit /b 1
)
set "EXT="
if /i "%GOOS%"=="windows" set "EXT=.exe"
set "OUTFILE=%OUTDIR%\%APPNAME%-%GOOS%-%GOARCH%%EXT%"
exit /b 0

:build_one
call :derive_platform "%~1"
if errorlevel 1 exit /b 1
echo       %GOOS%/%GOARCH%  -^>  %OUTFILE%
set "CGO_ENABLED=0"
set "GOOS=%GOOS%"
set "GOARCH=%GOARCH%"
go build -trimpath -ldflags "%LDFLAGS%" -o "%OUTFILE%" %PKG%
if errorlevel 1 (
    echo [ERROR] compile failed: %GOOS%/%GOARCH%
    exit /b 1
)
exit /b 0

:upx_one
call :derive_platform "%~1"
if errorlevel 1 exit /b 1
if not exist "%OUTFILE%" exit /b 0
echo       upx %UPX_LEVEL%  %OUTFILE%
upx %UPX_LEVEL% -q "%OUTFILE%"
if errorlevel 1 (
    echo       [WARN] upx reported a problem for %OUTFILE% ^(keeping the original^)
)
rem UPX may leave a <file>~ backup behind if it cannot remove it
rem (antivirus / file lock). Clean it up so the output dir stays tidy.
if exist "%OUTFILE%~" del /q "%OUTFILE%~" >nul 2>nul
exit /b 0

:report_one
call :derive_platform "%~1"
if errorlevel 1 exit /b 1
if not exist "%OUTFILE%" (
    echo   MISSING  %OUTFILE%
    exit /b 0
)
for %%F in ("%OUTFILE%") do set "SZ=%%~zF"
set /a "KB=!SZ!/1024"
echo   %OUTFILE%   !SZ! bytes ^(!KB! KB^)
exit /b 0

rem ============================================================================
rem  help
rem ============================================================================
:usage
set "EXITCODE=0"
goto usage_print

:usage_err
set "EXITCODE=2"

:usage_print
echo ============================================================
echo  ci-platform build.bat
echo ============================================================
echo.
echo  Usage:
echo    build.bat [options]
echo.
echo  Options:
echo    --platforms LIST   comma or space separated os/arch pairs
echo                       default: linux/amd64 windows/amd64
echo                       e.g.  --platforms linux/arm64,windows/amd64
echo    --single           build only for the host OS/arch
echo    --skip-web         skip the frontend build, reuse web\dist
echo    --no-upx           do not compress with UPX
echo    --upx-level LVL    UPX flags, default: "--best --lzma"
echo    -o DIR             output directory, default: dist
echo    -h, --help         show this help
echo.
echo  Examples:
echo    build.bat
echo    build.bat --single --no-upx
echo    build.bat --platforms linux/amd64,linux/arm64
echo    build.bat --skip-web --upx-level --lzma
echo.
echo  Requirements:
echo    go 1.24+   node 18+   upx 4.x (optional but recommended)
echo.
endlocal
exit /b %EXITCODE%
