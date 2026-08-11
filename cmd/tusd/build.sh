#!/bin/bash
set -e

echo "start build....."

# 获取当前目录名
CurrentDir=$(basename "$(pwd)")
echo "当前路径：$(pwd)"
echo "当前目录名：$CurrentDir"

# 默认 linux（本机）
export CGO_ENABLED=0
export GOOS=linux
export GOARCH=amd64
OutFile="$CurrentDir"
Platform=linux

# 检查是否有参数指定平台
if [ "$1" = "windows" ]; then
    export GOOS=windows
    OutFile="${CurrentDir}.exe"
    Platform=windows
fi

if [ -f "$OutFile" ]; then
    echo "删除旧文件：$OutFile"
    rm -f "$OutFile"
fi

# 检查是否有未提交的更改
if git diff-index --quiet HEAD -- 2>/dev/null; then
    echo "Git：代码库干净，无未提交更改"
    HasUncommittedChanges=false
else
    echo "Git：警告，存在未提交的更改"
    HasUncommittedChanges=true
fi

AppVersion=1.0.0

echo "AppVersion: $AppVersion"
echo "Platform: $Platform"

# 获取 Git 提交 ID
GitCommit=$(git rev-parse HEAD)

# 如果有未提交的更改，则使用 local_edit 标记
if [ "$HasUncommittedChanges" = "true" ]; then
    GitCommit="${GitCommit}_local_edit"
fi

echo "GitCommit: $GitCommit"

# 获取当前分支名
GitBranch=$(git rev-parse --abbrev-ref HEAD)
echo "GitBranch: $GitBranch"

# 获取当前时间
BuildDate=$(date '+%Y-%m-%d %H:%M:%S')
echo "BuildDate: $BuildDate"

# 获取 Go 版本
GoVersion=$(go version)
echo "GoVersion: $GoVersion"

# 版本信息所在的包路径
PACKAGE_PATH=github.com/tus/tusd/v2/cmd/tusd/cli

if go build -ldflags="-s -w -X '${PACKAGE_PATH}.AppVersion=${AppVersion}' -X '${PACKAGE_PATH}.GitCommit=${GitCommit}' -X '${PACKAGE_PATH}.GitBranch=${GitBranch}' -X '${PACKAGE_PATH}.BuildDate=${BuildDate}' -X '${PACKAGE_PATH}.GoVersion=${GoVersion}'" -o "$OutFile"; then
    echo "构建成功！输出文件: $OutFile"
else
    echo "构建失败！"
    exit 1
fi
