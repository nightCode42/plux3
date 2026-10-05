// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import Flutter
import Foundation
import Security

/// The platform services of the Plux runtime (ADR-0029): where the release
/// store lives, and secrets kept in the Keychain, readable only on this
/// device after its first unlock, so that no secret is ever written in the
/// clear. The device actions that need the platform, the share sheet and
/// permission prompts, run here too (SEC-080). Bundle data never crosses
/// this channel.
public class PluxFlutterPlugin: NSObject, FlutterPlugin {
  private static let service = "dev.plux.secrets"
  private let device = PluxDevice()

  public static func register(with registrar: FlutterPluginRegistrar) {
    let channel = FlutterMethodChannel(name: "dev.plux/runtime", binaryMessenger: registrar.messenger())
    registrar.addMethodCallDelegate(PluxFlutterPlugin(), channel: channel)
  }

  public func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    let args = call.arguments as? [String: Any] ?? [:]
    switch call.method {
    case "storageDirectory":
      result(storageDirectory())
    case "secretRead":
      guard let name = Self.name(args) else { return result(Self.badName) }
      result(read(name))
    case "secretWrite":
      guard let name = Self.name(args), let value = args["value"] as? String else { return result(Self.badName) }
      let status = write(name, value)
      result(status == errSecSuccess ? nil : FlutterError(code: "PLUX_PLATFORM", message: "keychain \(status)", details: nil))
    case "secretDelete":
      guard let name = Self.name(args) else { return result(Self.badName) }
      _ = SecItemDelete(query(name) as CFDictionary)
      result(nil)
    case "share":
      device.share(args, result: result)
    case "permissionRequest":
      device.requestPermission(args, result: result)
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  private static let badName = FlutterError(code: "PLUX_PLATFORM", message: "name", details: nil)

  private static func name(_ args: [String: Any]) -> String? {
    guard let n = args["name"] as? String, !n.isEmpty, n.count <= 64,
          n.allSatisfy({ $0.isLowercase || $0.isNumber || $0 == "." || $0 == "_" || $0 == "-" }) else { return nil }
    return n
  }

  /// Application Support/plux, excluded from backups: releases are
  /// re-downloaded, never restored onto another device.
  private func storageDirectory() -> String? {
    guard var url = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else { return nil }
    url.appendPathComponent("plux", isDirectory: true)
    try? FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
    var values = URLResourceValues()
    values.isExcludedFromBackup = true
    try? url.setResourceValues(values)
    return url.path
  }

  private func query(_ name: String) -> [String: Any] {
    [kSecClass as String: kSecClassGenericPassword,
     kSecAttrService as String: Self.service,
     kSecAttrAccount as String: name]
  }

  private func read(_ name: String) -> String? {
    var q = query(name)
    q[kSecReturnData as String] = true
    q[kSecMatchLimit as String] = kSecMatchLimitOne
    var item: CFTypeRef?
    guard SecItemCopyMatching(q as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return nil }
    return String(data: data, encoding: .utf8)
  }

  private func write(_ name: String, _ value: String) -> OSStatus {
    _ = SecItemDelete(query(name) as CFDictionary)
    var q = query(name)
    q[kSecValueData as String] = Data(value.utf8)
    q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
    return SecItemAdd(q as CFDictionary, nil)
  }
}
