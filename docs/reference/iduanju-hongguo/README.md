# 红果取流参考实现快照（iDuanJu）

这里是从 [`my-name-is-alan/iDuanJu`](https://github.com/my-name-is-alan/iDuanJu) 的
`STV/Providers/Hongguo/` 与 `STVTests/` 拉取的 Swift 源码快照（2026-11-25），
只作协议移植时的对照参考，**不参与本项目的构建**（Go 工具链会跳过非 `.go` 文件）。

| 文件 | 用途 | 对应的 Go 实现 |
| --- | --- | --- |
| `HGCurrentPlayer.swift` | 播放签名主体（`signPlaybackV5` / `hashF13V5` / `medusaV5`，以及旧的 `sign`/field-25 方言） | `native/core/provider_hongguo_playback_v5.go`（V5）与 `provider_hongguo_current.go`（旧方言） |
| `HGPlayerSign.swift` | 签名原语（`expandWords` / `mixRounds` / `sumMD5` / `xmxor` / `keyHash` / `getIV`） | 同上，函数名前缀 `hongguoPlaybackV5*` |
| `HGCurrentProfile.swift` | 3428 字节常量表（material / header / boxes / factorIV / factorTable / padding） | `native/core/provider_hongguo_current_profile.go` |
| `HGProtocolConfig.swift` | 可取流协议参数（field21 / appQuery / hosts / path / 基准设备）与内置默认值 | `native/core/provider_hongguo_protocol.go` |
| `HongguoClient.swift` | 取流编排：`resolveFresh` → `resolvePreloadV5` → `resolveVideoTabV5` → 网页兜底 | `native/core/provider_hongguo.go` 的 `resolveHongguoMedia` 链 |
| `HGDevice.swift` / `HGPlayerDevice.swift` | 设备注册与自铸设备 | `provider_hongguo_current.go`（身份注册）、`provider_hongguo_device.go` |
| `HGStreamExtras.swift` | `key_seed` / `fallback_api` 编码协商 | `provider_hongguo_media_extras.go` |
| `HGDanmaku.swift` | 弹幕签名（X-Gorgon / Ladon / Argus 旧方言，未变） | `provider_hongguo_comment_sign.go` |
| `HGCurrentPlayerTests.swift` | **`testPlaybackV5GoldenVector`**：固定输入 → 固定 query / x-helios / x-medusa | `provider_hongguo_playback_v5_test.go` 逐字节对齐 |
| `HGProtocolConfigTests.swift` | 协议配置解析校验 | `provider_hongguo_playback_v5_test.go` 的 `TestHongguoPreloadBodyShape` |

## 许可

上游项目以 GPL-3.0 发布（与本项目一致）；`HGCurrentPlayer.transform` 另有 Apache-2.0 出处说明，
见上游 `STV/Resources/Licenses/HGCurrentPlayer-NOTICE.md`。

移植约定：常量表与变换**一个字节都不要改**——它们是线上协议数据，改错就会变成 HTTP 200 + 空包。
每改一次签名相关代码，先跑 `go test ./core -run TestHongguoPlaybackV5GoldenVector`，
过了再跑联网回归 `GZ_LIVE=1 go test ./core -run TestZZLiveHongguoPlaybackV5Only -v`。
