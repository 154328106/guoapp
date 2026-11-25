# 红果取流修复 — 完整调研与移植方案

## 2026-11-25 结论：已修复（现行协议是 V5 播放签名，不是 field-25）

- 上游 11-23 再次收紧：field-25 方言（`provider_hongguo_current.go`）的请求整体被判无效，
  POST `video_model/v1` / `multi_video_model/v1` / `player/v1` 全部 HTTP 200 + 0 字节；
  同时 medusa 第 21 字段 `377` 被判无效（真机值 `312`），新注册的匿名设备也不再返回播放信息。
- 现行形态（iDuanJu 1.6b33 起）：
  - `POST https://<host>/novel/player/multi_video_model/preload/v1`，请求体 gzip，签名头只有
    `x-khronos` / `x-helios` / `x-medusa`（+ 有 body 时的 `Content-Type` 与 `x-ss-stub`）；
  - 失败回退 `GET /reading/bookapi/video_tab/multi_video_model/v1/`（无 body，`device_level=1`）；
  - query 用 7.4.5.32 profile（`version_code=74532`、`channel=update_64`、`device_type=25053RT47C`、
    `os_version=16`、`os_api=36`）＋ 基准设备身份。
- 已落地：
  - `provider_hongguo_playback_v5.go`：`hongguoPlaybackV5Sign` / `hashF13V5` / `medusaV5` +
    `expandWords` / `mixRounds` / `sumMD5` / `xmxor` / `keyHash`，以及 `preload/v1` 与 `video_tab/v1`
    两条通道和「基准设备 → 本地注册设备」的身份顺序。
  - `provider_hongguo_protocol.go`：field21=312、appQuery、hosts、path、device_level、基准设备。
  - `resolveHongguoCurrentMedia` 改为：V5（preload → video_tab）→ 旧 field-25 方言兜底。
- 验证：
  - 离线 `go test ./core -run TestHongguoPlaybackV5GoldenVector`（与上游 Swift 固定向量逐字节一致）；
  - 联网 `GZ_LIVE=1 go test ./core -run 'TestZZLiveHongguoPlaybackV5Only|TestZZLiveSourceSmoke' -v`
    → 第 1/2/3 集 0.1–0.3s 出片源，首页 → 详情 → 取流全链通。
- 参考实现快照见 `docs/reference/iduanju-hongguo/`。

以下为 2026-11-23 的调研记录，保留作为历史与协议回滚参考。

---

结论日期 2026-11-23。guoapp `native/core` 的 hongguo 取流因上游 2026-11-21 换签名协议而全部空 body。

## 根因

- 旧 V5（`provider_hongguo_v5_sign.go`，N3urda/hongguoTV branch-12 方言）已被服务端整体废弃，POST `player/v1` 与 GET `video_tab` 都返回 200 + 0 字节（签名软拒绝）。
- 密钥未变：`signKey = b2 3c 2a 47 0a 20 d1 05 dc 3f 8c 44 38 f8 27 2e d4 6d 6c 89 ff dc de dd 51 08 69 8d 26 e9 48 9d`、`magic = 2d 4b 4f ca 49 75 0d 43 3f b5 ae 2c 22 6d cc 56`，与 guoapp 现有常量一致。
- 变的是请求形态与签名器，不是密钥。

## 权威参考实现（已确认，公开可读）

- Swift：`my-name-is-alan/iDuanJu`（fork `154328106/iDuanJu`），`STV/Providers/Hongguo/`。
- Go 网关：`my-name-is-alan/get_video_streaming` commit `0613b49`（`internal/provider/hongguo/playersign/current*.go`）——**已 404/私有**，不可直搬。

关键文件：
- `HongguoClient.swift`：取流入口 `postPlayerBatch` + query + 设备。
- `HGSign.swift`：`genericSessionHeaders`（AB-off，默认）/ `videoModelHeaders`（AB-on）/ `xGorgon`。
- `HGPlayerDevice.swift`：自铸设备（`mint`：15 位 device_id，`iid = device_id + 4096`，**无 device_register**）。
- `HGCurrentPlayer.swift` / `HGCurrentProfile.swift` / `HGPlayerSign.swift` / `Medusa.swift`：下一代替换（field 25 方言，主分支取流尚未切换，暂不需要）。

## 当前主分支取流形态（移植目标）

### 设备（替换 device_register）
- `device_id` = 15 位十进制，首位 5–9，随机。
- `iid` = `device_id + 4096`。
- 不调 `log.snssdk.com/service/2/device_register/`；`device_register` 设备会被 batch 接口拒。
- 空 body 软拒绝时 `remint()` 换新设备。

### 端点与 query（`playerQuery`）
- 默认 AB-off：`https://api5-normal-sinfonlinea.fqnovel.com/novel/player/multi_video_model/v1?`
- 回退 AB-on：`https://api5-normal-sinfonlinea.fqnovel.com/novel/player/multi_video_model/player/v1?`
- query（`iid`/`device_id` 用自铸设备）：
  ```
  iid=<iid>&device_id=<device_id>&ac=wifi&channel=ry_hongguo_offlinestore_04&aid=8662&app_name=novelread&version_code=72133&version_name=7.2.133&device_platform=android&os=android&ssmix=a&device_type=EML-AL00&device_brand=HUAWEI&language=zh&os_api=29&os_version=10&manifest_version_code=72133&resolution=1080*2340&dpi=480&update_version_code=72133&_rticket=<nowMs>
  ```

### 请求体（不 gzip）
JSON（与 guoapp 现有 body 一致，`mixed_video_id_map` 结构）：
```json
{"biz_param":{...},"mixed_video_id_map":{"1004":["<videoID>"]},"dr_scene":"player"}
```
（AB-off 时 `dr_scene` 在 body 里；AB-on 时在 path。guoapp 现有 `resolveHongguoAppMultiMedia` 的 body 已类似。）

### 请求头
`User-Agent`（HGDevice.ua）、`Accept: application/json`、`Content-Type: application/json; charset=utf-8`、`X-XS-From-Web: 0`、`Sdk-Version: 2`，再加签名头。

### 签名（AB-off 默认，`genericSessionHeaders`）
```swift
ts = nowSeconds
stub = HEX(md5(body)).uppercase()
headers = {
  "X-Gorgon": xGorgon(params: rawQuery, ts, body, flags: 0),
  "X-Khronos": String(ts),
  "X-Argus": base64(le32(ts)),
  "X-SS-STUB": stub,
  "X-SS-Req-Ticket": String(nowMs),
}
```
其中 `xGorgon(params, ts, variant=random16, body, flags=0)`：
```
chunkX = body非空 ? md5(body)[0..4] : [0,0,0,0]
concat = md5(params)[0..4] + chunkX + [0,0,0,0] + [0x00,0x03,0x0a,0x04] + be32(ts)
fh = flags>>8 ; fl = flags&0xff
key  = [0x4a, fh, 0x16, variant&0xff, 0x47, 0x6c, fl, variant>>8]
ks   = brokenRC4(key, 20)            // 见下
xored = concat[i] ^ ks[i]  (20 字节)
head = [0x84, 0x04, variant>>8, variant&0xff, fh, fl]
return hex(head + gorgon20(xored))
```
`brokenRC4(key, n)`（注意：不是标准 RC4）：
```
S = 0..255; j=0
for i in 0..<256: j=(j+S[i]+key[i&7])&0xff; S[i]=S[j]
i=0; jj=0; out=[]
for _ in 0..<n:
    i=(i+1)&0xff; jj=(jj+S[i])&0xff; v=S[jj]; S[i]=v
    out.append(S[(S[i]+v)&0xff])
```
`gorgon20(buf)`：
```
for n in 0..<20:
    buf[n] = nibbleSwap(buf[n])          // (b&0x0f)<<4 | (b>>4)
    x = buf[n] ^ buf[(n+1)%20]
    buf[n] = 0x14 ^ (~bitReverse(x))     // bitReverse 8 位逆序
```

### AB-on 签名（`videoModelHeaders`，仅当 AB-off 空 body 时回退）
- gorgon 用固定 20 字节 key：
  `[0x44,0xb9,0xb9,0xd9,0xa4,0xae,0xf9,0xfc,0xa4,0x93,0xaa,0x75,0x7c,0xa3,0xc2,0xc4,0xa4,0x96,0x93,0x8f]`
- `p[0..4]=md5(query)[0..4]; p[4..8]=md5(body)[0..4]; p[12..16]=[0,6,11,28]; p[16..20]=be32(ts)`；p ^= key；再 nibbleSwap/位反转；头 `[0x84,0x04,0x40,0x1c,0,0]`。返回 X-Gorgon/X-Khronos/X-SS-STUB/X-SS-Req-Ticket。

### 响应解析
`code==0` 后取 `data[<videoID>].video_model`（与 guoapp `resolveHongguoAppMultiMedia` 的解析一致），再走 `hongguoMediaFromModel`。

## 移植方案（建议，改动最小）

1. 新建 `provider_hongguo_current.go`：
   - 自铸设备：`hongguoMintDevice()`（15 位 + iid=+4096），空 body 时换新。
   - `hongguoPlayerQuery(deviceID, iid, ticket)`：按上面 query 拼。
   - `hongguoXGorgon(params, ts, body, flags)` + `brokenRC4` + `gorgon20` + `bitReverse`/`nibbleSwap`。
   - `hongguoGenericSessionHeaders(rawQuery, body, now)` / `hongguoVideoModelHeaders(...)`。
   - `resolveHongguoCurrentBatchMedia(ctx, videoID)`：AB-off 先试，空 body 回退 AB-on；POST JSON（不 gzip）到 `multi_video_model/v1`。
2. `resolveHongguoMedia`（`provider_hongguo.go`）优先调 `resolveHongguoCurrentBatchMedia`，旧 V5/App/web/备用继续兜底。
3. 冒烟验证：`go test ./core -run TestZZLiveSourceSmoke -v`（`GZ_LIVE=1 GZ_LIVE_SOURCE=hongguo`），看第 1 集取流是否出 `media.URL`。

## 常量（已验证与 guoapp 一致，无需改）
- `signKey`/`magic`/`sbox` 见 `provider_hongguo_v5_sign.go`。
- 下一代方言新表在 `HGCurrentProfile.bytes`（base64，3428 字节），主分支取流暂不需要。

## 未决点（已实测收敛）
- **已实测**：`genericSessionHeaders`（AB-off）与 `videoModelHeaders`（AB-on）两条「简单 X-Gorgon」旧方言，POST `multi_video_model/v1` / `player/v1` 现在都返回 **HTTP 200 + 空 body**（软拒绝）。即旧 batch 方言也已死。
- **结论**：必须移植 `HGCurrentPlayer.sign`（field 25 新方言，`resolvePure`/`resolveCurrentBatch` 主路径），旧简单 X-Gorgon 只作兜底且当前同样被拒。

## resolveFresh 主流程（iDuanJu 实际取流顺序）
1. `resolvePure`：POST `/novel/player/video_model/v1/`，用 `HGCurrentPlayer.sign`，**注册设备身份**（`HGPlayerIdentity.register`，带 `cdid`/`openudid`，`channel=xiaomi_8662_64`，`version_name=7.3.9.32`，`version_code=73932`，`device_type=23127PN0CC`，`os_version=14`）。body：`{"video_id":vid,"biz_param":{"video_id_type":0,"need_all_video_definition":true,"video_platform":3,"device_level":3或4}}`（`J.encodeSorted` 排序键）。
2. 空 body → `resolveCurrentBatch`：POST `/novel/player/multi_video_model/v1` 或 `/player/v1`，同样 `HGCurrentPlayer.sign`。body：`biz_param{detail_page_version:0,device_level:level,disable_digg_stat:false,disable_video_relate_book:false,need_all_video_definition:true,need_mp4_align:false,use_os_player:false,use_server_dns:true,use_server_dns_scene:"cold_start_stage",video_platform:1024}` + `dr_scene:"player"` + `mixed_video_id_map{"1004":[vid]}`。
3. 仍空 → `resolveBatch`（旧简单 X-Gorgon，`HGPlayerDevice.mint` 自铸设备，`playerQuery`）→ `resolveSingle`。
4. 响应解析：`data[vid].video_model`（对象或 base64 JSON），再 `materialize` 成 streams。

## 移植核心（`HGCurrentPlayer.sign` + `HGPlayerSign` 帮助函数 + `HGCurrentProfile` 常量）
- 入口 `sign(pairs, nowMillis, deviceID, versionName, body)`：selector `%3==0`，`factor()`（SHA-256 式 + factorIV/factorTable/padding 新表），`protobuf()`（nested15 三字段 + field24 `do:0,tk:false` + **field25 非空** + 精简 env），`compute()`（crc64 checksum + gorgon/helios/medusa），`medusa`（`ks`+`transform` 自定义 AES + `lane/bits` 位置换 + `mid` 取 field13/14 首字节）。
- 常量表 `HGCurrentProfile.bytes`（3428 字节 base64）：material(32)=signKey、header(16)=magic、boxes(4×256)=sbox、initial(16)、checksum(256×8)、factorIV(8×4)、factorTable(64×4)、padding(4)。signKey/magic/sbox 与 guoapp 现有一致；新增 initial/checksum/factorIV/factorTable/padding。
- 帮助函数：`sm3`（guoapp 已有 `hongguoSM3`）、`md5`（已有 `md5Bytes`）、`le32/be32/rdLE64/wrLE64`、`getIV`、`encodeQuery`、`pSInt/pStr/pBytes`（guoapp 已有 `hongguoProtoSint/ProtoString/ProtoBytes`）、`xmxor`、`keyHash`、自定义 AES `transform`、`rc4Gorgon`、`xGorgon`、`helios`。

## 建议落点（guoapp）
- 新建 `provider_hongguo_current.go`：`HGCurrentProfile` 常量解码 + `HGCurrentPlayer.sign` 全算法 + 注册设备身份（复用/仿照现有 `ensureHongguoDevice`，但 pairs 换成 `xiaomi_8662_64`/`7.3.9.32`/`23127PN0CC`/带 cdid+openudid）+ `resolveHongguoCurrentMedia`（resolvePure → resolveCurrentBatch）。
- 保留现有 V5/App/web/备用作兜底。

## 注意
- 本次调研前对 `provider_hongguo_v5_media.go` / `provider_hongguo_v5_sign.go` / `provider_hongguo.go` 加的 video_tab 路由用的是旧 V5 签名，属方向正确但签名已废，最终应由上述「简单 X-Gorgon 新形态」替代。
