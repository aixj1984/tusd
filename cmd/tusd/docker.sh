#!/bin/bash
set -e

cd "$(dirname "$0")"

# 与 build.sh 一致：可执行文件名、镜像名均取当前目录名
CurrentDir=$(basename "$(pwd)")
BIN="./${CurrentDir}"
IMAGE_NAME="hmi-${CurrentDir}"

echo "目录名:     $CurrentDir"
echo "可执行文件: $BIN"
echo "镜像名:     $IMAGE_NAME"

if [ ! -f "$BIN" ]; then
    echo "错误：找不到可执行文件 $BIN，请先执行 ./build.sh 编译"
    exit 1
fi

# 从二进制内嵌版本信息取出 Git Commit
# 输出示例：
#   Git Commit:  abcdef0123... 或 abcdef0123..._local_edit
VERSION_OUT=$("$BIN" --version 2>&1) || true
echo "$VERSION_OUT"

GitCommit=$(echo "$VERSION_OUT" | sed -n 's/.*Git Commit:[[:space:]]*\(.*\)/\1/p' | tr -d '\r' | head -n1)

if [ -z "$GitCommit" ] || [ "$GitCommit" = "unknown" ]; then
    echo "错误：无法从 $BIN -v 解析到有效的 Git Commit，请用 ./build.sh 重新编译"
    exit 1
fi

# 取短 commit（前 7 位）；若带 _local_edit 后缀则保留
if [[ "$GitCommit" == *_local_edit ]]; then
    ShortCommit="${GitCommit:0:7}_local_edit"
else
    ShortCommit="${GitCommit:0:7}"
fi

# Docker tag 不允许部分字符，将不安全字符替换为 -
ImageTag=$(echo "$ShortCommit" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g')

FullImage="${IMAGE_NAME}:${ImageTag}"

echo "GitCommit: $GitCommit"
echo "ShortCommit: $ShortCommit"
echo "Image:     ${FullImage}"

# 本地已有同名版本则先删除
if docker image inspect "${FullImage}" >/dev/null 2>&1; then
    echo "本地已存在镜像 ${FullImage}，先删除..."
    docker rmi -f "${FullImage}"
fi

docker build -t "${FullImage}" .

echo "构建成功：${FullImage}"


# 镜像打标签
docker tag "${FullImage}" "hub.hobot.cc/aitools/${IMAGE_NAME}:${ImageTag}"
# 登录一次，后续就不需要了
# docker login hub.hobot.cc -u xiongjun.ai -p 'XXX'
# 镜像推送
docker push "hub.hobot.cc/aitools/${IMAGE_NAME}:${ImageTag}"

echo "镜像推送成功：hub.hobot.cc/aitools/${IMAGE_NAME}:${ImageTag}"
