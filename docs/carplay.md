# Go CarPlay导航联动

读取已有simplenavigation结构化数据，不抓屏、不解析iAP2、不改手机App。

```sh
HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"
REGISTRY='http://[::1]:80'
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" --iterations 10
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" --apply \
  --interval 2 --hold 3 --max-duration 15 --state-dir /tmp/smartlight-carplay
# 或采用已确认的service URI，和registry必须二选一
NAV_BASE='http://HEADUNIT_HOST:NAV_PORT/simplenavigation/'
sh carplay_light.sh --base "$BASE" --nav-base "$NAV_BASE" --iterations 10
```

> **建议使用 `--interval 2 --hold 3`，也是 CarPlay 命令和开机脚本的默认配置。**
> 如果 `--interval` 小于 2 秒，或 `--hold` 小于 3 秒，可能导致屏幕闪屏。
> 较小的值仍可用于本地模拟调试，不建议用于实车联动。

来源：active appleCarPlay context→nextManeuver真实uri→当前元素→重读context确认引用。
POST前重读当前机动，核对同上下文active、派生方向相同且eligible；允许同转弯id刷新。
读完机动后再次确认context与引用；期间停止、空引用或引用变化则跳过本次POST。
可恢复的快照变化仍按`--interval`等待，避免导航初始化或重算期间高频轮询。

S2995方向：mainType13的32/64/96=left，160/192/224=right；25/35的64/192=left/right；
16/27=left，15/26=right；其它组合不触发。实时查用途，不以14/15当URI。

部分车机不下发`stage`：实测 HCN1.1.134 在距转弯 0 米处仍返回空串，原逻辑会把它当致命错误退出。
此时按距离推导：≤50 米视为`execute`，其余视为`follow`；响应已提供`stage`时保持原语义。
该回退只影响阶段判定，方向白名单、去重、hold 窗口、状态与清理逻辑不变；没有距离可用时按可恢复的快照变化重试。
若车机把 CarLife 会话也上报为`technology: "appleCarPlay"`（实测车机如此），`activeContext` 会照常接受，
同一套阶段判定对 CarLife 同样生效；把 CarLife 标成其它`technology`的车机不在本回退覆盖范围。

默认execute或follow且距离≤当前阈值。默认车速自适应在60 km/h及以下按speed(km/h)/3.6×12秒计算，
60–150 km/h在60/200、90/500、120/1000、150/2000（km/h与米）之间线性插值，≥150 km/h使用2000米。
`--follow-lead-time`默认12，按参数值/12缩放曲线，再应用默认50–2000米的距离上下限；上下限可在1–2000米内配置。
车速未知回退150米。`--adaptive-follow=false`关闭自适应且不读取速度。
首个灯效请求成功后冻结触发时的距离阈值并暂停车速读取，SmartLight 按自身节奏播放。
周期内补发沿用冻结阈值。窗口到期或完成 one-shot 清理后，继续等待导航确认周期结束。
`--follow-distance 0`保持仅execute并跳过车速读取。单位m/km/mi/yd/ft归一化；拒绝bool/负/非有限/字符串/
未知单位。

首次满足 `follow` 距离阈值、首次进入 `execute` 时各计 15 秒。`--max-duration` 同时调整两段长度。
开放窗口内按至少 `hold` 间隔检查准确的队列项，服务已移除时才补发。窗口到期后停止新发和补发，
仍在队列中的灯效继续由 SmartLight 播放。阶段来回切换和元素 ID 刷新沿用已开始的计时。

远离触发范围、导航停用、确认无下一机动、进入 `prepare` 或 `instruction`，或切换来源或方向时结束周期并恢复采样。
未观测到边界的同向转弯继续共享周期。`--one-shot` 只在 `execute` 提示一次，按 `hold` 撤销。
在途 POST 按 HTTP 超时完成。成功时保存 Location，并在导航结束或监控退出时清理对应请求。

| 参数 | 默认 | 意义 |
|---|---|---|
| --base | 必填 | SmartLight service URI |
| --nav-base / --nav-registry | 二选一必填 | 实际导航服务/注册中心 |
| --apply | false | 创建请求，默认只读 |
| --interval | 2 | 轮询延迟0.1–5秒，HTTP另占时间；低于2秒可能闪屏 |
| --hold | 3 | 重复模式完成检查/重试最小间隔；one-shot保持时长，0.1–5秒，不是server TTL |
| --max-duration | 15 | 每个阶段的点亮和补发窗口，1–300 秒 |
| --follow-distance | 150 | 固定/车速失败回退米阈值0–1000；0关闭follow策略 |
| --adaptive-follow | true | 车速自适应；显式false关闭 |
| --one-shot | false | 每次转弯在 execute 提示一次 |
| --timeout | 2 | 每请求0.1–15秒；GET运输最多3次 |
| --iterations | 0 | 0持续，正数有界 |
| --state-dir | TMPDIR/smartlight-carplay | 状态与原子目录锁 |

注册中心GET /?name=simplenavigation，唯一返回uri才是service；不猜端口或混用回环。

```sh
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" \
  --auth-file /path/to/light-header.txt \
  --nav-auth-file /path/to/navigation-header.txt \
  --nav-registry-auth-file /path/to/registry-header.txt --apply
```

三侧凭据不继承、不入日志状态。prefix mapping用advertised-base/nav-advertised-base。
车速为独立GET数据源，默认从`http://[fd53:7cb8:383:3::99]:8080/`发现Displayspeed；
可用`--speed-uri`指定确认后的集合/元素。2026-11-17实车只读确认默认入口连接超时，
本机registry的`?name=DisplaySpeed`返回`http://[fd53:7cb8:383:3::73]:29721/DisplaySpeed/`，
其返回的车速集合为`/DisplaySpeed/speeds/`，boot-start.sh已显式配置此集合。
registry名称区分大小写，`Displayspeed`查不到此服务；其它车辆需重新查询实际URI。
`--speed-unit auto`要求返回unit，支持km/h、mph、m/s及VIWI的kmPerHour、milesPerHour；
也可显式传已确认的同义单位，仍拒绝km/h与mph冲突。独立凭据用speed-auth-file。
点亮前车速读取失败或过期时回退固定距离。暂停采样期间，`carplay_snapshot` 用
`speed_sampling_paused` 标记冻结的阈值和车速。完整参数见README双语教程。
代理访问前缀只用于HTTP请求地址；POST的purpose引用使用服务端路径，状态及清理保留访问路径。
Ctrl-C/SIGTERM停止来源读取；有界POST先存Location，独立cleanup context撤销本桥项。
慢GET受脉冲deadline限制；断电、SIGKILL、失联仍需独立恢复：

```sh
./smartlight-agl cleanup --state /tmp/日志给出的实际状态.json
sh rollback.sh --state /tmp/日志给出的实际状态.json
```

### 请求失败与持续运行

CarPlay的一次POST失败不再直接结束整场监控。若返回有效Location，先按准确URI
清理，清理成功后记录`carplay_post_ignored`并等待正常`--hold`节奏。
没有Location时不猜删除目标，将原意图保存为同目录`ignored-post-随机值.json`，
保留未知结果证据后继续监控；这不表示服务端已拒绝或队列已清空。
旧版本无Location的待处理状态在启动时同样归档，日志/证据供后续核对。

有Location但清理失败的请求仍保留原状态，退出码为**78**，不继续制造新请求。
这比旧Python main“清理失败也忘记身份”的策略更保守：保留可恢复的身份，避免遗留灯效项。
手动Go/Python触发与独立rollback继续保持原恢复规则，未知身份仍不会按名称删除。
HTTP层POST/DELETE没有重试，连续监控的后续脉冲会重新检查来源及现有队列。

已有明确未完成请求/锁会拒绝apply；确认旧进程结束、恢复状态后手动移除空
carplay-monitor.lock，不用另一目录重复运行。

### 可选开机启动

合入main的`boot-start.sh`用于现有`/lge/app_rw/ssh/boot-hook.sh`在后台调用。
它通过systemd运行独立Go监控服务，不依附SSH会话；**仅解压/执行help不会安装服务**。
确认Go二进制与脚本齐备并完成静止预演后，才手动运行：

```sh
sh boot-start.sh
```

脚本使用当前文档HOST和本机registry，自动加`--apply`，写入
`/run/systemd/system/smartlight-carplay.service`并调用systemctl启动；普通失败5秒后重启，
恢复错误78停止重启，`TimeoutStopSec=45s`。启动日志为`/tmp/carplay-light.log`，
启动时达到256 KiB轮换到`/tmp/carplay-light.log.1`；旧部署目录日志不自动搬移，重启可能清空/tmp。
速度HTTP日志带`source=speed`，传输失败用`error_kind`区分超时/拒绝连接/无路由等。
不需要开机联动时不要调用该脚本；停用用`systemctl stop smartlight-carplay.service`，
然后按状态清理。此轮只做shell语法/打包检查，未操作实机systemd或boot hook。

本轮Go仅本地模拟，未实车点灯。[迁移与回退](go-migration.md)。
