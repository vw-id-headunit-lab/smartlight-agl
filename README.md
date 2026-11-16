# smartlight-agl

[![CI](https://github.com/vw-id-headunit-lab/smartlight-agl/actions/workflows/ci.yml/badge.svg)](https://github.com/vw-id-headunit-lab/smartlight-agl/actions/workflows/ci.yml)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

从 AGL 通过现有 SmartLight HTTP 服务短时请求 ID.Light 灯效。支持左转、右转、灯效列表、只读诊断及独立清理，兼容精简 Python 2.7 和 Python 3。

新增 **CarPlay 导航联动**：读取已有 `simplenavigation` 服务的当前 CarPlay 机动，在 `execute` 阶段短时请求相应灯效。默认仍为只读预演；无需修改手机高德、解析 iAP2 或写车辆总线。详见 [CarPlay 接入与回退](docs/carplay.md)。

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
├── carplay_monitor.py
├── carplay_light.sh
└── vendor/
```

也可在开发机运行 `python3 tools/build_release.py` 生成 `dist/smartlight-agl.tar.gz`，将其复制到目标系统后解压。发布包包含运行脚本、使用说明和第三方许可。

## 设置服务地址

当前提供的 SmartLight 主机地址如下，保留 `/smartlight/` 服务前缀：

```sh
HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"
sh trigger_light.sh list --base "$BASE"
```

IPv6 地址必须带方括号。单引号内直接写 `http://`，不要写成 `http\://`；反斜杠会被保留到变量值中，导致 URI 无效。更换设备时按其实际服务地址调整 `HOST`。协议以实际服务为准；已验证的固件在此端口提供 HTTP。

AGL 与 QNX 的 `[::1]` 分别指向各自系统，不能直接互换。脚本不发现主机或创建跨系统转发。

## CarPlay 导航联动使用方法

此功能使用 **iPhone 经 CarPlay 提供的导航**，不是车机 Android 分区里的高德 APK。
先连接 CarPlay 并在手机高德中开始导航；车机还需提供可读取的
`simplenavigation` 服务，以及可访问的 SmartLight 服务。

### 1. 配置两套服务地址

沿用上面设置的 SmartLight 地址 `BASE`，另行设置导航服务地址：

```sh
# SmartLight 使用当前提供的地址
HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"
# 导航服务：交给注册中心解析（推荐）
REGISTRY='http://[::1]:80'
```

**不要把导航服务端口写死或猜。** 车机上有一个 VIWI 服务注册中心，
它只回答 `GET /?name=<服务名>`，**其它路径一律 307 重定向** ——
所以把 `?name=` 以外的地址当成服务地址会得到 `HTTP 307`。

查实际地址：

```sh
curl -g --noproxy '*' "$REGISTRY/?name=simplenavigation"
# → {"data":[{"name":"simplenavigation","uri":"http://[::1]:49533/simplenavigation/"}]}
```

两个参数不能省：`-g`（否则 `[::1]` 会被 curl 当成通配符）、`--noproxy '*'`（否则可能走代理）。

脚本也能自己解析：给 `--nav-registry` 即可，**不需要再传 `--nav-base`**。

两套服务可能位于不同主机或端口：导航桥在车机本机（`[::1]`），
SmartLight 在另一台 VM（`fd53:7cb8:383:3::73`）；脚本不建立 AGL/QNX 转发。

### 2. 先只读预演

```sh
# 读取 10 次当前导航状态后退出，不创建灯效请求
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" --iterations 10
```

检查 `carplay_snapshot` 日志：开始导航后应能读到当前机动的 `stage` 和
`direction`。只有 `execute` 阶段且方向匹配 left/right 才会出现
`carplay_plan`；未加 `--apply` 时仍是预演，不会点灯。
没有活跃 CarPlay 导航时 `active=false`；直行或未识别方向不会触发转向灯效。

### 3. 启用持续联动

```sh
# 持续运行；每个新机动至多请求一次，默认目标保持 2 秒后撤销
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --apply --interval 0.5 --hold 2 --state-dir /tmp/smartlight-carplay
```

程序按 `nextManeuver` 读取**当前机动**，只处理
`technology=appleCarPlay`、导航 active 且阶段为 `execute` 的左右转向。
持续收到同一机动不会重复入队；停止导航、换路、换向、错误或退出时会清理本次请求。
灯效从实时 `purposes` 列表匹配，不写死电影编号、名称或 UUID。

### 4. 服务需要鉴权时

分别准备权限为 600 的凭据文件，内容为完整的 Authorization 值：

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --auth-file /path/to/smartlight-header.txt \
  --nav-auth-file /path/to/navigation-header.txt \
  --apply --state-dir /tmp/smartlight-carplay
```

两侧凭据互不继承；只给需要鉴权的一侧传入对应参数。已有代理/转发导致响应中的
URI 前缀与访问地址不同，可分别使用 `--advertised-base` 和
`--nav-advertised-base`，详见 [CarPlay 接入与回退](docs/carplay.md)。

### 5. 停止与异常清理

前台运行时按 **Ctrl-C**，或向监控进程发送 SIGTERM；程序会尝试撤销自己创建的请求。
异常中断后，使用 `state_saved` / `cleanup_pending` 日志中记录的实际状态文件：

```sh
# 替换为日志中的真实文件路径；需要鉴权时另加 --auth-file
sh rollback.sh --state '/tmp/smartlight-carplay/carplay-实际随机值.json'

# 查看全部参数
sh carplay_light.sh --help
```

每个状态目录只运行一个 `--apply` 实例。有未完成状态或遗留锁时，先清理并确认旧进程
已经结束，再按 [锁与恢复说明](docs/carplay.md#队列身份和回退) 操作，不要绕过锁重复启动。

### 常用参数

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `--base` | 必填 | 当前系统可达的 SmartLight 基础 URI |
| `--nav-base` | 必填 | 当前系统可达的 simplenavigation 基础 URI |
| `--apply` | 关闭 | 实际创建请求；省略时只读预演 |
| `--interval` | `0.5` | 两轮读取间隔，0.1–5 秒；HTTP 请求本身另占时间 |
| `--hold` | `2` | 单次灯效请求目标保持时长，0.1–5 秒 |
| `--timeout` | `2` | 每个 HTTP 请求超时，0.1–15 秒 |
| `--iterations` | `0` | 轮询次数；0 表示持续运行 |
| `--state-dir` | 临时目录下的 `smartlight-carplay` | 保存队列身份和运行锁，优先使用 TMPDIR |

`--hold` 是客户端时长，不是服务端 TTL，网络和撤销确认会增加实际时间。
当前方向映射针对 S2995 平台；其他固件先核对预演结果。新增 CarPlay 功能已通过
本地回环测试，尚未进行实车联动验证；下方现场记录仅属于原手动灯效功能。

## 手动触发：左转与右转

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
python3 tests/test_carplay.py
sh -n trigger_light.sh
sh -n rollback.sh
sh -n carplay_light.sh
```

开发机需要 Python 3.10+ 与 curl。测试只访问本机回环模拟服务，覆盖真实名称与相对 URI、最简 201 响应、Location 身份、方向参数、分页、中断、失败清理及其它队列项保持。生成报告写入 Git 忽略的 `artifacts/`。

2026-11-15 的现场验证已确认精简 Python 2.7.15 可运行；左转请求返回 201，播放器报告播放，撤销返回 200，实体亮灯由现场使用者确认。右转已完成只读预演。这些结果不代表所有车型、固件或电影资源包都兼容。日志 `physical_light_verified=false` 表示客户端本身无法观察实体灯带。

更多验证方法见 [测试文档](docs/testing.md)。代码结构与请求身份设计见 [设计说明](docs/design.md)。

## 许可

本项目代码采用 [MIT License](LICENSE)。随附 CPython 文件保留原许可，见 [第三方声明](THIRD_PARTY_NOTICES.md)。
