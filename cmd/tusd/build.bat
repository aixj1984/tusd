echo "start build....."
@echo off
setlocal

:: 转中文输出
chcp 65001

:: 获取当前文件的目录名称
for %%I in (.) do set "CurrentDir=%%~nxI"

:: SET OutFile=%CurrentDir%
:: SET OutFile=admin-api

echo 当前盘符和路径：%~dp0
echo 当前目录名：%CurrentDir%

:: 默认 windows
SET CGO_ENABLED=0
SET GOOS=windows
SET OutFile=%CurrentDir%.exe
SET Platform=windows


:: 检查是否有参数指定平台
if "%1"=="linux" (
    SET GOARCH=amd64
    SET GOOS=linux
    SET OutFile=%CurrentDir%
    SET Platform=linux
)

if exist "%OutFile%" (
    echo "删除旧文件：%OutFile%"
    del  "%OutFile%"
)

:: 检查是否有未提交的更改
git diff-index --quiet HEAD --
if %ERRORLEVEL% EQU 0 (
    echo "Git：代码库干净，无未提交更改"
    SET HasUncommittedChanges=false
) else (
    echo "Git：警告，存在未提交的更改"
    SET HasUncommittedChanges=true
)

SET AppVersion=1.0.0

echo AppVersion: %AppVersion%
echo Platform: %Platform%


:: 获取 Git 提交 ID
:: for /f "delims=" %%i in ('git rev-parse HEAD') do set GitCommit=%%i
:: echo GitCommit: %GitCommit%

:: 获取 Git 提交 ID
for /f "delims=" %%i in ('git rev-parse HEAD') do set GitCommit=%%i

:: 如果有未提交的更改，则使用 local_edit_code 标记
if "%HasUncommittedChanges%"=="true" (
    SET GitCommit=%GitCommit%_local_edit
) 

echo GitCommit: %GitCommit%

:: 获取当前分支名
for /f "delims=" %%i in ('git rev-parse --abbrev-ref HEAD') do set GitBranch=%%i
echo GitBranch: %GitBranch%


:: 获取当前时间
for /f "delims=" %%i in ('powershell -Command "Get-Date -Format 'yyyy-MM-dd HH:mm:ss'"') do set BuildDate=%%i
echo BuildDate: %BuildDate%

:: 获取 Go 版本
for /f "delims=" %%i in ('go version') do set GoVersion=%%i
echo GoVersion: %GoVersion%

:: 版本信息所在的包路径
set PACKAGE_PATH=github.com/tus/tusd/v2/cmd/tusd/cli

go build -ldflags="-X '%PACKAGE_PATH%.AppVersion=%AppVersion%' -X '%PACKAGE_PATH%.GitCommit=%GitCommit%' -X '%PACKAGE_PATH%.GitBranch=%GitBranch%' -X '%PACKAGE_PATH%.BuildDate=%BuildDate%' -X '%PACKAGE_PATH%.GoVersion=%GoVersion%'" -o "%OutFile%"
:: go build -o "%OutFile%"
:: go build -ldflags="-X 'github.com/tus/tusd/v2/cmd/tusd/cli.VersionName=%AppVersion%' -X 'github.com/tus/tusd/v2/cmd/tusd/cli.GitCommit=%GitCommit%' -X 'github.com/tus/tusd/v2/cmd/tusd/cli.GitBranch=%GitBranch%' -X 'github.com/tus/tusd/v2/cmd/tusd/cli.BuildDate=%BuildDate%' -X 'github.com/tus/tusd/v2/cmd/tusd/cli.GoVersion=%GoVersion%'" -o "%OutFile%"


if %ERRORLEVEL% EQU 0 (
    echo "构建成功！输出文件: %OutFile%"
) else (
    echo "构建失败！"
)

SET CGO_ENABLED=0
SET GOOS=windows
SET GOARCH=amd64

endlocal

