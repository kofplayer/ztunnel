@echo off
REM 交叉编译 4 个二进制：linux/windows x64 的 client 与 server。
REM 产物统一输出到**仓库根目录**，与 Readme 的运行示例一致。
REM
REM 此前的问题（报告 E-03）：
REM   1. go build 失败后仍继续执行 move，会把上一轮的陈旧产物当成新构建结果搬走；
REM   2. move 目标 "..\ztunnel_server_linux_x64" 里的 .. 是 cmd\，产物落在 cmd\ 而非仓库根；
REM   3. 非幂等：重跑时目标已存在会使 move 失败。
REM 改用 go build -o 显式指定输出路径，一步解决三点。

setlocal
cd /d "%~dp0"

set CGO_ENABLED=0
set GOARCH=amd64

set GOOS=linux
call go build -o ..\ztunnel_client_linux_x64 .\client || goto :fail
call go build -o ..\ztunnel_server_linux_x64 .\server || goto :fail

set GOOS=windows
call go build -o ..\ztunnel_client_windows_x64.exe .\client || goto :fail
call go build -o ..\ztunnel_server_windows_x64.exe .\server || goto :fail

echo.
echo build ok, artifacts in repo root:
dir /b ..\ztunnel_*
exit /b 0

:fail
echo.
echo BUILD FAILED
exit /b 1
