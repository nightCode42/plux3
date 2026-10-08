// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import CryptoKit
import Flutter
import Foundation
import Security

/// HTTP/2 to the Plux server over `URLSession` with its certificate chain
/// pinned (SEC-041, SYN-010). `cupertino_http` gives no hook for a
/// server-trust challenge, so the plugin runs these requests: the system
/// evaluates the trust first, and the connection is accepted only if the
/// SPKI SHA-256 of some certificate in the validated chain is a pin. A
/// mismatch cancels the challenge, so no request byte is sent, and is
/// reported as `PLUX_PIN_MISMATCH`; every other failure is `PLUX_HTTP` with
/// the numeric error code and nothing of the request.
///
/// Dart pulls (a platform channel reaches only the root isolate from
/// native code, and the sync and data isolates are background isolates):
/// `httpOpen` starts a request and answers with the status and headers,
/// `httpRead` with the next chunk of the body or null at its end, and
/// `httpClose` ends the request. The task is suspended while a few chunks
/// wait unread, which is the download's flow control. All state lives on
/// one serial queue; `result` is called on the main queue.
///
/// A session serves one set of pins. When Dart sends a different set
/// (signed metadata replaced the pins), a new session is made, and the old
/// one finishes the requests it has.
final class PluxPinnedHttp {
  private static let queue = DispatchQueue(label: "dev.plux.pinned-http")
  private var current: PluxPinnedSession?
  private var exchanges: [Int: PluxExchange] = [:]
  private var nextId = 0

  /// Handles httpOpen, httpRead and httpClose.
  func handle(_ method: String, _ args: [String: Any], result: @escaping FlutterResult) {
    Self.queue.async {
      switch method {
      case "httpOpen":
        self.open(args, result)
      case "httpRead":
        guard let id = args["id"] as? Int, let exchange = self.exchanges[id] else {
          return Self.reply(result, nil)
        }
        exchange.read(result)
      case "httpClose":
        if let id = args["id"] as? Int { self.exchanges.removeValue(forKey: id)?.cancel() }
        Self.reply(result, nil)
      default:
        Self.reply(result, FlutterMethodNotImplemented)
      }
    }
  }

  private func open(_ args: [String: Any], _ result: @escaping FlutterResult) {
    guard let text = args["url"] as? String, let url = URL(string: text), let host = url.host,
          let pinned = args["pins"] as? [String] else {
      return Self.reply(result, Self.error("argument"))
    }
    let digests = pinned.compactMap { Data(base64Encoded: $0) }.filter { $0.count == 32 }
    guard !digests.isEmpty, digests.count == pinned.count else { return Self.reply(result, Self.error("pins")) }
    let key = host + "|" + pinned.sorted().joined(separator: ",")
    if current?.key != key {
      current?.retire()
      current = PluxPinnedSession(key: key, pins: Set(digests), queue: Self.queue)
    }
    var request = URLRequest(url: url)
    request.httpMethod = args["method"] as? String ?? "GET"
    for (name, value) in args["headers"] as? [String: String] ?? [:] {
      request.setValue(value, forHTTPHeaderField: name)
    }
    if let agent = args["userAgent"] as? String, !agent.isEmpty {
      request.setValue(agent, forHTTPHeaderField: "User-Agent")
    }
    request.httpBody = (args["body"] as? FlutterStandardTypedData)?.data
    nextId += 1
    let exchange = PluxExchange(
      id: nextId,
      follow: args["followRedirects"] as? Bool ?? true,
      maxRedirects: args["maxRedirects"] as? Int ?? 5,
      opened: result)
    exchanges[nextId] = exchange
    let id = nextId
    exchange.discard = { [weak self] in _ = self?.exchanges.removeValue(forKey: id) }
    current!.start(request, exchange)
  }

  fileprivate static func reply(_ result: @escaping FlutterResult, _ value: Any?) {
    DispatchQueue.main.async { result(value) }
  }

  fileprivate static func error(_ detail: String) -> FlutterError {
    FlutterError(code: "PLUX_HTTP", message: detail, details: nil)
  }
}

/// One request: the task's callbacks on one side, Dart's pulls on the other.
fileprivate final class PluxExchange {
  static let high = 256 * 1024
  static let low = 64 * 1024

  let id: Int
  let follow: Bool
  let maxRedirects: Int
  var redirects = 0
  var task: URLSessionDataTask?
  var opened: FlutterResult?
  var pending: FlutterResult?
  var chunks: [Data] = []
  var buffered = 0
  var suspended = false
  var finished = false
  var failure: Int?
  var pinMismatch = false
  /// Forgets the request when Dart will never ask for it, since it failed
  /// before it was opened.
  var discard: (() -> Void)?

  init(id: Int, follow: Bool, maxRedirects: Int, opened: @escaping FlutterResult) {
    self.id = id
    self.follow = follow
    self.maxRedirects = maxRedirects
    self.opened = opened
  }

  func cancel() {
    task?.cancel()
    if let p = pending { pending = nil; PluxPinnedHttp.reply(p, nil) }
  }

  func read(_ result: @escaping FlutterResult) {
    if !chunks.isEmpty {
      let chunk = chunks.removeFirst()
      buffered -= chunk.count
      if suspended && buffered < Self.low {
        suspended = false
        task?.resume()
      }
      PluxPinnedHttp.reply(result, FlutterStandardTypedData(bytes: chunk))
    } else if finished {
      PluxPinnedHttp.reply(result, failureValue())
    } else if pending != nil {
      PluxPinnedHttp.reply(result, PluxPinnedHttp.error("busy"))
    } else {
      pending = result
    }
  }

  /// A chunk has arrived or the task has ended: answers a waiting read.
  func serve() {
    guard let result = pending, !chunks.isEmpty || finished else { return }
    pending = nil
    read(result)
  }

  func failureValue() -> Any? {
    if pinMismatch { return FlutterError(code: "PLUX_PIN_MISMATCH", message: nil, details: nil) }
    if let code = failure { return PluxPinnedHttp.error("\(code)") }
    return nil
  }
}

/// A `URLSession` that accepts a server only if its validated chain holds a
/// pinned key.
fileprivate final class PluxPinnedSession: NSObject, URLSessionDataDelegate {
  let key: String
  private let pins: Set<Data>
  private var session: URLSession!
  private var tasks: [Int: PluxExchange] = [:]

  init(key: String, pins: Set<Data>, queue: DispatchQueue) {
    self.key = key
    self.pins = pins
    super.init()
    let config = URLSessionConfiguration.ephemeral
    config.urlCache = nil
    config.requestCachePolicy = .reloadIgnoringLocalCacheData
    config.httpShouldSetCookies = false
    config.httpCookieStorage = nil
    config.tlsMinimumSupportedProtocolVersion = .TLSv12
    let delegateQueue = OperationQueue()
    delegateQueue.maxConcurrentOperationCount = 1
    delegateQueue.underlyingQueue = queue
    session = URLSession(configuration: config, delegate: self, delegateQueue: delegateQueue)
  }

  func start(_ request: URLRequest, _ exchange: PluxExchange) {
    let task = session.dataTask(with: request)
    exchange.task = task
    tasks[task.taskIdentifier] = exchange
    task.resume()
  }

  /// Lets the requests in flight finish, then releases the session.
  func retire() {
    session.finishTasksAndInvalidate()
  }

  // The system validates the chain first; a pin never makes an invalid
  // chain acceptable. Only the task-level method is implemented, which the
  // session calls for server trust when it has no session-level one.
  func urlSession(
    _ session: URLSession, task: URLSessionTask, didReceive challenge: URLAuthenticationChallenge,
    completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
  ) {
    guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
          let trust = challenge.protectionSpace.serverTrust else {
      return completionHandler(.performDefaultHandling, nil)
    }
    var error: CFError?
    guard SecTrustEvaluateWithError(trust, &error) else {
      return completionHandler(.cancelAuthenticationChallenge, nil)
    }
    let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate] ?? []
    let matches = chain.contains { cert in
      guard let spki = PluxDER.subjectPublicKeyInfo(SecCertificateCopyData(cert) as Data) else { return false }
      return pins.contains(Data(SHA256.hash(data: spki)))
    }
    if matches {
      completionHandler(.useCredential, URLCredential(trust: trust))
    } else {
      tasks[task.taskIdentifier]?.pinMismatch = true
      completionHandler(.cancelAuthenticationChallenge, nil)
    }
  }

  func urlSession(
    _ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void
  ) {
    guard let exchange = tasks[task.taskIdentifier], exchange.follow, exchange.redirects < exchange.maxRedirects else {
      // The redirect itself becomes the response.
      return completionHandler(nil)
    }
    exchange.redirects += 1
    completionHandler(request)
  }

  func urlSession(
    _ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse,
    completionHandler: @escaping (URLSession.ResponseDisposition) -> Void
  ) {
    if let exchange = tasks[dataTask.taskIdentifier], let http = response as? HTTPURLResponse,
       let opened = exchange.opened {
      exchange.opened = nil
      var headers: [String: String] = [:]
      for (name, value) in http.allHeaderFields {
        let key = "\(name)".lowercased()
        headers[key] = headers[key].map { "\($0), \(value)" } ?? "\(value)"
      }
      PluxPinnedHttp.reply(opened, ["id": exchange.id, "status": http.statusCode, "headers": headers])
      completionHandler(.allow)
    } else {
      completionHandler(.cancel)
    }
  }

  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
    guard let exchange = tasks[dataTask.taskIdentifier] else { return }
    exchange.chunks.append(data)
    exchange.buffered += data.count
    if exchange.buffered >= PluxExchange.high && !exchange.suspended {
      exchange.suspended = true
      dataTask.suspend()
    }
    exchange.serve()
  }

  func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
    guard let exchange = tasks.removeValue(forKey: task.taskIdentifier) else { return }
    exchange.finished = true
    if let error = error as NSError?, !(exchange.pinMismatch == false && error.code == NSURLErrorCancelled) {
      exchange.failure = error.code
    }
    if let opened = exchange.opened {
      exchange.opened = nil
      PluxPinnedHttp.reply(opened, exchange.failureValue() ?? PluxPinnedHttp.error("closed"))
      exchange.discard?()
    }
    exchange.serve()
  }
}

/// Just enough DER to find the SubjectPublicKeyInfo of an X.509
/// certificate (RFC 5280 §4.1), the bytes RFC 7469 pins hash.
fileprivate enum PluxDER {
  /// The tag, the offset of the content and the offset after the element
  /// at `offset`; nil when it is malformed.
  private static func element(_ der: [UInt8], _ offset: Int) -> (tag: UInt8, start: Int, end: Int)? {
    guard offset >= 0, offset + 2 <= der.count, der[offset] & 0x1F != 0x1F else { return nil }
    var start = offset + 2
    var length = Int(der[offset + 1])
    if length & 0x80 != 0 {
      let n = length & 0x7F
      guard n >= 1, n <= 4, start + n <= der.count else { return nil }
      length = 0
      for i in 0..<n { length = (length << 8) | Int(der[start + i]) }
      start += n
    }
    guard start + length <= der.count else { return nil }
    return (der[offset], start, start + length)
  }

  static func subjectPublicKeyInfo(_ data: Data) -> Data? {
    let der = [UInt8](data)
    // Certificate ::= SEQUENCE { TBSCertificate, ... }
    guard let cert = element(der, 0), cert.tag == 0x30, let tbs = element(der, cert.start), tbs.tag == 0x30 else {
      return nil
    }
    // [0] version OPTIONAL, then serial, signature, issuer, validity, subject.
    var at = tbs.start
    if let v = element(der, at), v.tag == 0xA0 { at = v.end }
    for _ in 0..<5 {
      guard let e = element(der, at) else { return nil }
      at = e.end
    }
    guard let spki = element(der, at), spki.tag == 0x30, spki.end <= tbs.end else { return nil }
    return Data(der[at..<spki.end])
  }
}
