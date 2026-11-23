# Go 运行时迁移与回退

基线 main `192cd8a`。Go1.22+，只使用标准库；CGO_ENABLED=0静态Linux ARM64。
目标不需要Go/Python/curl；没有改写原固件或系统服务。本轮未连接车辆。

## CLI与部署

| 原Python命令 | Go命令 |
|---|---|
| python-trigger/smartlight_trigger.py list/probe/trigger/cleanup | smartlight-agl list/probe/trigger/cleanup |
| 已移除的 Python CarPlay monitor | smartlight-agl carplay |

三个shell启动器默认调用同目录Go二进制。`SMARTLIGHT_BIN`可指定其它可执行文件。
flags放在子命令后；默认只读，--apply才创建请求。开发机运行：

```sh
go test -count=1 ./...
go vet ./...
go run ./cmd/build-release
```

输出dist/smartlight-agl-go-linux-arm64.tar.gz与sha256。包只含静态二进制、启动器、
文档和许可，权限/时间戳固定；不含凭据、状态、固件、Python/vendor、本地报告。
`python-trigger/tools/build_release.py`仅构建独立Python手动触发包，不是Go包。

## 独立手动触发目录

删除重复的Python CarPlay实现及其测试，根目录三个启动器只使用Go。
Python仅保留手动list/probe/trigger/cleanup，并连同解析器、许可和测试移入
`python-trigger/`。该目录可独立复制，不需要Go或根目录文件：

```sh
sh python-trigger/trigger_light.sh list --base "$BASE"
sh python-trigger/trigger_light.sh trigger --base "$BASE" --direction left --hold 2 --apply
sh python-trigger/rollback.sh --state /tmp/实际状态.json
python3 python-trigger/tests/test_trigger.py
python3 python-trigger/tools/build_release.py
```

不再支持通过`SMARTLIGHT_RUNTIME=python`选择根目录脚本的运行时。
独立Python包不含CarPlay，输出`python-trigger/dist/smartlight-python-trigger.tar.gz`；
用法见[Python触发工具](../python-trigger/README.md)。切换工具前先停监控并清理状态。

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
2. 连续上下文和派生方向定义转弯周期。首次满足 follow 距离阈值、首次进入 execute 时各开始 15 秒的发起和补发窗口。
   元素 ID 刷新和同一周期内的阶段回退沿用已开始的计时。
3. 保留仍在队列中的准确请求，服务移除后可在开放窗口内补发。hold 为完成检查和重试间隔。
   `--one-shot` 只在 execute 提示一次并按 hold 撤销。`--follow-distance 0` 关闭 follow 触发。
4. --nav-base/--nav-registry二选一；新增--nav-registry-auth-file，与导航/灯效凭据独立。
5. 数字参数必须有限，来源歧义/schema/URI不合要求时清理本桥项并退出。

## 实现结构

cmd/smartlight-agl负责signal-aware CLI；internal/smartlight/client.go负责标准库HTTP、
原点/前缀/分页/registry；state.go负责选择、落盘、Location和恢复；navigation.go负责
源快照、语义重校验、单位、计时与锁；app.go组合独立凭据和生命周期context。
cmd/build-release负责静态交叉编译和确定性运行包。

阶段窗口约束补发所需的队列检查和 POST 前校验。阶段到期后保留已接受的请求并继续轮询导航。
独立清理确认先于下一 POST。有界 POST 完成身份落盘后再响应
Ctrl-C/SIGTERM，清理使用独立有界context。断电、SIGKILL、网络失联和未知POST完成
结果仍需独立恢复；hold不是服务端TTL，player也不证明物理亮灯。

## 测试边界

Go tests仅访问本地httptest：默认预演、POST真实name/相对purpose、Location、
坏JSON/HTTP500恢复、缺身份、原请求保持、禁用组、URI/IPv6/遍历、分页循环、
GET重试/无重定向、registry独立凭据、距离非法值、id刷新窗口、one-shot、完整CLI、
取消后恢复和锁。Linux CI另跑race并记录go-test-json。ARM64交叉编译不是目标执行。

独立Python手动触发测试不验证Go，Go CarPlay只由Go测试覆盖。

## 合并 main 1e1941b 的决策

保留Go-only CarPlay与独立Python触发目录，不复活已删除的Python监控/测试。
保留main新增boot-start.sh并放入Go包；它的78退出/停止重启约定由Go实现。
Go监控对单次POST失败按hold节奏继续，未知Location原意图改为归档证据，不无声删除。
若已有有效Location但清理失败，保留身份并退出78，不采用旧Python忘记已知请求的策略。
手动触发器行为不变；上游Python失败策略不会移动到python-trigger手动工具里。
