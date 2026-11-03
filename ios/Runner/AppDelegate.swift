import Flutter
import UIKit
import DuanjuCore
import CFNetwork
import CoreTelephony
import AVFoundation
import MediaPlayer

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  private var originalBrightness: CGFloat?
  private let volumeView = MPVolumeView(frame: CGRect(x: -100, y: -100, width: 1, height: 1))
  // 国行 iOS 首次启动会弹「允许使用无线数据」。在用户点允许之前，Go 核心（BSD socket）发出的请求
  // 会让本进程网络一直失败直到重启，所以 Dart 侧等原生 URLSession 探测成功后再发请求。
  // 实测首启时 CTCellularData.restrictedState 在用户点允许之前就可能不是 unknown，不能作为放行依据，
  // 只认探测成功；成功过一次就记进 UserDefaults，之后启动直接放行。
  private let cellularData = CTCellularData()
  private static let networkGrantedKey = "networkGrantedOnce"
  private var networkProbeSucceeded = UserDefaults.standard.bool(forKey: AppDelegate.networkGrantedKey)
  private var networkProbing = false

  private func probeNetwork() {
    guard !networkProbing, !networkProbeSucceeded,
          let url = URL(string: "https://www.apple.com/library/test/success.html") else { return }
    networkProbing = true
    var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 8)
    request.httpMethod = "HEAD"
    URLSession.shared.dataTask(with: request) { _, response, _ in
      DispatchQueue.main.async {
        self.networkProbing = false
        if response is HTTPURLResponse {
          self.networkProbeSucceeded = true
          UserDefaults.standard.set(true, forKey: AppDelegate.networkGrantedKey)
        }
      }
    }.resume()
  }

  private var keyWindow: UIWindow? {
    UIApplication.shared.connectedScenes
      .compactMap { $0 as? UIWindowScene }
      .flatMap { $0.windows }
      .first { $0.isKeyWindow }
  }

  private var screen: UIScreen? { keyWindow?.screen }

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    ZgjEnsureCoreLinked()
    // 授权状态一变（用户点了允许）就立刻再探测一次，不用等下一轮轮询
    cellularData.cellularDataRestrictionDidUpdateNotifier = { _ in
      DispatchQueue.main.async { self.probeNetwork() }
    }
    probeNetwork()
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    if let registrar = engineBridge.pluginRegistry.registrar(forPlugin: "DeviceSettings") {
      let channel = FlutterMethodChannel(name: "duanju/device", binaryMessenger: registrar.messenger())
      channel.setMethodCallHandler { call, result in
        if call.method == "battery" {
          let device = UIDevice.current
          device.isBatteryMonitoringEnabled = true
          guard device.batteryLevel >= 0 else {
            result(nil)
            return
          }
          result(["level": Int((device.batteryLevel * 100).rounded()),
                  "charging": device.batteryState == .charging || device.batteryState == .full])
          return
        }
        let arguments = call.arguments as? [String: Any]
        switch call.method {
        case "getBrightness":
          result(self.screen.map { Double($0.brightness) })
          return
        case "setBrightness":
          guard let screen = self.screen, let value = arguments?["brightness"] as? Double else {
            result(nil)
            return
          }
          if self.originalBrightness == nil { self.originalBrightness = screen.brightness }
          screen.brightness = CGFloat(min(max(value, 0.01), 1))
          result(true)
          return
        case "resetBrightness":
          if let original = self.originalBrightness { self.screen?.brightness = original }
          self.originalBrightness = nil
          result(true)
          return
        case "networkReady":
          if self.networkProbeSucceeded {
            result("ready")
          } else {
            self.probeNetwork()
            result("pending")
          }
          return
        case "volume":
          result(Double(AVAudioSession.sharedInstance().outputVolume))
          return
        case "setVolume":
          if let value = arguments?["volume"] as? Double {
            if self.volumeView.superview == nil { self.keyWindow?.addSubview(self.volumeView) }
            let slider = self.volumeView.subviews.first { $0 is UISlider } as? UISlider
            slider?.value = Float(min(max(value, 0), 1))
          }
          result(nil)
          return
        default:
          break
        }
        guard call.method == "systemProxy" else {
          result(FlutterMethodNotImplemented)
          return
        }
        let settings = CFNetworkCopySystemProxySettings()?.takeRetainedValue() as? [String: Any] ?? [:]
        func address(_ prefix: String) -> String {
          guard (settings["\(prefix)Enable"] as? NSNumber)?.boolValue == true,
                let host = settings["\(prefix)Proxy"] as? String,
                let port = settings["\(prefix)Port"] as? NSNumber,
                !host.isEmpty, port.intValue > 0 else { return "" }
          let name = host.contains(":") ? "[\(host)]" : host
          return "http://\(name):\(port.intValue)"
        }
        let http = address("HTTP")
        let https = address("HTTPS")
        result(["http": http, "https": https.isEmpty ? http : https,
                "bypass": settings["ExceptionsList"] as? [String] ?? [],
                "pac": (settings["ProxyAutoConfigEnable"] as? NSNumber)?.boolValue == true])
      }
    }
  }
}
