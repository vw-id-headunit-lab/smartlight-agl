# CarPlay 导航联动

## 数据路径

```text
iPhone 导航 → CarPlay/iAP2 → 车机已有 SPI 服务
  → simplenavigation.drivingContexts.nextManeuver
  → carplay_monitor.py → 原项目 SmartLight HTTP/持久化/清理
  → navigation + direction + left/right 实时用途 → ID.Light
```

此功能针对 iPhone 经 CarPlay 提供的导航，不是 Android 本地高德 APK。
只使用系统已经发布的结构化数据，不截取仪表画面、不注入 iAP2、不修改
原车导航服务，也不改变亮度或功能开关。

## 运行文件

`carplay_light.sh`、`carplay_monitor.py` 与原项目的 `smartlight_trigger.py`、
`agl_runtime.py`、`vendor/` 必须保持同级。要求与原工具一致：AGL/POSIX、
Python 2.7 或 3、curl；新增功能在车机端不依赖 pip、argparse、subprocess、
threading 或 Python 网络模块。默认轮询避免假定一个尚未验证的 WS 帧格式。

## 服务地址与凭据

SmartLight 使用当前提供的主机地址；导航服务仍按其实际注册信息填写。
两套基础 URI 保留各自服务前缀：

```sh
HOST='http://[fd53:7cb8:383:3::73]:49520'
LIGHT="$HOST/smartlight/"
NAV='http://HEADUNIT_HOST:NAV_PORT/simplenavigation/'

# 预演：轮询当前 CarPlay 机动，零灯效写入
sh carplay_light.sh --base "$LIGHT" --nav-base "$NAV"

# 实际联动：每个新机动至多一次短请求，默认 2 秒后撤销
sh carplay_light.sh --base "$LIGHT" --nav-base "$NAV" \
  --apply --interval 0.5 --hold 2 --state-dir /tmp/carplay-light-session
```

`HOST` 是 SmartLight 地址，单引号内使用 `http://`，不要加反斜杠。
`NAV` 中的 `HEADUNIT_HOST` 和 `NAV_PORT` 仍为导航服务占位值；未确认它与
SmartLight 共用端口。脚本不会自动建立 AGL/QNX 转发，也不把
Android/电脑的 `[::1]` 当成 QNX 回环。

需要鉴权时分别指定：

```sh
sh carplay_light.sh --base "$LIGHT" --nav-base "$NAV" \
  --auth-file /path/to/light-header.txt \
  --nav-auth-file /path/to/navigation-header.txt --apply
```

两侧凭据互不继承，导航凭据不会发往灯效服务。它们遵循现有服务访问方式，
日志和状态文件不保存凭据。服务返回另一个 URI 前缀时，显式提供
`--advertised-base` / `--nav-advertised-base`，两者独立校验。

## 当前机动与触发规则

1. 只选择 `technology="appleCarPlay"` 且 `isRouteGuidanceActive=true` 的
   drivingContext；多个活跃上下文视为来源歧义，停止并清理本桥请求。
2. 跟随 `nextManeuver.uri` 读取真实单项；不按 UUID 拼地址、不处理未来机动。
3. 使用字符串阶段 `follow/prepare/instruction/execute`；仅 `execute` 触发。
   CarPlay 内部阶段 3 和原厂 externalguidance 的阶段 3 含义不同，禁止裸数值透传。
4. light preflight 后、POST 前再次读取当前来源，避免预检期间退出/换路仍触发旧灯。
5. 每个上下文/机动/方向组合最多触发一次；持续 execute 不重复 POST。
   达到 `--hold` 后撤销；停止、非 execute、换向、换路、错误和 SIGTERM 都清理。
6. 不刷新灯效来无限延长请求，也不为了补发而清空其它业务队列。
7. execute 观察窗口超过 `--max-duration`（默认 30 秒）后不再触发；有限历史拒绝
   同一导航会话的旧机动回放。follow/prepare 的长时间等待不消耗 execute 窗口。
   已确认 inactive 后开启新会话，允许服务器在下一次导航复用机动 UUID/索引。

### S2995 平台方向映射

本映射来自原 SmartLightClient 的 ExternalGuidanceAccess 构造常量与
HandleDirection 分支，映射到**方向语义**后再实时查找用途，不写死 14/15 或 URI：

| mainType | directionType | 方向 |
|---|---|---|
| 13 | 32 / 64 / 96 | left |
| 13 | 160 / 192 / 224 | right |
| 25 / 35 | 64 | left |
| 25 / 35 | 192 | right |
| 16 / 27 | 任意平台方向值 | left |
| 15 / 26 | 任意平台方向值 | right |

直行、未知类型以及其它组合均不产生转向请求；环岛等未列语义不猜测。
这些数字是本固件的 RSI 平台编码，不能套用到 Apple CPManeuverType 或
高德 SDK 图标枚举。其它固件应先用预演及只读快照核对，不将单固件映射视为通用标准。

## 队列身份和回退

复用上游已验证的请求形状：真实 purpose `name`，相对 `purpose` 路径，
以及服务分配的 `Location`。POST 前落盘意图，收到 Location 后立即保存，
撤销只针对该 URI。既有同名请求保持不动；清理失败或 POST 结果不明时停止，
保留状态，不自动重试 POST，也不按公共名称批量删除。

正常退出用 Ctrl-C，或给监控进程发 SIGTERM；程序会清理当前请求。异常退出后，
使用 `state_saved` / `cleanup_pending` 日志中的实际文件：

```sh
sh rollback.sh --state /tmp/carplay-light-session/carplay-实际随机值.json
```

有未完成状态的目录会拒绝新的 `--apply` 启动，避免遗留项累积。
默认状态目录是 `/tmp/smartlight-carplay`（优先使用 TMPDIR），`--apply` 对目录
持有原子 `carplay-monitor.lock` 锁，防止同一状态目录重复启动。SIGKILL/断电
遗留锁时，先确认进程已结束并执行所有未完成状态的 rollback，再用
`rmdir /实际状态目录/carplay-monitor.lock` 删除空锁目录；脚本不自动抢占锁。
同一服务只运行一个监控实例，不要同时启动多套不同状态目录的监控。
本项目不替换原系统服务；停用新脚本即可恢复原来的手动触发工具和原车行为。

### 时限说明

`--hold` 为客户端目标时长，不是服务端 TTL。网络和删除确认会增加实际时长。
POSIX 定时器会中断慢导航 GET 后执行有界清理，避免三个导航 GET 超时叠加到灯效上。
网络故障、SIGKILL、断电仍需按持久化状态独立恢复；不宣称在这些情况下自动恢复已完成。
当前选择 GET 轮询，默认延迟 0.5 秒；可用 `--interval` 调整，需考虑服务负载。
`--iterations N` 允许有界采集/测试；0 表示持续运行。

## 验证

```sh
python3 tests/test_trigger.py
python3 tests/test_carplay.py
sh -n carplay_light.sh
python3 tools/build_release.py
```

新增测试只使用本地回环 HTTP 模拟服务：预演零写入、execute 左右匹配、停止和换向
清理、同机动去重、重算引用一致性、独立凭据、越源 URI、缺失 Location、
SIGTERM 和慢导航响应的清理，以及独立 rollback。保留原有灯效功能回归。

本次新增功能没有连接实车。上游 README 的现场记录属于原手动触发功能，
不等于本次 CarPlay 联动已获物理灯带验证。仍需在目标系统提供实际服务 URI 并核对
当前机动快照；先预演，再由使用者在静止条件确认灯效。
