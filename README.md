# SmartLight via CarPlay — CarPlay 导航联动 ID.Light

[English](README.en.md)

将 iPhone 高德等 CarPlay 导航的当前转向提示同步到 ID.Light 灯带。程序读取车机已有的 `simplenavigation` 服务，再通过 SmartLight 请求灯效；不抓屏、不解析原始 iAP2，也不修改固件或手机 App。

**Go 版已通过实车联动测试。** 不同车机的服务地址可能不同，首次部署请在停车状态先做只读预演。车机独立运行的 Android 导航不属于 CarPlay 数据源。

## 快速开始

1. 从 [GitHub Releases](https://github.com/vw-id-headunit-lab/smartlight-agl/releases) 下载 `smartlight-agl-go-linux-arm64.tar.gz` 和同名 `.sha256` 校验文件。也可在开发机用 Go 1.22+ 执行 `go run ./cmd/build-release`，产物在 `dist/`。车机无需 Python、curl 或 Go 编译器。
2. 校验后将运行包复制到 AGL 车机，并解压。下文命令在解压出的目录中运行：

   ```sh
   sha256sum -c smartlight-agl-go-linux-arm64.tar.gz.sha256
   tar -xzf smartlight-agl-go-linux-arm64.tar.gz
   cd smartlight-agl
   ```

3. 在车机 HMI 中开启 ID.Light 导航提示，连接 CarPlay 并开始导航。设置下列地址（`BASE` 按目标车机确认的地址修改），只读检查 10 轮：

   ```sh
   BASE='http://[fd53:7cb8:383:3::73]:49520/smartlight/'
   REGISTRY='http://[::1]:80'
   sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" --iterations 10
   ```

   `REGISTRY` 是车机 AGL 本机的服务注册中心，不是导航服务本身。确认 `carplay_started` 中 `apply=false`，以及 `carplay_snapshot` 中导航已激活、方向与当前路线一致。

4. 预演无误后，加 `--apply` 启用联动：

   ```sh
   sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
     --apply --interval 2 --hold 3 --state-dir /tmp/smartlight-carplay
   ```

   **不要为了提速而降低 `--interval 2` 或 `--hold 3`，否则可能闪屏。**

## 触发规则

- 仅跟随当前 CarPlay 转向：`execute` 阶段直接提示，`follow` 阶段需进入距离阈值。默认按车速分段计算，90 km/h 对应 500 米、120 km/h 对应 1 公里、≥150 km/h 对应 2 公里，范围为 50–2000 米；车速不可用时回退到 150 米。
- 首次灯效请求成功后，使用 SmartLight 原有播放节奏，固定本次转弯的距离阈值并暂停读取车速，直到导航确认转弯周期结束。
- 首次满足 `follow` 距离阈值、首次进入 `execute` 时各开一个默认 15 秒的点亮和补发窗口。默认至少间隔 3 秒检查队列，确认服务已移除本次请求才补发。窗口到期或阶段切换时，保留仍在队列中的灯效。
- `--one-shot` 把唯一一次提示留给 `execute`，默认保持 3 秒后撤销。`--follow-distance 0` 也仅在 `execute` 提示。
- 若默认车速入口不可用，从车机注册中心查找 `DisplaySpeed`（区分大小写），将确认的集合 URI 通过 `--speed-uri` 传给预演和联动命令。已知导航 URI、独立鉴权和其他参数见 [CarPlay 配置](docs/carplay.md)。

### 默认车速自适应

| 当前车速 | 默认 `follow` 触发距离阈值 |
|---|---|
| 0–15 km/h | 50 米（下限） |
| 30 km/h | 100 米 |
| 60 km/h | 200 米 |
| 90 km/h | 500 米 |
| 120 km/h | 1 公里 |
| ≥150 km/h | 2 公里（上限） |

60 km/h 及以下按 `车速(km/h) / 3.6 × 12 秒` 计算，最低 50 米；60–150 km/h 在表中相邻车速点之间线性插值，≥150 km/h 使用 2000 米。
`--follow-lead-time` 按 `参数值 / 12` 缩放这条距离曲线，再应用 `--follow-min-distance` 和 `--follow-max-distance` 上下限；它不代表所有车速下都提前相同秒数。

这是可调整的项目策略，不是原厂规则，也不识别道路类型；相同车速在不同道路类型下使用相同阈值。
默认只读发现入口为 `http://[fd53:7cb8:383:3::99]:8080/`。程序跟随 `Displayspeed` 下返回的 URI，读取 `type=velocityPrimary` 的 `currentValue`，不猜测资源名称或 UUID。
`--speed-unit auto` 要求响应明确提供 `unit=km/h|mph|m/s`，也接受 VIWI 枚举 `kmPerHour`（km/h）和 `milesPerHour`（mph）；单位缺失或未知时回退到固定距离。
配置单位与响应单位会先归一化别名再比较，仍会拒绝 km/h 与 mph 等单位冲突。

2026-11-17 的实车只读检查发现，默认入口连接超时。本机注册中心查询 `GET http://[::1]/?name=DisplaySpeed` 返回 `::73:29721/DisplaySpeed/`，其返回的车速集合为 `/DisplaySpeed/speeds/`，单位为 `kmPerHour`。
注册名称区分大小写，`name=Displayspeed` 无匹配结果。`boot-start.sh` 已使用这辆车确认的集合；其他车辆需重新查询注册中心并跟随实际返回的服务 URI。

确认车速 URI 和单位后，可跳过发现，先做只读预演：

```sh
SPEED_URI='http://[fd53:7cb8:383:3::73]:29721/DisplaySpeed/speeds/'
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --speed-uri "$SPEED_URI" --iterations 10
```

这辆车可使用默认 `auto`，显式指定 `--speed-unit km/h` 也能匹配 `kmPerHour`。程序同样支持 `mph` 和 `m/s`，但必须与确认的单位一致。
独立车速凭据通过 `--speed-auth-file` 提供，不继承灯效或导航凭据。`adaptive_follow` 和 `carplay_snapshot` 日志中的 `speed_available`、`speed_kph`、`follow_distance_m` 分别表示车速是否可用、车速和当前距离阈值。
本次转弯已有成功请求时，`speed_sampling_paused` 标记保留的车速和阈值。

| 参数 | 默认值 | 含义 |
|---|---|---|
| `--speed-base` | 上述 `::99:8080` 入口 | 未指定 `--speed-uri` 时使用的服务树 |
| `--speed-unit` | `auto` | 读取响应单位，或指定已确认的单位 |
| `--follow-lead-time` | 12 秒 | 按参数值 / 12 缩放默认距离曲线，范围 1–30 秒 |
| `--follow-min-distance` / `--follow-max-distance` | 50 / 2000 米 | 自适应距离上下限，范围 1–2000 米 |
| `--speed-interval` | 2 秒 | 一次车速读取完成后的最短等待时间，至少 1 秒 |
| `--speed-timeout` | 1 秒 | 包含 GET 重试的车速读取或发现总预算 |
| `--speed-max-age` | 5 秒 | 按本地接收时间计算的有效期，至少为 interval + timeout |

读取失败会立即使旧车速失效，并回退到固定距离。新鲜度按本地接收时间计算，**不能证明上游传感器仍在更新**。
首次请求成功前，车速决定 `follow` 是否满足触发距离；完成检查仍在各阶段的请求窗口内按既定间隔进行。

关闭自适应，使用固定 150 米阈值（不再请求车速）：

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --adaptive-follow=false --follow-distance 150 --iterations 10
```

仅在 `execute` 阶段提示一次：

```sh
sh carplay_light.sh --base "$BASE" --nav-registry "$REGISTRY" \
  --apply --follow-distance 0 --one-shot --state-dir /tmp/smartlight-carplay
```

## 停止与恢复

前台运行时按 **Ctrl-C**。程序只尝试撤销自己创建的灯效项，不清空其他应用的队列。若日志提示 `cleanup_pending` 或程序退出码为 `78`，保留状态文件，先用日志中的实际路径回退，再重新启动：

```sh
sh rollback.sh --state '/tmp/smartlight-carplay/实际状态文件.json'
```

不要同时运行多个实例，也不要更换状态目录来绕过遗留锁。

需要开机联动时，核对 `boot-start.sh` 中的地址和凭据后运行 `sh boot-start.sh`。它会启动带 `--apply` 的 systemd 服务，但不会自动配置持久的 boot hook。日志在 `/tmp/carplay-light.log`；停用命令为 `systemctl stop smartlight-carplay.service`。

## 更多资料

- [CarPlay 配置与恢复](docs/carplay.md) · [常见问题](docs/troubleshooting.md) · [手动触发与调试](docs/debugging.md)
- [开发测试](docs/testing.md) · [MIT 许可](LICENSE) · [第三方声明](THIRD_PARTY_NOTICES.md)
