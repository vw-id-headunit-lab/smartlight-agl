# smartlight-agl — Go runtime

通过现有SmartLight HTTP服务请求ID.Light灯效，读取车机已有simplenavigation的CarPlay当前导航。
此分支默认运行时迁移为**单一Go二进制**，目标AGL不需要Python、curl或pip。
默认只读预演，只有--apply才创建请求；本轮仅本地模拟，未做实车Go联调。
不改写固件、亮度或功能开关，使用既有服务访问方式并在停车状态核对。

## 构建与安装

开发机需要Go1.22+，只用标准库，无外部模块依赖。

```sh
# 本机可执行文件
CGO_ENABLED=0 go build -trimpath -o smartlight-agl ./cmd/smartlight-agl

# AGL静态Linux ARM64二进制
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags='-s -w' -o dist/smartlight-agl ./cmd/smartlight-agl

# 含二进制、启动器、文档和许可的确定性运行包
go run ./cmd/build-release
```

默认输出dist/smartlight-agl-go-linux-arm64.tar.gz及sha256。
解包后smartlight-agl、trigger_light.sh、carplay_light.sh、rollback.sh及可选boot-start.sh保持同级。
目标不需要Go编译器。根目录三个启动器仅调用Go。
Python手动触发工具独立保存在[python-trigger/](python-trigger/README.md)，
不含CarPlay实现，也不装进Go运行包；两套工具无需相互依赖。

## 服务地址

保留用户提供的SmartLight地址：

```sh
HOST='http://[fd53:7cb8:383:3::73]:49520'
BASE="$HOST/smartlight/"
./smartlight-agl list --base "$BASE"
```

IPv6保留方括号，直接写http://，不要加反斜杠。AGL/QNX的[::1]不同，工具不建跨VM转发。

## 手动灯效

```sh
# 默认预演，无写入
./smartlight-agl trigger --base "$BASE" --direction left
# 实际请求，客户端2秒后撤销自己的队列项
./smartlight-agl trigger --base "$BASE" --direction right --hold 2 --apply
# 兼容shell形式，支持中文方向
sh trigger_light.sh trigger --base "$BASE" --direction 左转 --apply
./smartlight-agl probe --base "$BASE"
./smartlight-agl trigger --base "$BASE" --purpose '真实用途名称或资源ID'
./smartlight-agl trigger --base "$BASE" --purpose-uri '/smartlight/purposes/真实资源ID'
```

实时purposes唯一匹配navigation/direction/left或right。POST是实际purpose.name与相对路径，
不写死14/15或UUID。hold为0.1–5秒，是客户端目标时长，不是服务器TTL。

### 独立 Python 触发工具

需要精简Python 2.7/3 + curl版本时，可单独复制`python-trigger/`目录。
它只保留手动灯效和清理，不包含重复的CarPlay监控：

```sh
sh python-trigger/trigger_light.sh list --base "$BASE"
sh python-trigger/trigger_light.sh trigger --base "$BASE" --direction left --hold 2 --apply
sh python-trigger/rollback.sh --state /tmp/日志中的实际状态.json
```

完整参数、测试及独立打包见[Python触发说明](python-trigger/README.md)。
不再用`SMARTLIGHT_RUNTIME=python`切换根目录启动器。

## CarPlay联动

这是**iPhone经CarPlay提供的导航**，不是Android本地高德。连接CarPlay并开始导航：

```sh
REGISTRY='http://[::1]:80'
# 预演10次；注册中心解析导航服务
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" --iterations 10
# 持续实际联动
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" \
  --apply --interval 0.5 --hold 2 --max-duration 30 --state-dir /tmp/smartlight-carplay
# 已知实际导航地址时替代registry，必须二选一
NAV_BASE='http://HEADUNIT_HOST:NAV_PORT/simplenavigation/'
sh carplay_light.sh --base "$BASE" --nav-base "$NAV_BASE" --iterations 10
```

注册查询是GET /?name=simplenavigation，唯一匹配的返回uri才是service。
不猜导航端口，不把registry当service，HEADUNIT_HOST/NAV_PORT是占位值。

### 默认策略与有意修正

- 只处理active appleCarPlay当前nextManeuver；POST前复核来源及派生方向。
- 沿用最新main的有界重复脉冲：execute，或follow且合法剩余距离≤150**米**。
- m/km/mi/yd/ft归一化；0.2km是200m。拒绝bool/负/非有限/字符串/未知单位。
- hold到期先撤销旧项，仍eligible再新建脉冲，直到max-duration。
- 窗口按连续上下文+派生左右方向跟踪，不因元素id/raw方向变体刷新重置。
  far/inactive/prepare/instruction或方向/上下文变化结束窗口。
- 无可观测边界的相邻同向转弯不猜新窗口，仍受已有上限约束。
- --one-shot选择窗口一次；--follow-distance 0关闭follow策略，仅execute。

这明确修正旧Python的单位/异常值/窗口问题，不声称阈值是Apple或所有固件通用标准。
S2995平台方向映射在其它固件应先预演。[完整参数](docs/carplay.md)。

## 凭据、清理与回退

```sh
./smartlight-agl carplay --base "$BASE" --nav-registry "$REGISTRY" \
  --auth-file /path/to/light-header.txt \
  --nav-auth-file /path/to/navigation-header.txt \
  --nav-registry-auth-file /path/to/registry-header.txt --apply
```

三侧凭据独立不继承，不入日志状态。只为需要的一侧传参；前缀映射分别用
--advertised-base和--nav-advertised-base。无环境代理、重定向或不验证TLS选项。
只有GET运输失败最多3次，POST/DELETE不自动重试。

POST前落盘意图，Location先保存再解析JSON；只删准确队列URI，保留其他请求。
Ctrl-C/SIGTERM先完成有界POST身份记录再清理。断电/SIGKILL/网络故障按日志状态恢复：

```sh
./smartlight-agl cleanup --state /tmp/日志中的实际状态.json
sh rollback.sh --state /tmp/日志中的实际状态.json
```

兼容Python v1/v2状态；未知身份保留，不按公共名称删项，confirm-absent不绕过v2身份。
CarPlay目录锁和未完成状态会阻止重复apply；不自动抢占遗留锁。

CarPlay合入main的连续运行策略：一次失败POST在准确清理成功后可忽略；无Location时
归档`ignored-post-*.json`保留证据并继续，不能据此认定未入队。有Location但清理失败
仍保留状态并返回78。可选`boot-start.sh`显式运行时会创建systemd服务并启用`--apply`，
不是预演命令；恢复错误78会停止自动重启。详见[失败恢复与开机启动](docs/carplay.md)。

## 验证

```sh
go test -count=1 ./...
go vet ./...
go test -race ./...       # Linux CI；本机需支持race的工具链
go run ./cmd/build-release
```

只访问localhost httptest，未执行车控。交叉编译不是目标运行或物理灯带验证。
[迁移/设计/回退](docs/go-migration.md) · [测试](docs/testing.md) · [排错](docs/troubleshooting.md)。

## 许可

项目沿用[MIT](LICENSE)；Python解析模块位于`python-trigger/vendor/`，来源与许可见
[第三方声明](THIRD_PARTY_NOTICES.md)。Go运行包不依赖这些模块。
