// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import CryptoKit
import Flutter
import Foundation
import Security

/// The device keys of the Plux runtime (SEC-001, SEC-021, ADR-0012): P-256
/// private keys that never leave the Secure Enclave, or the Keychain where
/// no Secure Enclave exists (the simulator, reported as "software"). A key
/// is non-exportable; only its public point and signatures cross the channel.
final class PluxKeys {
  private static let tagPrefix = "dev.plux.keys."
  private static let purposes: Set<String> = ["dpop", "agreement"]
  private static let queue = DispatchQueue(label: "dev.plux.keys", qos: .userInitiated)

  /// Handles one of keyCreate, keyPublic, keySign and keyDelete. Keychain and
  /// Secure Enclave work runs on a serial queue; `result` is called on the
  /// main queue.
  func handle(_ method: String, _ args: [String: Any], result: @escaping FlutterResult) {
    guard let alias = Self.alias(args) else { return result(Self.fail("PLUX_PLATFORM", "alias")) }
    switch method {
    case "keyCreate":
      guard let purpose = args["purpose"] as? String, Self.purposes.contains(purpose) else {
        return result(Self.fail("PLUX_PLATFORM", "purpose"))
      }
      Self.run(result) { try Self.create(alias, purpose) }
    case "keyPublic":
      Self.run(result) { try Self.publicInfo(alias) }
    case "keySign":
      guard let data = (args["data"] as? FlutterStandardTypedData)?.data else {
        return result(Self.fail("PLUX_PLATFORM", "data"))
      }
      Self.run(result) { try Self.sign(alias, data) }
    case "keyDelete":
      Self.run(result) {
        try Self.delete(alias)
        return nil
      }
    default:
      result(FlutterMethodNotImplemented)
    }
  }

  // MARK: - Dispatch

  private struct KeyError: Error {
    let code: String
    let message: String
  }

  private static func fail(_ code: String, _ message: String) -> FlutterError {
    FlutterError(code: code, message: message, details: nil)
  }

  private static func run(_ result: @escaping FlutterResult, _ work: @escaping () throws -> Any?) {
    queue.async {
      let value: Any?
      do {
        value = try work()
      } catch let e as KeyError {
        value = fail(e.code, e.message)
      } catch {
        value = fail("PLUX_PLATFORM", "key")
      }
      DispatchQueue.main.async { result(value) }
    }
  }

  private static func alias(_ args: [String: Any]) -> String? {
    guard let a = args["alias"] as? String, !a.isEmpty, a.count <= 64,
          a.allSatisfy({ $0.isASCII && ($0.isLowercase || $0.isNumber || $0 == "." || $0 == "_" || $0 == "-") })
    else { return nil }
    return a
  }

  /// Only the failing step and its numeric code are reported, never key
  /// material or the system's error text.
  private static func platform(_ step: String, _ code: Int) -> KeyError {
    KeyError(code: "PLUX_PLATFORM", message: "\(step) \(code)")
  }

  private static func platform(_ step: String, cfError: Unmanaged<CFError>?) -> KeyError {
    platform(step, cfError.map { CFErrorGetCode($0.takeRetainedValue()) } ?? 0)
  }

  // MARK: - Keychain

  private static func tag(_ alias: String) -> Data {
    Data((tagPrefix + alias).utf8)
  }

  private static func query(_ alias: String) -> [String: Any] {
    [kSecClass as String: kSecClassKey,
     kSecAttrApplicationTag as String: tag(alias),
     kSecAttrKeyClass as String: kSecAttrKeyClassPrivate]
  }

  private static func delete(_ alias: String) throws {
    let status = SecItemDelete(query(alias) as CFDictionary)
    guard status == errSecSuccess || status == errSecItemNotFound else {
      throw platform("keychain", Int(status))
    }
  }

  /// The private key, its purpose label and whether it lives in the Secure
  /// Enclave, or nil when the alias has no key.
  private static func lookup(_ alias: String) throws -> (key: SecKey, purpose: String, enclave: Bool)? {
    var q = query(alias)
    q[kSecReturnRef as String] = true
    q[kSecReturnAttributes as String] = true
    q[kSecMatchLimit as String] = kSecMatchLimitOne
    var item: CFTypeRef?
    let status = SecItemCopyMatching(q as CFDictionary, &item)
    if status == errSecItemNotFound { return nil }
    // A CoreFoundation type cannot be cast conditionally: its type ID is
    // checked first, then the cast cannot fail.
    guard status == errSecSuccess, let found = item as? [String: Any],
          let value = found[kSecValueRef as String] else {
      throw platform("keychain", Int(status))
    }
    let ref = value as CFTypeRef
    guard CFGetTypeID(ref) == SecKeyGetTypeID() else { throw platform("keychain", 1) }
    let key = ref as! SecKey
    let purpose = found[kSecAttrLabel as String] as? String ?? ""
    let attrs = SecKeyCopyAttributes(key) as? [String: Any]
    let enclave = (attrs?[kSecAttrTokenID as String] as? String) == (kSecAttrTokenIDSecureEnclave as String)
    return (key, purpose, enclave)
  }

  private static func create(_ alias: String, _ purpose: String) throws -> [String: Any] {
    try delete(alias)
    let enclave = SecureEnclave.isAvailable
    var privateAttrs: [String: Any] = [
      kSecAttrIsPermanent as String: true,
      kSecAttrApplicationTag as String: tag(alias),
      kSecAttrLabel as String: purpose,
    ]
    var attrs: [String: Any] = [
      kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
      kSecAttrKeySizeInBits as String: 256,
    ]
    if enclave {
      var accessError: Unmanaged<CFError>?
      guard let access = SecAccessControlCreateWithFlags(
        kCFAllocatorDefault, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly, .privateKeyUsage, &accessError)
      else { throw platform("access", cfError: accessError) }
      privateAttrs[kSecAttrAccessControl as String] = access
      attrs[kSecAttrTokenID as String] = kSecAttrTokenIDSecureEnclave
    } else {
      privateAttrs[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
    }
    attrs[kSecPrivateKeyAttrs as String] = privateAttrs
    var createError: Unmanaged<CFError>?
    guard let key = SecKeyCreateRandomKey(attrs as CFDictionary, &createError) else {
      throw platform("keygen", cfError: createError)
    }
    return try info(key, enclave: enclave)
  }

  private static func publicInfo(_ alias: String) throws -> [String: Any]? {
    guard let found = try lookup(alias) else { return nil }
    return try info(found.key, enclave: found.enclave)
  }

  /// The public point of `key` as {x, y, storage, chain}; the chain is empty
  /// because iOS has no key attestation chain here.
  private static func info(_ key: SecKey, enclave: Bool) throws -> [String: Any] {
    guard let pub = SecKeyCopyPublicKey(key) else { throw platform("public", 0) }
    var exportError: Unmanaged<CFError>?
    guard let raw = SecKeyCopyExternalRepresentation(pub, &exportError) as Data? else {
      throw platform("public", cfError: exportError)
    }
    // Uncompressed point: 0x04 || X (32) || Y (32).
    guard raw.count == 65, raw[raw.startIndex] == 0x04 else { throw platform("public", 1) }
    let x = raw.subdata(in: (raw.startIndex + 1)..<(raw.startIndex + 33))
    let y = raw.subdata(in: (raw.startIndex + 33)..<(raw.startIndex + 65))
    return [
      "x": FlutterStandardTypedData(bytes: x),
      "y": FlutterStandardTypedData(bytes: y),
      "storage": enclave ? "secureEnclave" : "software",
      "chain": [Any](),
    ]
  }

  // MARK: - Signing

  private static func sign(_ alias: String, _ data: Data) throws -> FlutterStandardTypedData {
    guard let found = try lookup(alias) else {
      throw KeyError(code: "PLUX_KEY_MISSING", message: "alias")
    }
    guard found.purpose == "dpop" else { throw platform("purpose", 0) }
    var signError: Unmanaged<CFError>?
    guard let der = SecKeyCreateSignature(found.key, .ecdsaSignatureMessageX962SHA256, data as CFData, &signError) as Data?
    else { throw platform("sign", cfError: signError) }
    guard let raw = derToRaw(der) else { throw platform("signature", 0) }
    return FlutterStandardTypedData(bytes: raw)
  }

  /// Converts an ASN.1 DER ECDSA signature (SEQUENCE of two INTEGERs r, s)
  /// to the 64 bytes r||s of JWS ES256, each value big-endian and left-padded
  /// to 32 bytes. Returns nil unless the input is exactly one well-formed
  /// SEQUENCE of two positive, minimally encoded INTEGERs of at most 32
  /// significant bytes, with nothing before or after it.
  static func derToRaw(_ der: Data) -> Data? {
    let b = [UInt8](der)
    // SEQUENCE header: the tag and a short-form length covering the rest.
    guard b.count >= 8, b[0] == 0x30, b[1] < 0x80, Int(b[1]) == b.count - 2 else { return nil }
    guard let r = integer(b, at: 2), let s = integer(b, at: r.next), s.next == b.count else {
      return nil
    }
    return Data(r.value + s.value)
  }

  /// Reads the INTEGER at `at`: its value left-padded to 32 bytes, and the
  /// offset after it.
  private static func integer(_ b: [UInt8], at: Int) -> (value: [UInt8], next: Int)? {
    guard at + 2 <= b.count, b[at] == 0x02, b[at + 1] >= 1, b[at + 1] < 0x80 else { return nil }
    let length = Int(b[at + 1])
    let start = at + 2
    guard start + length <= b.count else { return nil }
    var value = Array(b[start..<(start + length)])
    // Positive: the sign bit is clear. Minimal: a leading zero only where the
    // next byte would otherwise read as negative.
    guard value[0] & 0x80 == 0 else { return nil }
    if value.count > 1 && value[0] == 0 {
      guard value[1] & 0x80 != 0 else { return nil }
      value.removeFirst()
    }
    guard value.count <= 32, value.contains(where: { $0 != 0 }) else { return nil }
    return ([UInt8](repeating: 0, count: 32 - value.count) + value, start + length)
  }
}
