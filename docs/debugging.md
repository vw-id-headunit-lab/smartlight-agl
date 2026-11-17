# 调试工具：手动触发与链路排查

这里的命令用于排查 SmartLight 服务、用途匹配和请求清理，**不属于日常 CarPlay 联动流程**。
正常使用从[主 README](../README.md)的预演和自动联动开始。
手动触发前停止自动监控，避免两套请求相互影响；先查看只读结果，再按需短时测试。

## 1. 设置灯效地址

在解压后的 Go 运行目录：

```sh
HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"
```

这里不需要导航注册中心，因为这些命令只检查灯效端。
需要鉴权时添加 `--auth-file /path/to/light-header.txt`。

## 2. 只读目录与诊断

```sh
./smartlight-agl list --base "$BASE"
./smartlight-agl probe --base "$BASE"
./smartlight-agl --help
./smartlight-agl trigger --help
```

`list` 读取实际用途目录及分页；`probe` 只读配置、组件版本、功能组、用途、队列和播放器。
关注服务是否 ready、HMI 对应功能组是否启用、左右用途是否唯一匹配，以及有无更高优先级请求。
这些命令不修改开关或亮度。

## 3. 手动短时请求

```sh
# 默认预演，不点灯
./smartlight-agl trigger --base "$BASE" --direction left

# 实际请求，客户端目标保持 2 秒后撤销本次队列项
./smartlight-agl trigger --base "$BASE" --direction right --hold 2 --apply

# shell 形式和中文方向参数
sh trigger_light.sh trigger --base "$BASE" --direction 左转 --hold 2 --apply
```

方向参数精确匹配实时 `purposes` 的 `navigation / direction / left|right`。
不要将 14/15、movie ID 或平台机动枚举直接当作资源 UUID。
需要检查其它现有用途时，以下选择器与 `--direction` 三选一：

```sh
./smartlight-agl trigger --base "$BASE" --purpose '实时用途名称或资源ID'
./smartlight-agl trigger --base "$BASE" --purpose-uri '/smartlight/purposes/实际资源ID'
```

省略 `--apply` 均为预演。hold 允许 0.1–5 秒，是客户端撤销时机，不是服务端 TTL。
HTTP 成功、播放器选择与实体亮灯是不同观察结果；工具本身不证明实体灯带状态。

## 4. 只清理自己的请求

记录 `state_saved` / `cleanup_pending` 中的真实状态路径：

```sh
./smartlight-agl cleanup --state /tmp/实际状态.json
sh rollback.sh --state /tmp/实际状态.json
```

恢复只使用已保存的准确队列 URI。未知 POST 结果和缺失 Location 不应按公共名称猜测删除，
也不要为调试清空其它业务队列。详见[恢复说明](carplay.md)。

## 5. 独立 Python 手动工具（仅源码目录）

没有使用 Go 二进制、只需排查手动触发时，可以整目录复制 `python-trigger/`。
它需要 Python 2.7/3 与 curl，不含 CarPlay，也不包含在 Go-only 运行包中。

```sh
# 在源码仓库根目录执行；Go 运行包中没有这个子目录
sh python-trigger/trigger_light.sh list --base "$BASE"
sh python-trigger/trigger_light.sh trigger --base "$BASE" --direction left
sh python-trigger/trigger_light.sh trigger --base "$BASE" --direction right --hold 2 --apply
sh python-trigger/rollback.sh --state /tmp/实际状态.json
```

源码中的 [python-trigger/README.md](https://github.com/vw-id-headunit-lab/smartlight-agl/tree/refactor/golang/python-trigger)
说明独立部署、测试和打包。根目录启动器不通过环境变量切换到 Python。
