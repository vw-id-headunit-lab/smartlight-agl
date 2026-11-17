# 独立 Python 手动灯效触发工具

本目录仅保留 `list / probe / trigger / cleanup`，不包含 CarPlay 监控。
它可整目录独立复制，使用 Python 2.7/3 + curl，不需要 Go，也不读取根目录文件。
CarPlay 联动使用仓库根目录的 Go 运行时，见 [主项目](https://github.com/vw-id-headunit-lab/smartlight-agl)。

## 目录与依赖

```text
python-trigger/
├── smartlight_trigger.py
├── agl_runtime.py
├── trigger_light.sh
├── rollback.sh
├── vendor/                   # 精简 Python 2.7 的 JSON / URL 解析模块与许可
├── tests/                    # 回环回归和可选远程解释器 mock
└── tools/build_release.py    # 独立 Python 包
```

运行需要 POSIX `/bin/sh`、Python 2.7 或 3、支持 IPv6 的 curl；目标无需 pip/jq。
开发机回归测试与打包使用 Python 3.10+。`vendor/` 原件与哈希清单原样保留。

## 使用

从仓库根目录运行：

```sh
HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"

sh python-trigger/trigger_light.sh list --base "$BASE"
sh python-trigger/trigger_light.sh probe --base "$BASE"

# 默认预演
sh python-trigger/trigger_light.sh trigger --base "$BASE" --direction left

# 实际短时请求：2 秒后撤销本次队列项
sh python-trigger/trigger_light.sh trigger --base "$BASE" --direction right --hold 2 --apply

# 按实时用途名称 / UUID / URI 选择（与 --direction 三选一）
sh python-trigger/trigger_light.sh trigger --base "$BASE" --purpose '实时用途名称'
sh python-trigger/trigger_light.sh --help
```

如果已单独复制本目录，先 `cd python-trigger`，再使用 `sh trigger_light.sh ...`。
单引号内直接写 `http://`，不要写 `http\://`。AGL 与 QNX 回环不通用，工具不会创建转发。

`--direction left/right`（或中文左转/右转）精确匹配实时 purposes 的
`functionCategory=navigation`、`functionName=direction` 与 `functionValue`。
匹配必须唯一；不写死电影编号或用途 URI。

## 凭据与清理

需要鉴权时使用 `--auth-file /path/to/header.txt`，文件中为完整 Authorization 值，权限设为 600。
响应带另一 URI 前缀时可显式传 `--advertised-base`，只按已有服务访问方式使用。

POST 使用真实用途名称与相对用途路径，先落盘再发请求，并立即保存返回 Location。
状态文件路径见 `state_saved` 日志；只撤销其记录的 URI，保留其它请求。

```sh
# 异常后填入日志中的真实状态文件，需要鉴权时再加 --auth-file
sh python-trigger/rollback.sh --state /tmp/实际状态文件.json
```

Ctrl-C/SIGTERM 尽力完成身份记录和清理；未知 POST 结果/缺失 Location 时保留状态，
不凭公共名称猜测删除、不重试 POST。`--hold` 是客户端时长，不是服务端 TTL。
本次目录整理仅做本地回归，不表示已重新验证实车灯带。

## 测试与独立运行包

```sh
# 在仓库根目录
python3 python-trigger/tests/test_trigger.py
python3 python-trigger/tools/build_release.py
```

输出 `python-trigger/artifacts/mock-verification.json` 和
`python-trigger/dist/smartlight-python-trigger.tar.gz`（及 SHA256）。
运行包以 `python-trigger/` 为前缀，仅含手动运行文件、本文档与许可，不含 Go/CarPlay/测试/凭据。
解包后可直接使用其中的 `trigger_light.sh` / `rollback.sh`。

`tests/test_agl_runtime.py` 是可选远程解释器 mock，必须显式 `--target`；它不使用真实灯效服务。
本轮未运行该远程 harness。

## 许可

本工具沿用 [MIT](LICENSE)。随附 CPython 原件的来源和许可见
[第三方声明](THIRD_PARTY_NOTICES.md)、[vendor/LICENSE](vendor/LICENSE)。
