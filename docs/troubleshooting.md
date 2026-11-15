# 排错与恢复

## `ImportError: No module named io/json/urlparse`

请部署完整运行目录，包括 `agl_runtime.py` 和 `vendor/`。本项目用 codecs、随附 CPython 模块和 curl 兼容精简 Python 2.7。不要只复制主脚本。

## TLS `wrong version number` 或 404

检查实际服务的协议和路径。已验证固件在该端口提供 HTTP，前缀为 `/smartlight/`。不要在 shell 字符串的冒号前保留反斜杠。AGL 与 QNX 的回环地址属于不同系统，应使用现有可达地址。

## POST 返回 `400 invalid request`

使用实时列表中的真实名称及相对用途 URI。本项目已自动处理；旧版把随机运行标识当成请求名称，会被已验证服务拒绝。日志包含状态和错误信息，脚本不重试 POST。

## 匹配失败或功能组关闭

先执行 `list` 检查固件实际元数据。方向参数只匹配导航转向，不回退到变道。匹配不到或不唯一时停止。功能组关闭时先在 HMI 打开对应功能；脚本不会修改开关或亮度。

## 状态文件已存在

不要覆盖上次状态。按需清理：

```sh
sh rollback.sh --state /tmp/实际状态文件.json
```

重复触发时省略 `--state`，自动生成新文件名。已清理文件确认后可自行归档。

## `cleanup_pending` 或缺少 Location

保留日志和状态，恢复连通后重跑 `rollback.sh --state ...`。有已保存的 `queue_uri` 时只处理这个 URI。

POST 结果不明且没有 Location 时，不能仅凭公共用途名称判断本次创建了哪项。脚本会停止，不猜测删除目标。结合服务端记录人工确认；不要批量清队列。`--confirm-absent` 只兼容旧 v1 的独有随机名称状态，不能解除新 v2 的身份要求。

## 接口成功但没有亮灯

HTTP 成功、请求入队、播放器选中与实体灯带播放是不同观察结果。用户设置、高优先级请求、控制器状态及电影资源包会影响效果。`physical_light_verified=false` 表示客户端无法证明实体播放。

## 恢复软件版本

更新前保存完整旧目录。回退时停止正在触发的进程，先按状态清理，再恢复成套运行文件。`rollback.sh` 撤销灯效请求，不恢复软件版本。
