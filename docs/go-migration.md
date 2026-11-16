# Go 运行时迁移与回退

基线 main `192cd8a`。Go1.22+，只使用标准库；CGO_ENABLED=0静态Linux ARM64。
目标不需要Go/Python/curl；没有改写原固件或系统服务。本轮未连接车辆。

## CLI与部署

| 原Python命令 | Go命令 |
|---|---|
| smartlight_trigger.py list/probe/trigger/cleanup | smartlight-agl list/probe/trigger/cleanup |
| carplay_monitor.py | smartlight-agl carplay |

三个shell启动器默认调用同目录Go二进制。`SMARTLIGHT_BIN`可指定其它可执行文件。
flags放在子命令后；默认只读，--apply才创建请求。开发机运行：

```sh
go test -count=1 ./...
go vet ./...
go run ./cmd/build-release
```

输出dist/smartlight-agl-go-linux-arm64.tar.gz与sha256。包只含静态二进制、启动器、
文档和许可，权限/时间戳固定；不含凭据、状态、固件、Python/vendor、本地报告。
tools/build_release.py保留为旧Python包构建路线，不是Go包。

源码的Python及vendor原件未修改，保留显式回退，不装进Go-only包：

```sh
# 源码目录里有Python/vendor时才适用
SMARTLIGHT_RUNTIME=python sh trigger_light.sh list --base "$BASE"
SMARTLIGHT_RUNTIME=python sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY"
```

回退前先停监控并清理状态。旧Python保留原行为，回退不等于修复旧单位/窗口缺陷。

## 请求与状态保持

- GET/POST/DELETE、VIWI envelope/数组/单项、同资源分页及URI原点校验。
- POST仅真实purpose.name和相对purpose路径，不发随机请求名字或movie id。
- Location先验证落盘再解析JSON；status-only201可用，HTTP500/坏JSON后可精确恢复。
- v1/v2状态兼容，0600独占创建，更新sync+rename；基线重叠/未知身份/不匹配元数据拒绝清理。
- confirm-absent不绕过v2身份；POST/DELETE不自动重试，GET运输失败最多3次。
- 凭据在灯效/导航/注册中心三侧隔离，不落日志状态、不继承到registry返回服务。
- 无环境代理/重定向/关闭TLS校验选项，HTTPS仍需目标正常证书根。
- Go替换curl后header顺序/User-Agent不保证逐字节相同；业务字段和身份语义保持。

## 有意修正与新参数

1. follow的150阈值按米归一化。支持m/km/mi/yd/ft，拒绝bool/负/非有限/字符串/未知单位。
   0.2km是200m，不是0.2m。execute无距离仍可触发，若提供距离也必须合法。
2. 最大时长跟随连续上下文+派生左右方向，不因元素id/原方向变体刷新重置。
   无足够状态区分相邻同向转弯时不猜新窗口，仍受已有上限约束。
3. 默认与最新main统一为有界重复脉冲，不再写“只execute/每个机动一次”的旧描述。
   --one-shot选择窗口一次，--follow-distance0关闭follow策略，仅保留execute。
4. --nav-base/--nav-registry二选一；新增--nav-registry-auth-file，与导航/灯效凭据独立。
5. 数字参数必须有限，来源歧义/schema/URI不合要求时清理本桥项并退出。

## 实现结构

cmd/smartlight-agl负责signal-aware CLI；internal/smartlight/client.go负责标准库HTTP、
原点/前缀/分页/registry；state.go负责选择、落盘、Location和恢复；navigation.go负责
源快照、语义重校验、单位、计时与锁；app.go组合独立凭据和生命周期context。
cmd/build-release负责静态交叉编译和确定性运行包。

当前脉冲deadline约束慢来源GET；DELETE先于下一POST。有界POST完成身份落盘后再响应
Ctrl-C/SIGTERM，清理使用独立有界context。断电、SIGKILL、网络失联和未知POST完成
结果仍需独立恢复；hold不是服务端TTL，player也不证明物理亮灯。

## 测试边界

Go tests仅访问本地httptest：默认预演、POST真实name/相对purpose、Location、
坏JSON/HTTP500恢复、缺身份、原请求保持、禁用组、URI/IPv6/遍历、分页循环、
GET重试/无重定向、registry独立凭据、距离非法值、id刷新窗口、one-shot、完整CLI、
取消后恢复和锁。Linux CI另跑race并记录go-test-json。ARM64交叉编译不是目标执行。

原Python测试属于legacy参考；其shell测试需SMARTLIGHT_RUNTIME=python，不验证Go。
