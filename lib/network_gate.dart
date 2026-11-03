import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';

/// 国行 iOS 首次启动会弹「允许使用无线数据」。用户点允许之前，Go 核心发出的请求
/// 会让整个进程的网络一直失败（只能大退重开），所以网络请求先在这里等原生侧放行：
/// 原生 URLSession 探测成功（成功过一次之后的启动直接放行）。最多等 60 秒，之后照常发请求。
/// 只在 iOS 主 isolate 生效；放行后不再询问。
abstract final class NetworkGate {
  static const _channel = MethodChannel('duanju/device');
  static Future<void>? _ready;

  static Future<void> wait() {
    if (!Platform.isIOS) return Future.value();
    return _ready ??= _poll();
  }

  static Future<void> _poll() async {
    final deadline = DateTime.now().add(const Duration(seconds: 60));
    while (DateTime.now().isBefore(deadline)) {
      try {
        final state = await _channel.invokeMethod<String>('networkReady');
        // ready / 旧原生返回 null：不再等
        if (state != 'pending') return;
      } catch (_) {
        return;
      }
      await Future<void>.delayed(const Duration(milliseconds: 500));
    }
  }
}
