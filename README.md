# SmartLight via CarPlay — CarPlay 导航联动 ID.Light

[简体中文](README.md) | [English](README.en.md)

让 **iPhone 高德等 CarPlay 导航的当前转向信息联动 ID.Light 灯带**。
程序读取车机已有的 `simplenavigation` 服务，再通过 SmartLight 请求灯效；
不抓屏、不解析原始 iAP2，也不需要修改手机 App。

**正常使用流程：部署 Go 运行包 → 配置地址 → 只读预演 → 启用联动。**
目标车机不需要 Python、curl 或 Go 编译器。手动触发只是排查灯带链路的调试工具，
不代替自动联动，见[调试说明](docs/debugging.md)。

> 这是社区工具，使用现有服务访问方式，不修改固件、亮度或功能开关。
> 以下功能在本地模拟服务验证；Go 版本尚未完成实车联动验证。先在停车状态核对。

## 1. 准备并部署

需要：

- iPhone 已连接 CarPlay，且高德等导航正在提供导航信息。
- 车机存在可读取的 `simplenavigation` 服务和可访问的 SmartLight 服务。
- HMI 中已开启需要的 ID.Light 导航/转向提示功能。
- 与车机架构匹配的运行包：本项目默认构建静态 Linux ARM64。

在开发机安装 Go 1.22+ 后，从项目根目录构建运行包：

```sh
go run ./cmd/build-release
```

输出 `dist/smartlight-agl-go-linux-arm64.tar.gz` 和 `.sha256`。
将运行包复制到车机选定目录后解压，**后续命令均在解压出的目录内运行**：

```sh
tar -xzf smartlight-agl-go-linux-arm64.tar.gz
cd smartlight-agl
```

保持 `smartlight-agl` 二进制、`carplay_light.sh`、`rollback.sh`、`trigger_light.sh`
和可选的 `boot-start.sh` 在同一级。不要把开发机上的 Windows 可执行文件复制到 AGL。

### 从 GitHub Release 下载

`main` 每次推送，或在 `main` 上手动运行 CI，均在 Go 与 Python 检查全部通过后
自动发布 [GitHub Release](https://github.com/vw-id-headunit-lab/smartlight-agl/releases)。
选择 `main-<完整提交 SHA>` 对应的版本，正常联动下载 **`smartlight-agl-go-linux-arm64.tar.gz`**
及其 `.sha256`，不要把 Python 调试包当作 CarPlay 运行包。

```sh
sha256sum -c smartlight-agl-go-linux-arm64.tar.gz.sha256
```

PR、其它分支、标签推送和失败的 CI 不发布。相同提交重跑会更新其 Release 及同名附件，
不会每次创建重复版本；测试日志仍只保存为 Actions artifact，不上传到 Release。
CI 自动发布不等于实车验证，最终使用前仍应按下面步骤预演。

## 2. 配置灯效地址与导航服务发现

在当前 shell 中设置：

```sh
# 当前提供的 SmartLight 地址
HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"

# 在车机 AGL 环境中查询导航服务的注册中心
REGISTRY='http://[::1]:80'
```

IPv6 地址保留方括号，直接写 `http://`，不要写 `http\://`。
`--nav-registry` 会查询 `/?name=simplenavigation` 并使用返回的实际导航 URI；
**不要把 SmartLight 的 49520 端口套用到导航服务，也不要把注册中心当成导航服务。**

`[::1]` 指当前系统的回环。以上导航注册中心地址用于车机本机，不是电脑或 Android
分区的回环；程序不会自动创建 AGL/QNX 跨系统转发。

## 3. 先只读预演

连接 CarPlay，在手机导航里开始一条路线，然后运行：

```sh
# 读取 10 轮后退出；没有 --apply，不创建灯效请求
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" --iterations 10
```

观察 JSON 日志：

| 日志 | 看什么 |
|---|---|
| `carplay_started` | `apply` 应为 `false`，数据源为已有导航服务 |
| `carplay_snapshot` | `active`、`stage`、`direction`，以及可用时的 `distance_m` |
| `plan` / `dry_run` | 满足触发条件时选中了实际灯效用途，但没有写入 |

未发现活跃 CarPlay 导航时 `active=false`，不是要求手动发一个左转请求。
如果没有读到当前机动，先检查 CarPlay 导航、注册中心返回地址、访问凭据和网络上下文；
不要直接加 `--apply` 掩盖读取问题。

## 4. 启用自动联动

确认预演中的方向和导航状态符合当前路线后，运行：

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --apply --interval 2 --hold 3 --max-duration 30 \
  --state-dir /tmp/smartlight-carplay
```

也可直接调用二进制，行为相同：

```sh
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" \
  --apply --interval 2 --hold 3 --max-duration 30 \
  --state-dir /tmp/smartlight-carplay
```

> **闪屏提示：建议使用 `--interval 2 --hold 3`（也是当前默认值）。**
> 如果 `--interval` 小于 2 秒，或 `--hold` 小于 3 秒，可能导致屏幕闪屏。

这是 **iPhone 经 CarPlay 提供的导航**，不是车机 Android 分区里独立运行的高德 APK。
程序只跟随 active `appleCarPlay` 的当前 `nextManeuver`，不播放未来路口。
灯效用途从实时目录按左右语义匹配，不写死电影编号或资源 UUID。

### 默认什么时候提示

- `execute` 阶段，或 `follow` 且合法剩余距离不超过当前阈值。
- **默认启用车速自适应**：`距离 = 时速(km/h) ÷ 3.6 × 12秒`，限制在 **50–500 米**；车速不可用时回退 **150 米**。
- 距离单位 `m/km/mi/yd/ft` 会换算到米；直行、未知方向及不合法距离不会猜测为转向。
- 每个脉冲目标保持 3 秒，先撤销旧请求；仍符合条件时可重新请求。
- 连续同一转向窗口最多 30 秒，机动元素 ID 刷新不会重置上限。
- 远离路口、导航停止、非触发阶段、方向或上下文变化时结束该窗口。

这是当前 S2995 平台策略，不表示所有车型或 CarPlay 实现采用相同编码/阈值。
`--hold` 是客户端撤销时机，不是服务端 TTL；网络和清理确认会影响实际时长。

### 按需调整

| 参数 | 默认值 | 作用 |
|---|---|---|
| `--interval` | `2` 秒 | 两轮读取的等待时间，HTTP 请求本身另占时间；低于 2 秒可能闪屏 |
| `--hold` | `3` 秒 | 单个灯效请求的目标保持时长，范围 0.1–5 秒；低于 3 秒可能闪屏 |
| `--max-duration` | `30` 秒 | 连续语义转向窗口上限，范围 1–300 秒 |
| `--follow-distance` | `150` 米 | 车速不可用时的回退阈值；关闭自适应时为固定阈值；0 只接受 execute |
| `--adaptive-follow` | 开启 | 按车速调整 follow 阈值；`--adaptive-follow=false` 关闭 |
| `--one-shot` | 关闭 | 每个语义转向窗口只播放一个脉冲，不重复请求 |
| `--timeout` | `2` 秒 | 每个 HTTP 请求的超时；GET 传输失败最多尝试 3 次 |
| `--iterations` | `0` | 0 持续运行，正整数为读取轮数 |
| `--state-dir` | 临时目录下的 `smartlight-carplay` | 本次请求身份和监控锁目录 |

### 默认车速自适应

| 当前时速 | 默认 follow 阈值 |
|---|---|
| 0–15 km/h | 50 米（下限） |
| 30 km/h | 100 米 |
| 60 km/h | 200 米 |
| 90 km/h | 300 米 |
| 120 km/h | 400 米 |
| ≥150 km/h | 500 米（上限） |

这是项目可调策略，不是原厂参数，也不识别道路类型；同样时速采用同样阈值。
默认只读发现入口是 `http://[fd53:7cb8:383:3::99]:8080/`，沿服务返回的 URI
查找 `Displayspeed` 下 `type=velocityPrimary` 的 `currentValue`，不猜测资源名/UUID。
`--speed-unit auto` 只接受响应明确提供的 `unit=km/h|mph|m/s`；缺少单位则回退，
经实车只读确认后可显式指定单位。入口可达性和真实响应格式仍需实车核对。

已确认速度 URI 和单位时，推荐跳过发现，先只读预演：

```sh
SPEED_URI='http://[fd53:7cb8:383:3::99]:8080/实际返回的速度资源URI'
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --speed-uri "$SPEED_URI" --speed-unit km/h --iterations 10
```

示例 `km/h` 须与真实 `currentValue` 单位一致；也支持 `mph`、`m/s`。
速度凭据独立使用 `--speed-auth-file`，不继承灯效/导航凭据。日志 `adaptive_follow` 和
`carplay_snapshot` 中的 `speed_available`、`speed_kph`、`follow_distance_m` 显示当前决策。

| 参数 | 默认 | 含义 |
|---|---|---|
| `--speed-base` | 上述 `::99:8080` 入口 | 没有 `--speed-uri` 时使用的服务树 |
| `--speed-unit` | `auto` | 从响应读取单位，或显式指定已确认单位 |
| `--follow-lead-time` | 12 秒 | 以当前速度预计的提前时间，范围 1–30 秒 |
| `--follow-min-distance` / `--follow-max-distance` | 50 / 500 米 | 自适应上下限，范围 1–1000 米 |
| `--speed-interval` | 2 秒 | 一次读取完成到下次读取的最短间隔，至少 1 秒 |
| `--speed-timeout` | 1 秒 | 一轮车速读取/发现的总预算，含 GET 重试 |
| `--speed-max-age` | 5 秒 | 本地成功接收样本的有效期；至少 interval+timeout |

读取失败即丢弃旧速度并回退；不会无限沿用高速值。有效期按本地接收时间计算，
**不证明上游传感器仍在更新**。车速变化只调整 follow，不改变 execute、3 秒保持和
30 秒总窗口；阈值下降会撤销不再符合条件的脉冲，但不会仅因车速波动重新计算同转向预算。

关闭自适应并使用固定 150 米（关闭后不请求车速服务）：

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --adaptive-follow=false --follow-distance 150 --iterations 10
```

例如，只在 execute 阶段提示且每个窗口只发一次：

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --apply --follow-distance 0 --one-shot --state-dir /tmp/smartlight-carplay
```

## 5. 停止与恢复

前台运行时按 **Ctrl-C**，或向监控进程发送 SIGTERM。程序会停止读取并尝试撤销
自己创建的灯效项，不清空其它业务的队列。

断电、SIGKILL、网络故障等可能留下待恢复状态。保留 `state_saved` /
`cleanup_pending` 日志，使用其中的**真实文件路径**：

```sh
sh rollback.sh --state '/tmp/smartlight-carplay/carplay-实际随机值.json'
```

需要灯效凭据时给清理命令加 `--auth-file /path/to/light-header.txt`。
不要同时运行多个联动实例，也不要通过换状态目录绕过未完成请求或遗留锁。

- `carplay_post_ignored`：该次 POST 失败后按正常节奏继续；无 Location 的意图
  会归档为 `ignored-post-*.json`，**不代表服务端一定没有接受请求**。
- 已知 Location 但清理失败：保留请求身份并退出 **78**，先恢复再重启。
- 需要处理遗留锁时，先确认旧进程结束并恢复未完成项，再按[恢复说明](docs/carplay.md)
  移除空锁目录；不要按公共用途名称猜测删除目标。

## 6. 可选：开机启动

先完成前台预演和联动验证。`boot-start.sh` 用于由已有 boot hook 调用，
或在目标车机上手动启动独立 systemd 服务：

```sh
# 会创建/启动服务并启用 --apply，不是只读检查
sh boot-start.sh
```

它写入 `/run/systemd/system/smartlight-carplay.service`，使用脚本内的 HOST 与
本机注册中心，日志为 `/tmp/carplay-light.log`（启动时超过 256 KiB 会轮换到 `.1`）。普通失败 5 秒后重启，
退出 78 时停止自动重启。它不会自动修改现有 boot hook；`/run` 中的服务文件也不跨重启持久保存。
有鉴权或自定义地址时先核对启动脚本参数，不要直接套用无凭据的启动配置。

`/tmp` 日志可能随重启丢失；部署目录中的旧日志不会自动搬移或删除。
取速请求日志带 `source=speed`。`transport_error.error_kind` 区分 `timeout`、
`connection_refused`、`network_unreachable`、`dns_error` 等；`speed_read_timeout`
表示本轮取速总预算耗尽。导航 GET 的 200 不代表车速读取成功。

```sh
systemctl status smartlight-carplay.service
systemctl stop smartlight-carplay.service
```

不需要开机联动就不要运行 `boot-start.sh`。详细安装边界和恢复步骤见
[CarPlay 接入说明](docs/carplay.md)。

## 7. 鉴权或已知导航 URI

服务需要鉴权时，分别提供权限为 600、内容为完整 Authorization 值的文件：

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --auth-file /path/to/light-header.txt \
  --nav-auth-file /path/to/navigation-header.txt \
  --nav-registry-auth-file /path/to/registry-header.txt --apply
```

只给需要的一侧传参，灯效、导航和注册中心的凭据互不继承，不写进日志或状态文件。
如果已经取得实际导航服务 URI，可以**替代**注册中心参数：

```sh
# 主机、端口须替换为已确认的导航服务地址，不猜测端口
NAV_BASE='http://HEADUNIT_HOST:NAV_PORT/simplenavigation/'
sh carplay_light.sh --base "$BASE" --nav-base "$NAV_BASE" --iterations 10
```

`--nav-base` 与 `--nav-registry` 必须二选一。已有代理/转发使响应广播的 URI 前缀不同时，
使用 `--advertised-base` / `--nav-advertised-base` 显式映射；工具不自动创建转发。

## 调试与开发资料

**手动亮灯只用于确认灯效服务链路，不是日常 CarPlay 联动步骤。**

- [调试工具](docs/debugging.md)：灯效目录/诊断、短时手动触发和独立 Python 触发工具。
- [CarPlay 参数与恢复](docs/carplay.md)：来源复核、平台方向映射、队列身份和启动边界。
- [测试](docs/testing.md) · [设计](docs/design.md) · [迁移说明](docs/go-migration.md) · [排错](docs/troubleshooting.md)。

开发机自检：`go test -count=1 ./...`、`go vet ./...`；支持 race 的工具链可执行
`go test -race ./...`。这些测试访问本机模拟服务，交叉编译和测试通过不等于实车灯带验证。

## 许可

项目自有代码采用 [MIT](LICENSE)。Go 运行包不依赖 Python，并附带实际构建工具链的
Go/标准库许可及来源哈希；独立 Python 调试包另保留 CPython 解析器许可。
两类包的许可范围见[第三方声明](THIRD_PARTY_NOTICES.md)。
