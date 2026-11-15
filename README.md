# smartlight-agl

[![CI](https://github.com/vw-id-headunit-lab/smartlight-agl/actions/workflows/ci.yml/badge.svg)](https://github.com/vw-id-headunit-lab/smartlight-agl/actions/workflows/ci.yml)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

从 AGL 通过现有 SmartLight HTTP 服务短时请求 ID.Light 灯效。支持左转、右转、灯效列表、只读诊断及独立清理，兼容精简 Python 2.7 和 Python 3。

本项目是独立社区工具。使用前需已有合法的服务访问方式，请在停车状态测试。脚本默认预演，只有加 `--apply` 才会创建播放请求。

## 环境要求

- AGL/Linux 或其它 POSIX 环境，具备 `/bin/sh`、Python 2.7 或 Python 3、支持 IPv6 的 curl。
- 从当前系统可达的 SmartLight 服务 URI，以及服务需要的正常凭据。
- 可写的临时目录，用于状态记录和权限受限的 HTTP 临时文件。

车机端无需 pip、jq 或额外 Python 包。`vendor/` 为精简 Python 2.7 补充 JSON 和 URL 解析模块；Python 3 使用其标准库。`curl` 负责网络请求。

## 安装

```sh
git clone https://github.com/vw-id-headunit-lab/smartlight-agl.git
cd smartlight-agl
```

部署到车机时，保持以下文件和目录在同一级：

```text
smartlight-agl/
├── trigger_light.sh
├── rollback.sh
├── smartlight_trigger.py
├── agl_runtime.py
└── vendor/
```

也可在开发机运行 `python3 tools/build_release.py` 生成 `dist/smartlight-agl.tar.gz`，将其复制到目标系统后解压。发布包包含运行脚本、使用说明和第三方许可。

## 设置服务地址

用实际服务注册信息中的可达地址替换以下示例：

```sh
# 示例域名，必须替换；保留服务声明的 /smartlight/ 前缀
BASE='http://HEADUNIT_HOST:49520/smartlight/'
sh trigger_light.sh list --base "$BASE"
```

IPv6 地址必须带方括号，例如 `http://[IPv6地址]:49520/smartlight/`。冒号不需要反斜杠转义。协议以实际服务为准；已验证的固件在此端口提供 HTTP。

AGL 与 QNX 的 `[::1]` 分别指向各自系统，不能直接互换。脚本不发现主机或创建跨系统转发。

## 左转与右转

```sh
# 左转：请求播放，等待 2 秒后撤销本次请求
sh trigger_light.sh trigger --base "$BASE" --direction left --hold 2 --apply

# 右转
sh trigger_light.sh trigger --base "$BASE" --direction right --hold 2 --apply

# 也支持中文方向参数
sh trigger_light.sh trigger --base "$BASE" --direction 左转 --hold 2 --apply
sh trigger_light.sh trigger --base "$BASE" --direction 右转 --hold 2 --apply
```

省略 `--apply` 只预演。脚本从实时 `purposes` 列表精确匹配：

| 参数 | functionCategory | functionName | functionValue |
| --- | --- | --- | --- |
| `left` / `左转` | `navigation` | `direction` | `left` |
| `right` / `右转` | `navigation` | `direction` | `right` |

匹配结果必须唯一。名称和 UUID 从服务读取，不写死具体固件的映射。`--hold` 范围是 0.1–5 秒，表示客户端等待撤销的时长，网络及服务调度会增加实际时间。

## 列表、诊断与其它灯效

```sh
sh trigger_light.sh list --base "$BASE"
sh trigger_light.sh probe --base "$BASE"
sh trigger_light.sh trigger --base "$BASE" --purpose '从列表取得的名称或UUID' --hold 2
sh trigger_light.sh --help
```

`list` 自动读取同源、同资源分页。`probe` 只读 configurations、versions、settinggroups、purposes、queuedpurposes 和 players。`--purpose-uri URI` 可按真实用途 URI 选择；与 `--direction`、`--purpose` 三选一。

## 状态与清理

默认每次生成新的 `/tmp/smartlight-随机值.json`，完整路径出现在 `state_saved` 日志中。也可指定一个尚不存在的状态文件：

```sh
sh trigger_light.sh trigger --base "$BASE" --direction left --hold 2 --apply \
  --state /tmp/light-left-new.json

# 清理异常中断的那次请求；填写日志中记录的真实状态文件
sh rollback.sh --state /tmp/light-left-new.json
```

状态保存于 POST 之前，返回的 `Location` 队列 URI 会在解析响应 JSON 前保存。清理只撤销该 URI 对应的请求，并确认其消失；保留其它同名或不同名请求。若测试前已有同名请求，脚本停止，不重复入队。

SIGINT/SIGTERM 会尽力清理；POST 期间收到信号时先完成有超时限制的请求、保存身份，再清理退出。SIGKILL、断电和网络故障需要使用保留的状态文件恢复。

结果不明且缺少 `Location` 时，脚本保留状态并报告 `cleanup_pending`，不会重试 POST 或按公共名称猜测删除目标。具体处理见 [排错说明](docs/troubleshooting.md)。

## 凭据与服务地址映射

服务需要鉴权时，准备权限为 600 的文件，内容为完整 Authorization 值，再添加 `--auth-file /path/to/auth.txt`。请通过正常方式取得凭据。

已有代理或转发的服务若在响应中广播另一个前缀，可显式指定：

```sh
sh trigger_light.sh list --base "$BASE" \
  --advertised-base 'http://[::1]:49520/smartlight/'
```

curl 禁用默认配置、代理变量、重定向和重试，保留 TLS 证书校验。凭据不会保存到状态文件或日志。

## 验证

```sh
python3 tests/test_trigger.py
```

开发机需要 Python 3.10+ 与 curl。测试只访问本机回环模拟服务，覆盖真实名称与相对 URI、最简 201 响应、Location 身份、方向参数、分页、中断、失败清理及其它队列项保持。生成报告写入 Git 忽略的 `artifacts/`。

2026-11-15 的现场验证已确认精简 Python 2.7.15 可运行；左转请求返回 201，播放器报告播放，撤销返回 200，实体亮灯由现场使用者确认。右转已完成只读预演。这些结果不代表所有车型、固件或电影资源包都兼容。日志 `physical_light_verified=false` 表示客户端本身无法观察实体灯带。

更多验证方法见 [测试文档](docs/testing.md)。代码结构与请求身份设计见 [设计说明](docs/design.md)。

## 许可

本项目代码采用 [MIT License](LICENSE)。随附 CPython 文件保留原许可，见 [第三方声明](THIRD_PARTY_NOTICES.md)。
