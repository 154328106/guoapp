package core

// 红果取流协议参数。
//
// 这些值会随上游 App 版本调整而变化（域名 / 路径 / dr_scene / device_level / medusa 第 21 字段 /
// 基准设备号），但不涉及签名算法本身；集中放在这里，上游再改时只动这一个文件。
//
// 2026-11-23 上游变更：medusa 第 21 字段 377 被判无效（真机值 312）；取流改走
// 7.4.5.32 的 preload/v1（gzip 请求体），video_tab 作兜底；新注册的匿名设备会被软拒绝，
// 因此默认先用基准设备身份，失败再退回本地注册设备。

// hongguoProtocol 是取流用的一组可调参数。
type hongguoProtocol struct {
	// Field21 是 medusa 第 21 字段（nil 表示整条省略）。377 已失效，真机为 312。
	Field21 *int
	// AppQuery 是所有 App 接口共用的 query 前缀（含 aid / version / channel / 机型）。
	AppQuery [][2]string
	// PreloadHosts / PreloadPath / PreloadDrScene / PreloadLevel 供 preload/v1 主通道使用。
	PreloadHosts   []string
	PreloadPath    string
	PreloadDrScene string
	PreloadLevel   int
	// TabHosts / TabPath / TabLevel 供 video_tab 兜底通道使用。
	TabHosts []string
	TabPath  string
	TabLevel int
	// FallbackID 是上游验证过的基准设备身份，匿名注册被拒时靠它取流。
	FallbackID hongguoPlayerIdentity
}

func hongguoIntPtr(v int) *int { return &v }

// hongguoProtocolCurrent 返回当前生效的协议参数（内置值）。
func hongguoProtocolCurrent() hongguoProtocol {
	field21 := hongguoIntPtr(312)
	return hongguoProtocol{
		Field21: field21,
		AppQuery: [][2]string{
			{"aid", "8662"},
			{"app_name", "novelread"},
			{"version_code", "74532"},
			{"version_name", "7.4.5.32"},
			{"update_version_code", "74532"},
			{"manifest_version_code", "74532"},
			{"os", "android"},
			{"ssmix", "a"},
			{"channel", "update_64"},
			{"device_platform", "android"},
			{"device_type", "25053RT47C"},
			{"device_brand", "Redmi"},
			{"os_version", "16"},
			{"os_api", "36"},
			{"ac", "wifi"},
			{"language", "zh"},
			{"host_abi", "arm64-v8a"},
		},
		PreloadHosts: []string{
			"reading.snssdk.com",
			"api5-normal-sinfonlinea.fqnovel.com",
			"api5-normal-sinfonlineb.fqnovel.com",
		},
		PreloadPath:    "/novel/player/multi_video_model/preload/v1",
		PreloadDrScene: "preload",
		PreloadLevel:   2,
		TabHosts: []string{
			"api5-normal-sinfonlineb.fqnovel.com",
			"api5-normal-hl.fqnovel.com",
		},
		TabPath:  "/reading/bookapi/video_tab/multi_video_model/v1/",
		TabLevel: 1,
		FallbackID: hongguoPlayerIdentity{
			DeviceID:  "1624456208323876",
			InstallID: "1624456208327972",
			CDID:      "91d56f7e-c027-4ce9-9a3c-229d59a13c3b",
			OpenUDID:  "56a962410c494bba",
		},
	}
}
