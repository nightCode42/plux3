// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import Flutter
import FlutterPluginRegistrant
import UIKit

/// The host's side of the Plux module (HST-033). One FlutterEngine runs
/// the module: it starts when a native screen first opens a Plux page and
/// stays warm while the app runs, so later pages open without starting
/// Flutter again. The module and the host talk on the `dev.plux/host`
/// channel (plux_module/lib/plux_module.dart).
final class PluxHost {
  /// The app's one host.
  static let shared = PluxHost()

  private lazy var engine: FlutterEngine = start()
  private var channel: FlutterMethodChannel?
  private var nativeScreen: FlutterResult?

  /// The runtime's settings the module asks for, from the launch
  /// environment: PLUX_ENDPOINT, PLUX_APP_ID, PLUX_ENVIRONMENT,
  /// PLUX_HOST_BUILD and PLUX_ROOT_KEYS (comma-separated `keyId:hex`). The
  /// UI tests pass the test server's.
  private let settings: [String: Any] = {
    let env = ProcessInfo.processInfo.environment
    var settings: [String: Any] = [:]
    for (key, name) in [
      ("endpoint", "PLUX_ENDPOINT"), ("appId", "PLUX_APP_ID"),
      ("environment", "PLUX_ENVIRONMENT"), ("hostBuild", "PLUX_HOST_BUILD"),
    ] {
      if let value = env[name], !value.isEmpty { settings[key] = value }
    }
    if let keys = env["PLUX_ROOT_KEYS"] {
      settings["rootKeys"] = keys.split(separator: ",").map(String.init)
    }
    return settings
  }()

  /// The Plux page at `route`, full screen.
  func page(_ route: String) -> UIViewController {
    let flutter = controller(route)
    flutter.modalPresentationStyle = .fullScreen
    return flutter
  }

  /// A view controller that shows the Plux page at `route`. The engine
  /// shows one view controller at a time, so it leaves the previous one.
  func controller(_ route: String) -> FlutterViewController {
    let engine = self.engine
    engine.viewController = nil
    channel?.invokeMethod("open", arguments: ["route": route])
    return FlutterViewController(engine: engine, nibName: nil, bundle: nil)
  }

  private func start() -> FlutterEngine {
    let engine = FlutterEngine(name: "plux")
    let channel = FlutterMethodChannel(name: "dev.plux/host", binaryMessenger: engine.binaryMessenger)
    channel.setMethodCallHandler { [weak self] call, result in
      self?.handle(call, result: result)
    }
    engine.run()
    GeneratedPluginRegistrant.register(with: engine)
    self.channel = channel
    return engine
  }

  private func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    switch call.method {
    case "config":
      result(settings)
    case "openNative":
      let screen = (call.arguments as? [String: Any])?["screen"] as? String
      guard screen == "settings", let top = topViewController() else {
        result(FlutterError(code: "screen", message: "the host cannot show the native screen now", details: screen))
        return
      }
      nativeScreenClosed()
      nativeScreen = result
      top.present(SettingsViewController { [weak self] in self?.nativeScreenClosed() }, animated: true)
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  // The native screen the module opened has closed.
  private func nativeScreenClosed() {
    nativeScreen?(nil)
    nativeScreen = nil
  }

  private func topViewController() -> UIViewController? {
    let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
    var top = scene?.windows.first { $0.isKeyWindow }?.rootViewController
    while let presented = top?.presentedViewController {
      top = presented
    }
    return top
  }
}
