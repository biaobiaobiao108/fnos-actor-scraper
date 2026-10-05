#!/bin/sh
set -eu

if [ "$#" -gt 0 ]; then
	exec /usr/local/bin/fnactor "$@"
fi

printf '%s\n' \
	"fnactor 容器已就绪，当前为空闲模式。" \
	"进入容器终端后运行：fnactor --limit 20" \
	"实际写入飞牛资料时才添加 --apply。"

exec tail -f /dev/null
