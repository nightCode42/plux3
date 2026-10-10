// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import DeviceCheck
import Flutter
import Foundation

/// The App Attest side of device attestation (SEC-004, SEC-025, ADR-0012):
/// a new key per registration, attested over the binding hash, and later
/// assertions with that key. Key identifiers cross the channel as the raw
/// bytes App Attest's base64 identifier encodes. A device without App Attest
/// (the simulator, some older hardware) answers `UNSUPPORTED`, which a
/// development build may turn into development evidence (SEC-008); every
/// other failure is `PLUX_PLATFORM` with the step and the numeric error code,
/// never the system's error text, a key identifier or an attestation.
final class PluxAttestation {
  private static let service = DCAppAttestService.shared

  /// Handles attestationSupported, appAttestKey, appAttestAttest and
  /// appAttestAssert; `result` is called on the main queue.
  func handle(_ method: String, _ args: [String: Any], result: @escaping FlutterResult) {
    switch method {
    case "attestationSupported":
      result(Self.service.isSupported)
    case "appAttestKey":
      guard Self.service.isSupported else { return result(Self.unsupported) }
      Self.service.generateKey { keyId, error in
        guard let keyId = keyId, error == nil, let raw = Data(base64Encoded: keyId) else {
          return Self.finish(result, Self.failure("appAttestKey", error))
        }
        Self.finish(result, FlutterStandardTypedData(bytes: raw))
      }
    case "appAttestAttest":
      guard let keyId = Self.keyId(args), let hash = Self.hash(args) else {
        return result(Self.fail("PLUX_PLATFORM", "argument"))
      }
      guard Self.service.isSupported else { return result(Self.unsupported) }
      Self.service.attestKey(keyId, clientDataHash: hash) { object, error in
        guard let object = object, error == nil else {
          return Self.finish(result, Self.failure("appAttestAttest", error))
        }
        Self.finish(result, FlutterStandardTypedData(bytes: object))
      }
    case "appAttestAssert":
      guard let keyId = Self.keyId(args), let hash = Self.hash(args) else {
        return result(Self.fail("PLUX_PLATFORM", "argument"))
      }
      guard Self.service.isSupported else { return result(Self.unsupported) }
      Self.service.generateAssertion(keyId, clientDataHash: hash) { assertion, error in
        guard let assertion = assertion, error == nil else {
          return Self.finish(result, Self.failure("appAttestAssert", error))
        }
        Self.finish(result, FlutterStandardTypedData(bytes: assertion))
      }
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  // MARK: - Arguments

  private static let unsupported = fail("UNSUPPORTED", "appAttest")

  /// The App Attest key identifier: the raw bytes over the channel, App
  /// Attest's own base64 text.
  private static func keyId(_ args: [String: Any]) -> String? {
    guard let raw = (args["keyId"] as? FlutterStandardTypedData)?.data, !raw.isEmpty else { return nil }
    return raw.base64EncodedString()
  }

  private static func hash(_ args: [String: Any]) -> Data? {
    guard let data = (args["clientDataHash"] as? FlutterStandardTypedData)?.data, !data.isEmpty else { return nil }
    return data
  }

  // MARK: - Replies

  private static func fail(_ code: String, _ message: String) -> FlutterError {
    FlutterError(code: code, message: message, details: nil)
  }

  /// Only the failing step and the numeric code of a DeviceCheck error are
  /// reported. `featureUnsupported` is the unsupported answer.
  private static func failure(_ step: String, _ error: Error?) -> FlutterError {
    guard let error = error else { return fail("PLUX_PLATFORM", step) }
    if let dc = error as? DCError, dc.code == .featureUnsupported { return unsupported }
    return fail("PLUX_PLATFORM", "\(step) \((error as NSError).code)")
  }

  /// DeviceCheck calls back on an arbitrary queue; Flutter replies belong on
  /// the main queue.
  private static func finish(_ result: @escaping FlutterResult, _ value: Any) {
    DispatchQueue.main.async { result(value) }
  }
}
