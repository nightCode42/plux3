// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import AVFoundation
import Contacts
import CoreLocation
import Flutter
import Foundation
import LocalAuthentication
import Photos
import UIKit
import UserNotifications

/// The device actions that need the platform (SEC-080): the share sheet and
/// permission prompts. Nothing here reads a picked file's contents; a file
/// is handed to the share sheet by URL, and only files under the app's
/// container can be shared.
final class PluxDevice: NSObject, CLLocationManagerDelegate {
  private var location: CLLocationManager?
  private var locationResult: FlutterResult?

  // MARK: Share

  func share(_ args: [String: Any], result: @escaping FlutterResult) {
    var items: [Any] = []
    if let text = args["text"] as? String { items.append(text) }
    if let url = args["url"] as? String, let u = URL(string: url) { items.append(u) }
    if let path = args["filePath"] as? String {
      let file = URL(fileURLWithPath: path).resolvingSymlinksInPath()
      guard Self.insideContainer(file) else {
        return result(FlutterError(code: "PLUX_PLATFORM", message: "file", details: nil))
      }
      items.append(file)
    }
    guard !items.isEmpty, let top = Self.topViewController() else {
      return result(FlutterError(code: "PLUX_PLATFORM", message: "presenter", details: nil))
    }
    let sheet = UIActivityViewController(activityItems: items, applicationActivities: nil)
    // iPad presents the sheet as a popover anchored to the screen's centre.
    if let popover = sheet.popoverPresentationController {
      popover.sourceView = top.view
      popover.sourceRect = CGRect(x: top.view.bounds.midX, y: top.view.bounds.midY, width: 0, height: 0)
      popover.permittedArrowDirections = []
    }
    sheet.completionWithItemsHandler = { _, _, _, _ in result(nil) }
    top.present(sheet, animated: true)
  }

  private static func insideContainer(_ file: URL) -> Bool {
    let home = URL(fileURLWithPath: NSHomeDirectory()).resolvingSymlinksInPath().path
    return file.path.hasPrefix(home + "/")
  }

  private static func topViewController() -> UIViewController? {
    let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
    var top = scene?.windows.first(where: { $0.isKeyWindow })?.rootViewController
    while let presented = top?.presentedViewController { top = presented }
    return top
  }

  // MARK: Permissions

  func requestPermission(_ args: [String: Any], result: @escaping FlutterResult) {
    switch args["permission"] as? String {
    case "camera":
      AVCaptureDevice.requestAccess(for: .video) { ok in DispatchQueue.main.async { result(ok) } }
    case "photos":
      PHPhotoLibrary.requestAuthorization(for: .readWrite) { status in
        DispatchQueue.main.async { result(status == .authorized || status == .limited) }
      }
    case "contacts":
      CNContactStore().requestAccess(for: .contacts) { ok, _ in DispatchQueue.main.async { result(ok) } }
    case "notifications":
      UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .badge, .sound]) { ok, _ in
        DispatchQueue.main.async { result(ok) }
      }
    case "biometrics":
      let context = LAContext()
      var error: NSError?
      guard context.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &error) else {
        return result(false)
      }
      context.evaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, localizedReason: "Allow biometric sign-in") { ok, _ in
        DispatchQueue.main.async { result(ok) }
      }
    case "location":
      requestLocation(result)
    default:
      result(FlutterError(code: "PLUX_PLATFORM", message: "permission", details: nil))
    }
  }

  private func requestLocation(_ result: @escaping FlutterResult) {
    let manager = CLLocationManager()
    switch manager.authorizationStatus {
    case .authorizedWhenInUse, .authorizedAlways:
      return result(true)
    case .denied, .restricted:
      return result(false)
    default:
      break
    }
    locationResult?(false)
    locationResult = result
    manager.delegate = self
    location = manager
    manager.requestWhenInUseAuthorization()
  }

  func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
    guard manager.authorizationStatus != .notDetermined, let result = locationResult else { return }
    locationResult = nil
    location = nil
    result(manager.authorizationStatus == .authorizedWhenInUse || manager.authorizationStatus == .authorizedAlways)
  }
}
