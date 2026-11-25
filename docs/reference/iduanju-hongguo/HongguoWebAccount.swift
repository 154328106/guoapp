import Foundation
import WebKit

/// Hongguo web account session (QR login via https://hongguoduanju.com) + cloud watch-history sync.
/// Uses a persistent same-origin `WKWebView` (`WKWebsiteDataStore.default()`) for `/api/pc/*` requests so
/// `HttpOnly` / `SameSite` session cookies set during QR login are always sent natively by WebKit without
/// cookie-jar flush races or `URLSession` mismatches.
@MainActor
final class HongguoWebAccount: ObservableObject {
    struct Profile: Codable, Equatable {
        let nickname: String
        let avatarUrl: String
    }

    static let shared = HongguoWebAccount()
    static let webOrigin = "https://hongguoduanju.com"
    static let pcUserAgent =
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

    private static let profileKey = "stv.hg.web.profile"
    private static let cookieKey = "stv.hg.web.cookie"
    private static let sessionTagTTL: TimeInterval = 15 * 60
    private static let reportThrottle: TimeInterval = 30
    /// Grace window after WebView confirms login during which transient empty checks must never wipe `profile`.
    private static let loginGraceWindow: TimeInterval = 90

    @Published private(set) var profile: Profile?
    @Published private(set) var syncing = false
    @Published private(set) var lastSyncNote: String?

    var isLoggedIn: Bool { profile != nil }

    /// Live QR-login WebView while `HongguoLoginView` is presented, preferred over the background API WebView.
    weak var activeLoginWebView: WKWebView?

    private var cachedCookieHeader: String?
    private var lastConfirmedLoginAt: Date?
    private var sessionTags: [String: (tag: String, at: Date)] = [:]
    private var lastReportedAt: [String: Date] = [:]

    private var apiWebView: WKWebView?
    private var apiWebViewReady = false
    private var apiReadyWaiters: [CheckedContinuation<Bool, Never>] = []
    private var pendingCalls: [String: CheckedContinuation<(status: Int, text: String)?, Never>] = [:]
    private lazy var apiBridge = APIBridge(owner: self)

    private let session: URLSession = {
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = 15
        c.httpShouldSetCookies = false
        c.httpCookieAcceptPolicy = .never
        return URLSession(configuration: c)
    }()

    private init() {
        cachedCookieHeader = UserDefaults.standard.string(forKey: Self.cookieKey)
        if let data = UserDefaults.standard.data(forKey: Self.profileKey),
           let saved = try? JSONDecoder().decode(Profile.self, from: data),
           !saved.nickname.isEmpty {
            profile = saved
        }
    }

    // MARK: - Same-origin WebKit RPC

    private final class APIBridge: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
        weak var owner: HongguoWebAccount?
        init(owner: HongguoWebAccount) { self.owner = owner }

        func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
            guard message.name == "idjApi",
                  let dict = message.body as? [String: Any],
                  let reqId = dict["reqId"] as? String else { return }
            let status = dict["status"] as? Int ?? 0
            let text = dict["text"] as? String ?? ""
            Task { @MainActor [weak owner] in
                owner?.completeCall(reqId: reqId, result: status > 0 ? (status, text) : nil)
            }
        }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            Task { @MainActor [weak owner] in
                owner?.markApiWebViewReady(true)
            }
        }

        func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
            Task { @MainActor [weak owner] in
                owner?.markApiWebViewReady(false)
            }
        }

        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            Task { @MainActor [weak owner] in
                owner?.markApiWebViewReady(false)
            }
        }
    }

    private func markApiWebViewReady(_ ok: Bool) {
        apiWebViewReady = ok
        let waiters = apiReadyWaiters
        apiReadyWaiters.removeAll()
        for w in waiters { w.resume(returning: ok) }
    }

    private func completeCall(reqId: String, result: (status: Int, text: String)?) {
        guard let cont = pendingCalls.removeValue(forKey: reqId) else { return }
        cont.resume(returning: result)
    }

    /// Ensures a lightweight same-origin `WKWebView` on `https://hongguoduanju.com` is ready to run `fetch(...)`.
    private func ensureApiWebView() async -> WKWebView? {
        if let live = activeLoginWebView, live.url?.host?.lowercased().contains("hongguoduanju.com") == true {
            return live
        }
        if let wv = apiWebView, apiWebViewReady, wv.url?.host?.lowercased().contains("hongguoduanju.com") == true {
            return wv
        }
        if apiWebView == nil {
            let ucc = WKUserContentController()
            ucc.add(apiBridge, name: "idjApi")
            let cfg = WKWebViewConfiguration()
            cfg.userContentController = ucc
            cfg.websiteDataStore = .default()
            cfg.defaultWebpagePreferences.preferredContentMode = .desktop
            let wv = WKWebView(frame: CGRect(x: 0, y: 0, width: 320, height: 240), configuration: cfg)
            wv.customUserAgent = Self.pcUserAgent
            wv.navigationDelegate = apiBridge
            apiWebView = wv
            apiWebViewReady = false
            // Load a tiny same-origin endpoint so cookies + origin are bound without heavy video DOM.
            if let u = URL(string: "\(Self.webOrigin)/robots.txt") {
                wv.load(URLRequest(url: u))
            }
        } else if let wv = apiWebView, !wv.isLoading, !apiWebViewReady {
            if let u = URL(string: "\(Self.webOrigin)/robots.txt") {
                wv.load(URLRequest(url: u))
            }
        }

        let ready = await withTaskGroup(of: Bool.self) { group in
            group.addTask { @MainActor in
                if self.apiWebViewReady { return true }
                return await withCheckedContinuation { cont in
                    self.apiReadyWaiters.append(cont)
                }
            }
            group.addTask {
                try? await Task.sleep(nanoseconds: 6_000_000_000)
                return false
            }
            let first = await group.next() ?? false
            group.cancelAll()
            return first
        }
        return ready ? apiWebView : nil
    }

    /// Executes a same-origin `fetch` inside WebKit so `HttpOnly` cookies are sent natively.
    private func webViewFetch(
        path: String,
        method: String = "GET",
        headers: [String: String] = [:],
        body: String? = nil
    ) async -> (status: Int, text: String)? {
        guard let wv = await ensureApiWebView() else { return nil }
        let isDedicatedApiWV = (wv === apiWebView)
        if !isDedicatedApiWV {
            // For `activeLoginWebView`, ensure `idjApi` handler is attached once.
            wv.configuration.userContentController.removeScriptMessageHandler(forName: "idjApi")
            wv.configuration.userContentController.add(apiBridge, name: "idjApi")
        }

        let reqId = UUID().uuidString
        let hdrData = (try? JSONSerialization.data(withJSONObject: headers)) ?? Data("{}".utf8)
        let hdrJSON = String(data: hdrData, encoding: .utf8) ?? "{}"
        let pathLit = Self.jsStringLiteral(path)
        let methodLit = Self.jsStringLiteral(method)
        let reqIdLit = Self.jsStringLiteral(reqId)
        let bodyExpr = body.map { Self.jsStringLiteral($0) } ?? "undefined"

        let js = """
        (function() {
          var reqId = \(reqIdLit);
          var opts = { method: \(methodLit), credentials: 'include', headers: \(hdrJSON) };
          var bodyVal = \(bodyExpr);
          if (bodyVal !== undefined) opts.body = bodyVal;
          fetch(\(pathLit), opts)
            .then(function(r) { return r.text().then(function(t) { return { status: r.status, text: t }; }); })
            .then(function(res) {
              window.webkit.messageHandlers.idjApi.postMessage({ reqId: reqId, status: res.status, text: res.text });
            })
            .catch(function() {
              window.webkit.messageHandlers.idjApi.postMessage({ reqId: reqId, status: 0, text: '' });
            });
        })();
        """

        return await withTaskGroup(of: (status: Int, text: String)?.self) { group in
            group.addTask { @MainActor in
                await withCheckedContinuation { cont in
                    self.pendingCalls[reqId] = cont
                    wv.evaluateJavaScript(js) { _, err in
                        if err != nil {
                            Task { @MainActor in self.completeCall(reqId: reqId, result: nil) }
                        }
                    }
                }
            }
            group.addTask {
                try? await Task.sleep(nanoseconds: 8_000_000_000)
                await MainActor.run { self.completeCall(reqId: reqId, result: nil) }
                return nil
            }
            let first = await group.next() ?? nil
            group.cancelAll()
            return first
        }
    }

    private static func jsStringLiteral(_ s: String) -> String {
        guard let data = try? JSONSerialization.data(withJSONObject: [s]),
              let arr = String(data: data, encoding: .utf8),
              arr.count >= 2 else { return "\"\"" }
        return String(arr.dropFirst().dropLast())
    }

    /// Snapshot cookies for `hongguoduanju.com` from `WKWebsiteDataStore` into `UserDefaults`.
    func syncCookiesFromWebView() async -> String? {
        let all = await WKWebsiteDataStore.default().httpCookieStore.allCookies()
        let matching = all.filter { c in
            let d = c.domain.lowercased()
            return d == "hongguoduanju.com" || d == ".hongguoduanju.com" || d.hasSuffix(".hongguoduanju.com")
        }
        guard !matching.isEmpty else {
            return cachedCookieHeader
        }
        let header = matching.map { "\($0.name)=\($0.value)" }.joined(separator: "; ")
        cachedCookieHeader = header
        UserDefaults.standard.set(header, forKey: Self.cookieKey)
        return header
    }

    private func saveProfile(_ next: Profile?) {
        profile = next
        if let next, let data = try? JSONEncoder().encode(next) {
            UserDefaults.standard.set(data, forKey: Self.profileKey)
        } else {
            UserDefaults.standard.removeObject(forKey: Self.profileKey)
        }
    }

    private static func parseProfileJSON(_ data: Data) -> (hasRoot: Bool, profile: Profile?) {
        guard let root = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else {
            return (false, nil)
        }
        let candidates: [[String: Any]?] = [
            root["profile"] as? [String: Any],
            (root["data"] as? [String: Any])?["profile"] as? [String: Any],
            root["user"] as? [String: Any],
            (root["data"] as? [String: Any])?["user"] as? [String: Any]
        ]
        for cand in candidates {
            guard let p = cand else { continue }
            let nick = (
                (p["nickname"] as? String) ??
                (p["name"] as? String) ??
                (p["screen_name"] as? String) ??
                (p["user_name"] as? String) ?? ""
            ).trimmingCharacters(in: .whitespacesAndNewlines)
            let avatar = (
                (p["avatarUrl"] as? String) ??
                (p["avatar_url"] as? String) ??
                (p["avatar"] as? String) ?? ""
            ).trimmingCharacters(in: .whitespacesAndNewlines)
            if !nick.isEmpty {
                return (true, Profile(nickname: nick, avatarUrl: avatar))
            }
        }
        return (true, nil)
    }

    /// Called when the in-app WKWebView directly observes a logged-in profile response or SSR state.
    func acceptWebViewLogin(nickname: String, avatarUrl: String) async {
        let trimmed = nickname.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        lastConfirmedLoginAt = Date()
        saveProfile(Profile(nickname: trimmed, avatarUrl: avatarUrl.trimmingCharacters(in: .whitespacesAndNewlines)))
        _ = await syncCookiesFromWebView()
        try? await Task.sleep(nanoseconds: 500_000_000)
        _ = await syncCookiesFromWebView()
    }

    private var withinLoginGrace: Bool {
        guard let t = lastConfirmedLoginAt else { return false }
        return Date().timeIntervalSince(t) < Self.loginGraceWindow
    }

    @discardableResult
    func refreshProfile() async -> Profile? {
        syncing = true
        defer { syncing = false }

        // 1) Primary: same-origin fetch inside WebKit (always carries HttpOnly session cookies).
        if let res = await webViewFetch(path: "/api/pc/profile", headers: ["Accept": "application/json"]),
           res.status == 200,
           let data = res.text.data(using: .utf8) {
            let parsed = Self.parseProfileJSON(data)
            if let found = parsed.profile {
                lastConfirmedLoginAt = Date()
                saveProfile(found)
                _ = await syncCookiesFromWebView()
                return found
            }
            if parsed.hasRoot && !withinLoginGrace {
                saveProfile(nil)
                return nil
            }
            return profile
        }

        // 2) Fallback: URLSession with snapshotted cookies (never clear profile on fallback miss if already logged in).
        let cookie = await syncCookiesFromWebView()
        guard let cookie, !cookie.isEmpty, let url = URL(string: "\(Self.webOrigin)/api/pc/profile") else {
            return profile
        }
        var req = URLRequest(url: url)
        req.httpMethod = "GET"
        req.setValue(cookie, forHTTPHeaderField: "Cookie")
        req.setValue(Self.pcUserAgent, forHTTPHeaderField: "User-Agent")
        req.setValue("\(Self.webOrigin)/", forHTTPHeaderField: "Referer")
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        do {
            let (data, resp) = try await session.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 200 {
                let parsed = Self.parseProfileJSON(data)
                if let found = parsed.profile {
                    lastConfirmedLoginAt = Date()
                    saveProfile(found)
                    return found
                }
            }
            return profile
        } catch {
            return profile
        }
    }

    func logout() {
        lastConfirmedLoginAt = nil
        cachedCookieHeader = nil
        UserDefaults.standard.removeObject(forKey: Self.cookieKey)
        UserDefaults.standard.removeObject(forKey: Self.profileKey)
        sessionTags.removeAll()
        lastReportedAt.removeAll()
        profile = nil
        lastSyncNote = nil

        Task {
            let dataStore = WKWebsiteDataStore.default()
            let all = await dataStore.httpCookieStore.allCookies()
            for c in all where c.domain.lowercased().contains("hongguoduanju.com") {
                await dataStore.httpCookieStore.deleteCookie(c)
            }
            let records = await dataStore.dataRecords(ofTypes: WKWebsiteDataStore.allWebsiteDataTypes())
            let targets = records.filter { $0.displayName.lowercased().contains("hongguoduanju") }
            if !targets.isEmpty {
                await dataStore.removeData(ofTypes: WKWebsiteDataStore.allWebsiteDataTypes(), for: targets)
            }
        }
    }

    func reportProgressAsync(
        seriesId: String,
        vid: String,
        episodeNumber: Int,
        positionMs: Int64,
        accumulatedMs: Int64,
        force: Bool = false
    ) {
        guard isLoggedIn,
              !seriesId.isEmpty, seriesId.allSatisfy(\.isNumber),
              !vid.isEmpty, vid.allSatisfy(\.isNumber),
              episodeNumber > 0, positionMs >= 0 else { return }

        let key = "\(seriesId):\(vid)"
        let now = Date()
        if !force, let prev = lastReportedAt[key], now.timeIntervalSince(prev) < Self.reportThrottle {
            return
        }
        lastReportedAt[key] = now

        Task { [weak self] in
            guard let self else { return }
            let cookie = await self.syncCookiesFromWebView() ?? ""
            guard let sessionTag = await self.fetchSessionTag(seriesId: seriesId, vid: vid, cookie: cookie) else {
                self.lastSyncNote = "未获取到网页同步会话，请尝试重新登录"
                return
            }
            let payload: [String: Any] = [
                "book_id": seriesId,
                "book_type": 2,
                "is_delete": false,
                "read_timestamp_ms": String(Int64(Date().timeIntervalSince1970 * 1000)),
                "vid": vid,
                "vid_index": String(episodeNumber),
                "current_play_position": String(max(0, positionMs)),
                "player_accumulate_total_time": String(max(1000, accumulatedMs))
            ]
            guard let bodyData = try? JSONSerialization.data(withJSONObject: payload),
                  let bodyString = String(data: bodyData, encoding: .utf8) else { return }

            // Primary: same-origin POST inside WebKit so HttpOnly session cookies & Origin/Referer match natively.
            if let res = await self.webViewFetch(
                path: "/api/pc/watch-history",
                method: "POST",
                headers: [
                    "Content-Type": "application/json; charset=utf-8",
                    "Accept": "application/json",
                    "X-PC-History-Session": sessionTag
                ],
                body: bodyString
            ), res.status > 0 {
                let json = res.text.data(using: .utf8).flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }
                let ok = (json?["isSuccess"] as? Bool) == true
                if ok {
                    self.lastSyncNote = "已同步第 \(episodeNumber) 集到红果"
                    return
                } else if res.status == 401 || res.status == 403 {
                    self.sessionTags.removeValue(forKey: seriesId)
                }
            }

            // Fallback: URLSession
            guard !cookie.isEmpty, let url = URL(string: "\(Self.webOrigin)/api/pc/watch-history") else { return }
            var req = URLRequest(url: url)
            req.httpMethod = "POST"
            req.httpBody = bodyData
            req.setValue("application/json; charset=utf-8", forHTTPHeaderField: "Content-Type")
            req.setValue(cookie, forHTTPHeaderField: "Cookie")
            req.setValue(Self.pcUserAgent, forHTTPHeaderField: "User-Agent")
            req.setValue(Self.webOrigin, forHTTPHeaderField: "Origin")
            req.setValue("\(Self.webOrigin)/player/\(seriesId)/\(vid)", forHTTPHeaderField: "Referer")
            req.setValue(sessionTag, forHTTPHeaderField: "X-PC-History-Session")

            do {
                let (data, resp) = try await self.session.data(for: req)
                let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
                let json = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
                let ok = (json?["isSuccess"] as? Bool) == true
                if ok {
                    self.lastSyncNote = "已同步第 \(episodeNumber) 集到红果"
                } else {
                    if code == 401 || code == 403 || !ok {
                        self.sessionTags.removeValue(forKey: seriesId)
                    }
                    let reason = (json?["reason"] as? String).flatMap { $0.isEmpty ? nil : $0 } ?? "HTTP \(code)"
                    self.lastSyncNote = "同步未生效（\(reason)）"
                }
            } catch {
                // Silent network failure during background sync
            }
        }
    }

    private func fetchSessionTag(seriesId: String, vid: String, cookie: String) async -> String? {
        if let cached = sessionTags[seriesId], Date().timeIntervalSince(cached.at) < Self.sessionTagTTL {
            return cached.tag
        }
        let paths = ["/player/\(seriesId)/\(vid)", "/player/\(seriesId)"]
        for path in paths {
            if let res = await webViewFetch(path: path), (200..<300).contains(res.status),
               let tag = Self.extractSessionTag(from: res.text) {
                sessionTags[seriesId] = (tag, Date())
                return tag
            }
            guard !cookie.isEmpty, let url = URL(string: "\(Self.webOrigin)\(path)") else { continue }
            var req = URLRequest(url: url)
            req.httpMethod = "GET"
            req.setValue(cookie, forHTTPHeaderField: "Cookie")
            req.setValue(Self.pcUserAgent, forHTTPHeaderField: "User-Agent")
            req.setValue("\(Self.webOrigin)/", forHTTPHeaderField: "Referer")
            guard let (data, resp) = try? await session.data(for: req),
                  let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode),
                  let html = String(data: data, encoding: .utf8),
                  let tag = Self.extractSessionTag(from: html) else { continue }
            sessionTags[seriesId] = (tag, Date())
            return tag
        }
        return nil
    }

    private static func extractSessionTag(from html: String) -> String? {
        let patterns = [
            "\"pcHistorySessionTag\"\\s*:\\s*\"([^\"]+)\"",
            "\\\\\"pcHistorySessionTag\\\\\"\\s*:\\s*\\\\\"([^\\\\\"]+)\\\\\""
        ]
        let ns = html as NSString
        for pattern in patterns {
            guard let regex = try? NSRegularExpression(pattern: pattern),
                  let match = regex.firstMatch(in: html, range: NSRange(location: 0, length: ns.length)),
                  match.numberOfRanges >= 2 else { continue }
            let range = match.range(at: 1)
            if range.location != NSNotFound {
                let value = ns.substring(with: range)
                if !value.isEmpty { return value }
            }
        }
        return nil
    }
}
