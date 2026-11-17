# SmartLight / ID.Light 灯效列表

本表依据 **2026-11-15 实车 SmartLight 服务返回的全部 58 个用途（purposes）** 整理，非本次实时查询。不同固件、车型和电影资源包可能提供不同列表，请以目标车辆的 `list` 结果为准。

“编号”是接口中的真实 `name`，可直接用于 `--purpose`；在本次固件上与 `purposeId` 相同，但不是资源 UUID。中文说明是对接口字段的翻译，未明确给出的颜色、动画形式及电量区间不作推断。

## 完整列表

| 编号（name） | 接口类别 | 功能 / 参数 | 中文说明 |
| ---: | --- | --- | --- |
| 0 | `standby` | — | 待机 |
| 2 | `engine_on` | — | 启动提示 |
| 3 | `prepare_fault` | — | 准备阶段故障提示 |
| 4 | `engine_off` | — | 关闭提示 |
| 8 | `navigation` | `lane_change / left` | 向左变道 |
| 9 | `navigation` | `lane_change / right` | 向右变道 |
| 14 | `navigation` | `direction / right` | **右转** |
| 15 | `navigation` | `direction / left` | **左转** |
| 16 | `brake_warning` | `level / advance` | 制动预警 |
| 19 | `assistant` | `driver / left / activation` | 语音助手：主驾 / 左侧激活 |
| 20 | `assistant` | `driver / left / listen` | 语音助手：主驾 / 左侧聆听 |
| 21 | `assistant` | `driver / left / speak` | 语音助手：主驾 / 左侧说话 |
| 22 | `assistant` | `driver / left / think` | 语音助手：主驾 / 左侧思考 |
| 23 | `assistant` | `driver / left / pause` | 语音助手：主驾 / 左侧暂停 |
| 24 | `assistant` | `driver / left / deactivate` | 语音助手：主驾 / 左侧停用 |
| 25 | `assistant` | `codriver / right / activation` | 语音助手：副驾 / 右侧激活 |
| 26 | `assistant` | `codriver / right / listen` | 语音助手：副驾 / 右侧聆听 |
| 27 | `assistant` | `codriver / right / speak` | 语音助手：副驾 / 右侧说话 |
| 28 | `assistant` | `codriver / right / think` | 语音助手：副驾 / 右侧思考 |
| 29 | `assistant` | `codriver / right / pause` | 语音助手：副驾 / 右侧暂停 |
| 30 | `assistant` | `codriver / right / deactivate` | 语音助手：副驾 / 右侧停用 |
| 31 | `incoming_call` | — | 来电提示 |
| 32 | `turtlemode` | `turtlemode / yellow` | 乌龟模式：黄色 |
| 33 | `SOC` | `SOC / 1` | 电量显示参数 1 |
| 34 | `SOC` | `SOC / 2` | 电量显示参数 2 |
| 35 | `SOC` | `SOC / 3` | 电量显示参数 3 |
| 36 | `SOC` | `SOC / 4` | 电量显示参数 4 |
| 37 | `SOC` | `SOC / 5` | 电量显示参数 5 |
| 38 | `SOC` | `SOC / 6` | 电量显示参数 6 |
| 39 | `SOC+Status` | `SOC+Charging / 1` | 电量与充电状态参数 1 |
| 40 | `SOC+Status` | `SOC+Charging / 2` | 电量与充电状态参数 2 |
| 41 | `SOC+Status` | `SOC+Charging / 3` | 电量与充电状态参数 3 |
| 42 | `SOC+Status` | `SOC+Charging / 4` | 电量与充电状态参数 4 |
| 43 | `SOC+Status` | `SOC+Charging / 5` | 电量与充电状态参数 5 |
| 44 | `SOC+Status` | `SOC+Charging / 6` | 电量与充电状态参数 6 |
| 45 | `configuration` | `brightness / smartlight` | 亮度设置相关用途 |
| 47 | `SOC` | `SOC / 0` | 电量显示参数 0 |
| 48 | `assistant` | `all / activation` | 语音助手：全区域激活 |
| 49 | `assistant` | `all / listen` | 语音助手：全区域聆听 |
| 50 | `assistant` | `all / speak` | 语音助手：全区域说话 |
| 51 | `assistant` | `all / think` | 语音助手：全区域思考 |
| 52 | `assistant` | `all / pause` | 语音助手：全区域暂停 |
| 53 | `assistant` | `all / deactivate` | 语音助手：全区域停用 |
| 54 | `turtlemode` | `turtlemode / red` | 乌龟模式：红色 |
| 55 | `ACC` | — | ACC 相关用途，接口未细分状态 |
| 56 | `Hands_on_Detection` | `HoD / info` | 方向盘握持检测：信息提示 |
| 57 | `Hands_on_Detection` | `HoD / urgent` | 方向盘握持检测：紧急提示 |
| 58 | `Hands_on_Detection` | `HoD / critical` | 方向盘握持检测：严重提示 |
| 59 | `PEA` | — | PEA 相关用途，接口未细分状态 |
| 60 | `LGW` | `direction / left / red` | 左侧红色 |
| 61 | `LGW` | `direction / left / yellow` | 左侧黄色 |
| 62 | `LGW` | `direction / right / red` | 右侧红色 |
| 63 | `LGW` | `direction / right / yellow` | 右侧黄色 |
| 64 | `LGW` | `direction / middle / red` | 中间红色 |
| 65 | `LGW` | `direction / middle / yellow` | 中间黄色 |
| 66 | `DFB` | `dfb / Prewarn CIL` | 保留接口原始参数，具体含义未确认 |
| 67 | `DFB` | `dfb / PreWarn SPSM` | 保留接口原始参数，具体含义未确认 |
| 68 | `DFB` | `dfb / Block` | 保留接口原始参数，具体含义未确认 |

说明：

- 编号不连续，表示本次服务未列出中间那些编号，不应据此猜测额外用途。
- SOC 的数字是接口参数，尚未确认对应的电量百分比或区间。
- `LGW`、`DFB`、`PEA` 保留原缩写，不据缩写推断功能。
- `—` 表示接口的 `functionName` 和 `functionValue` 未提供具体内容。
- 出现在列表中表示服务提供该用途，不等于每项都经过实体灯带播放验证。

## 查询当前车辆列表

在脚本目录执行，用实际可达的服务地址替换示例主机：

```sh
BASE='http://HEADUNIT_HOST:49520/smartlight/'
sh trigger_light.sh list --base "$BASE"
```

`list` 自动读取分页。IPv6 主机必须放在方括号中，保留服务声明的 `/smartlight/` 前缀；协议以实际服务为准。

## 按编号请求灯效

例如来电提示，先预演：

```sh
sh trigger_light.sh trigger --base "$BASE" --purpose 31 --hold 2
```

加上 `--apply` 后请求播放，等待 2 秒再撤销本次队列项：

```sh
sh trigger_light.sh trigger --base "$BASE" --purpose 31 --hold 2 --apply
```

请在停车状态测试。`--hold` 是客户端等待撤销的时间，不是服务端播放时限，网络与服务调度会影响实际时长。默认自动生成独立状态文件，日志中的 `state_saved` 会给出路径。

## 左转与右转快捷参数

```sh
# 左转
sh trigger_light.sh trigger --base "$BASE" --direction left --hold 2 --apply

# 右转
sh trigger_light.sh trigger --base "$BASE" --direction right --hold 2 --apply
```

也支持 `--direction 左转`、`--direction 右转`。快捷参数根据实时元数据匹配 `navigation / direction / left或right`，不写死编号，也不会匹配到变道用途。

## 异常后的独立清理

使用本次日志中记录的真实状态路径：

```sh
sh rollback.sh --state /tmp/本次状态文件.json
```

清理依据服务返回的 `Location` 队列 URI，只撤销本次请求。缺少可信身份时保留状态并报告 `cleanup_pending`，不按灯效名称猜测删除目标。更多说明见 [排错与恢复](troubleshooting.md)。

## 已验证范围

| 项目 | 结果 |
| --- | --- |
| 完整列表 | 58 项，分页读取通过 |
| 左转 | POST 201；播放器选中本次项；现场使用者确认实体亮灯 |
| 左转自动撤销 | 等待 2 秒后 DELETE 200，并确认本次队列项消失 |
| 右转 | 实车只读预演匹配通过，未确认实体播放 |
| 其它用途 | 已列举，尚未逐项确认实体播放 |

HTTP 成功、队列中出现请求、播放器选中与实体灯带播放是不同观察结果。日志中的 `physical_light_verified=false` 表示脚本自身无法观察实体灯带，不否定现场使用者的观察。
