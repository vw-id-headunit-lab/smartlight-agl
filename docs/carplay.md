# Go CarPlay导航联动

读取已有simplenavigation结构化数据，不抓屏、不解析iAP2、不改手机App。

```sh
HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"
REGISTRY='http://[::1]:80'
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" --iterations 10
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" --apply \
  --hold 2 --max-duration 30 --state-dir /tmp/smartlight-carplay
# 或采用已确认的service URI，和registry必须二选一
NAV_BASE='http://HEADUNIT_HOST:NAV_PORT/simplenavigation/'
sh carplay_light.sh --base "$BASE" --nav-base "$NAV_BASE" --iterations 10
```

来源：active appleCarPlay context→nextManeuver真实uri→当前元素→重读context确认引用。
POST前重读当前机动，核对同上下文active、派生方向相同且eligible；允许同转弯id刷新。

S2995方向：mainType13的32/64/96=left，160/192/224=right；25/35的64/192=left/right；
16/27=left，15/26=right；其它组合不触发。实时查用途，不以14/15当URI。

默认execute或follow且距离≤150米。单位m/km/mi/yd/ft归一化；拒绝bool/负/非有限/字符串/
未知单位。hold到期先DELETE，仍eligible可再次POST；连续上下文+左右方向窗口不因id刷新
重置。far/inactive/prepare/instruction或换来源/方向结束窗口；未观测到边界的同向转弯
继续共享窗口。--one-shot选窗口一次；--follow-distance 0仅execute。

| 参数 | 默认 | 意义 |
|---|---|---|
| --base | 必填 | SmartLight service URI |
| --nav-base / --nav-registry | 二选一必填 | 实际导航服务/注册中心 |
| --apply | false | 创建请求，默认只读 |
| --interval | 0.5 | 轮询延迟0.1–5秒，HTTP另占时间 |
| --hold | 2 | 脉冲目标时长0.1–5秒，不是server TTL |
| --max-duration | 30 | 连续语义窗口1–300秒，不随id刷新重置 |
| --follow-distance | 150 | 米阈值0–1000；0关闭follow策略 |
| --one-shot | false | 窗口一次 |
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
Ctrl-C/SIGTERM停止来源读取；有界POST先存Location，独立cleanup context撤销本桥项。
慢GET受脉冲deadline限制；断电、SIGKILL、失联仍需独立恢复：

```sh
./smartlight-agl cleanup --state /tmp/日志给出的实际状态.json
sh rollback.sh --state /tmp/日志给出的实际状态.json
```

缺Location保留pending，不按公共名称删项。已有状态/锁拒绝apply；确认旧进程结束、
恢复状态后手动移除空carplay-monitor.lock，不用另一目录重复运行。
本轮Go仅本地模拟，未实车点灯。[迁移与回退](go-migration.md)。
