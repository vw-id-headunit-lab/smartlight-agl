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

- 仅跟随当前 CarPlay 转向：`execute` 阶段直接提示，`follow` 阶段需进入距离阈值。阈值默认按当前车速乘以 12 秒计算，限制在 50–500 米；车速不可用时回退到 150 米。
- 默认保留自己创建且仍在队列中的灯效请求，`follow` 切 `execute` 也连续沿用；不再每 3 秒强制撤销重发。按至少 3 秒的间隔检查该队列项，确认服务已移除且转向仍有效时才补发，总窗口不超过 30 秒。`--one-shot` 仍是每窗口一次、保持 3 秒后撤销；`--follow-distance 0` 只在 `execute` 阶段提示。
- 若默认车速入口不可用，从车机注册中心查找 `DisplaySpeed`（区分大小写），将确认的集合 URI 通过 `--speed-uri` 传给预演和联动命令。已知导航 URI、独立鉴权和其他参数见 [CarPlay 配置](docs/carplay.md)。

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
