import Foundation

/// Hongguo playback protocol knobs that change without an app release (medusa field 21, request profile,
/// hosts, paths, baseline identity). The signing algorithm itself stays in code. The built-in value is the
/// default; a validated remote copy (`configs/hongguo-protocol.json` in the repo) overrides it when its
/// `configVersion` is not older than the built-in one. Mirrors android HGProtocolConfig.kt.
struct HGKV: Equatable { var k: String; var v: String }

struct HGProtocolConfig: Equatable {
    var configVersion: Int
    /// nil = omit the field entirely.
    var field21: Int?
    var appQuery: [HGKV]
    var preloadHosts: [String]
    var preloadPath: String
    var preloadDrScene: String
    var preloadDeviceLevel: Int
    var tabHosts: [String]
    var tabPath: String
    var tabDeviceLevel: Int
    var fallbackIdentity: HGPlayerIdentity?
    var preferFallbackIdentity: Bool = true

    static let schema = 1
    private static let maxBytes = 64 * 1024
    private static let queryKeys: Set<String> = [
        "aid", "app_name", "version_code", "version_name", "update_version_code", "manifest_version_code",
        "os", "ssmix", "channel", "device_platform", "device_type", "device_brand", "os_version", "os_api",
        "ac", "language", "host_abi",
    ]

    static let builtin = HGProtocolConfig(
        configVersion: 2, field21: 312,
        appQuery: [
            ("aid", "8662"), ("app_name", "novelread"), ("version_code", "74532"), ("version_name", "7.4.5.32"),
            ("update_version_code", "74532"), ("manifest_version_code", "74532"), ("os", "android"), ("ssmix", "a"),
            ("channel", "update_64"), ("device_platform", "android"), ("device_type", "25053RT47C"),
            ("device_brand", "Redmi"), ("os_version", "16"), ("os_api", "36"), ("ac", "wifi"), ("language", "zh"),
            ("host_abi", "arm64-v8a"),
        ].map { HGKV(k: $0.0, v: $0.1) },
        preloadHosts: ["reading.snssdk.com", "api5-normal-sinfonlinea.fqnovel.com", "api5-normal-sinfonlineb.fqnovel.com"],
        preloadPath: "/novel/player/multi_video_model/preload/v1", preloadDrScene: "preload", preloadDeviceLevel: 2,
        tabHosts: ["api5-normal-sinfonlineb.fqnovel.com", "api5-normal-hl.fqnovel.com"],
        tabPath: "/reading/bookapi/video_tab/multi_video_model/v1/", tabDeviceLevel: 1,
        fallbackIdentity: HGPlayerIdentity(deviceId: "1624456208323876", iid: "1624456208327972",
                                           cdid: "91d56f7e-c027-4ce9-9a3c-229d59a13c3b", openudid: "56a962410c494bba", registeredAt: 0)
    )

    private static func matches(_ s: String, _ pattern: String) -> Bool {
        s.range(of: "^(?:\(pattern))$", options: .regularExpression) != nil
    }

    private static func hosts(_ v: Any?) -> [String]? {
        guard let a = v as? [Any], (1...6).contains(a.count) else { return nil }
        var out: [String] = []
        for item in a {
            guard let h = (item as? String)?.lowercased(), matches(h, "[a-z0-9-]+(\\.[a-z0-9-]+)*"),
                  h == "fqnovel.com" || h.hasSuffix(".fqnovel.com") || h == "snssdk.com" || h.hasSuffix(".snssdk.com") else { return nil }
            out.append(h)
        }
        return out
    }

    private static func path(_ s: String) -> String? {
        guard matches(s, "/?[A-Za-z0-9_]+(/[A-Za-z0-9_]+){1,6}/?") else { return nil }
        return s.hasPrefix("/") ? s : "/" + s
    }

    /// Returns nil for anything malformed, oversized or pointing outside the allowed hosts.
    static func parse(_ data: Data) -> HGProtocolConfig? {
        guard data.count <= maxBytes, let j = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
              J.int(j["schema"]) == schema, J.int(j["configVersion"]) >= 1,
              let medusa = j["medusa"] as? [String: Any], medusa.keys.contains("field21") else { return nil }
        var f21: Int?
        if !(medusa["field21"] is NSNull) {
            guard let n = medusa["field21"] as? NSNumber, (0...65535).contains(n.intValue) else { return nil }
            f21 = n.intValue
        }
        guard let q = j["appQuery"] as? [String], (8...40).contains(q.count) else { return nil }
        var query: [HGKV] = []
        for s in q {
            guard let eq = s.firstIndex(of: "=") else { return nil }
            let k = String(s[..<eq]), v = String(s[s.index(after: eq)...])
            guard queryKeys.contains(k), v.count <= 64, !query.contains(where: { $0.k == k }) else { return nil }
            query.append(HGKV(k: k, v: v))
        }
        guard ["aid", "version_code", "version_name"].allSatisfy({ k in query.contains { $0.k == k } }),
              let pre = j["preload"] as? [String: Any], let tab = j["tab"] as? [String: Any],
              let dr = pre["drScene"] as? String, dr == "preload" || dr == "player",
              let pl = (pre["deviceLevel"] as? NSNumber)?.intValue, (0...5).contains(pl),
              let tl = (tab["deviceLevel"] as? NSNumber)?.intValue, (0...5).contains(tl),
              let ph = hosts(pre["hosts"]), let th = hosts(tab["hosts"]),
              let pp = path(pre["path"] as? String ?? ""), let tp = path(tab["path"] as? String ?? "") else { return nil }
        var fb: HGPlayerIdentity?
        if let o = j["fallbackIdentity"] as? [String: Any] {
            guard let did = o["did"] as? String, let iid = o["iid"] as? String, let cdid = o["cdid"] as? String, let ou = o["openudid"] as? String,
                  matches(did, "[1-9][0-9]{9,23}"), matches(iid, "[1-9][0-9]{9,23}"),
                  matches(cdid, "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"), matches(ou, "[0-9a-f]{16}") else { return nil }
            fb = HGPlayerIdentity(deviceId: did, iid: iid, cdid: cdid, openudid: ou, registeredAt: 0)
        }
        let preferFallback = (j["preferFallbackIdentity"] as? Bool) ?? true
        return HGProtocolConfig(configVersion: J.int(j["configVersion"]), field21: f21, appQuery: query,
                                preloadHosts: ph, preloadPath: pp, preloadDrScene: dr, preloadDeviceLevel: pl,
                                tabHosts: th, tabPath: tp, tabDeviceLevel: tl, fallbackIdentity: fb,
                                preferFallbackIdentity: preferFallback)
    }
}

extension HGPlayerIdentity: Equatable {
    static func == (a: HGPlayerIdentity, b: HGPlayerIdentity) -> Bool {
        a.deviceId == b.deviceId && a.iid == b.iid && a.cdid == b.cdid && a.openudid == b.openudid
    }
}

/// Holder + loader: disk cache first, then a throttled background refresh from the repo (raw + CDN mirrors).
final class HGProtocol: @unchecked Sendable {
    static let shared = HGProtocol()
    private let lock = NSLock()
    private var _current = HGProtocolConfig.builtin
    private var lastAttempt = Date.distantPast
    private var inFlight = false
    private static let urls = [
        "https://raw.githubusercontent.com/my-name-is-alan/iDuanJu/main/configs/hongguo-protocol.json",
        "https://cdn.jsdelivr.net/gh/my-name-is-alan/iDuanJu@main/configs/hongguo-protocol.json",
        "https://fastly.jsdelivr.net/gh/my-name-is-alan/iDuanJu@main/configs/hongguo-protocol.json",
    ]

    var current: HGProtocolConfig { lock.lock(); defer { lock.unlock() }; return _current }

    private static var fileURL: URL {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir.appendingPathComponent("hongguo_protocol.json")
    }

    func load() {
        var cfg = HGProtocolConfig.builtin
        if let d = try? Data(contentsOf: Self.fileURL), let parsed = HGProtocolConfig.parse(d),
           parsed.configVersion >= HGProtocolConfig.builtin.configVersion { cfg = parsed }
        lock.lock(); _current = cfg; lock.unlock()
    }

    /// Keeps the highest valid `configVersion`; never downgrades below the built-in one.
    func refresh(session: URLSession) async -> Bool {
        for u in Self.urls {
            guard let url = URL(string: u) else { continue }
            var req = URLRequest(url: url, timeoutInterval: 6)
            req.cachePolicy = .reloadIgnoringLocalCacheData
            guard let (data, resp) = try? await session.data(for: req), (resp as? HTTPURLResponse)?.statusCode == 200,
                  let cfg = HGProtocolConfig.parse(data) else { continue }
            if cfg.configVersion < current.configVersion { return false }
            store(cfg)
            try? data.write(to: Self.fileURL, options: .atomic)
            return true
        }
        return false
    }

    private func store(_ cfg: HGProtocolConfig) { lock.lock(); _current = cfg; lock.unlock() }
    private func finish() { lock.lock(); inFlight = false; lock.unlock() }

    func refreshAsync(session: URLSession, force: Bool = false) {
        guard begin(force: force) else { return }
        Task.detached { [self] in
            _ = await refresh(session: session)
            finish()
        }
    }

    private func begin(force: Bool) -> Bool {
        let now = Date()
        lock.lock(); defer { lock.unlock() }
        let wait: TimeInterval = force ? 60 : 30 * 60
        if inFlight || now.timeIntervalSince(lastAttempt) < wait { return false }
        inFlight = true; lastAttempt = now
        return true
    }
}
