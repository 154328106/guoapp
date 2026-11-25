import XCTest
@testable import STV

final class HGProtocolConfigTests: XCTestCase {
    private func repoData() throws -> Data {
        try Data(contentsOf: URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("configs/hongguo-protocol.json"))
    }
    private func mutate(_ edit: (inout [String: Any]) -> Void) throws -> Data {
        var j = try XCTUnwrap(JSONSerialization.jsonObject(with: repoData()) as? [String: Any])
        edit(&j)
        return try JSONSerialization.data(withJSONObject: j)
    }

    func testRepoJsonEqualsBuiltinDefault() throws {
        let parsed = try XCTUnwrap(HGProtocolConfig.parse(try repoData()))
        XCTAssertEqual(parsed, HGProtocolConfig.builtin)
        XCTAssertEqual(parsed.field21, 312)
    }

    func testRejectsMalformedOrUnsafeConfigs() throws {
        XCTAssertNil(HGProtocolConfig.parse(Data("not json".utf8)))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { $0["schema"] = 2 }))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { $0["configVersion"] = 0 }))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { $0.removeValue(forKey: "medusa") }))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { $0["medusa"] = ["field21": 70000] }))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { var p = $0["preload"] as! [String: Any]; p["hosts"] = ["evil.example.com"]; $0["preload"] = p }))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { var p = $0["preload"] as! [String: Any]; p["hosts"] = ["fqnovel.com.evil.com"]; $0["preload"] = p }))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { var t = $0["tab"] as! [String: Any]; t["path"] = "../etc/passwd"; $0["tab"] = t }))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { var q = $0["appQuery"] as! [String]; q.append("device_id=1"); $0["appQuery"] = q }))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { var p = $0["preload"] as! [String: Any]; p["drScene"] = "x"; $0["preload"] = p }))
        XCTAssertNil(HGProtocolConfig.parse(try mutate { var f = $0["fallbackIdentity"] as! [String: Any]; f["did"] = "abc"; $0["fallbackIdentity"] = f }))
        XCTAssertNil(HGProtocolConfig.parse(Data(repeating: 0x78, count: 70_000)))
    }

    func testAcceptsHotUpdateOfField21AndOmission() throws {
        XCTAssertEqual(HGProtocolConfig.parse(try mutate { $0["medusa"] = ["field21": 400] })?.field21, 400)
        let omitted = try XCTUnwrap(HGProtocolConfig.parse(try mutate { $0["medusa"] = ["field21": NSNull()] }))
        XCTAssertNil(omitted.field21)
        let p = HGProtocolConfig.parse(try mutate { var p = $0["preload"] as! [String: Any]; p["path"] = "novel/player/multi_video_model/preload/v1"; $0["preload"] = p })
        XCTAssertEqual(p?.preloadPath, "/novel/player/multi_video_model/preload/v1")
    }

    func testField21ChangesTheSignedMessage() throws {
        let pairs = [("aid", "8662"), ("version_name", "7.4.5.32"), ("device_id", "1000000000000001")]
        let entropy = (0..<16).map { UInt8($0 + 1) }
        func sign(_ f: Int?) throws -> String? {
            try HGCurrentPlayer.signPlaybackV5(pairs: pairs, nowMillis: 1795500000123, body: Array(#"{"t":1}"#.utf8), includeBodyHeaders: true,
                                               entropy: entropy, processNonce: "fixedProcessNonce_000123", processStarted: 1700000000000, field21: f).headers["x-medusa"]
        }
        XCTAssertNotEqual(try sign(312), try sign(400))
        XCTAssertNotEqual(try sign(312), try sign(nil))
        XCTAssertEqual(try sign(312), try sign(312))
    }
}
