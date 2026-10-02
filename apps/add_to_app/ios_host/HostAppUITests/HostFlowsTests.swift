// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import XCTest

/// The add-to-app flows (HST-033) on a simulator, against the server
/// TestAddToAppAgainstTheServer starts. They find what is on screen by its
/// accessibility label, which Flutter sets from a widget's semantics. The
/// driver runs each test in an xcodebuild run of its own, passing the
/// runtime's settings as TEST_RUNNER_PLUX_* variables, which reach this
/// runner without the prefix and the app through its launch environment.
final class HostFlowsTests: XCTestCase {
  override func setUp() {
    continueAfterFailure = false
  }

  /// A native screen opens a Plux page, rendered from the baseline with
  /// the server out of reach.
  func testRendersOfflineFromTheBaseline() {
    let app = launch()
    tap(app, "Open welcome")
    waitFor(app, "Welcome to Plux", exact: true)
    tap(app, "Back", exact: true)
    waitFor(app, "Open welcome")
  }

  /// Sync applies the newer release once no Plux page is open, and a
  /// plugin page shown below a native header opens the host's native
  /// screen.
  func testAppliesUpdatesAndOpensNativeScreens() {
    let app = launch()
    let deadline = Date().addingTimeInterval(60)
    while true {
      tap(app, "Open welcome")
      waitFor(app, "Welcome to Plux")
      if element(app, "Welcome to Plux, again", exact: true).exists { break }
      XCTAssertLessThan(Date(), deadline, "the update never applied")
      tap(app, "Back", exact: true)
      waitFor(app, "Open welcome")
      Thread.sleep(forTimeInterval: 0.5)
    }
    tap(app, "Back", exact: true)
    waitFor(app, "Open welcome")

    tap(app, "Open host link")
    waitFor(app, "Native header", exact: true)
    tap(app, "Open host settings")
    waitFor(app, "Host settings", exact: true)
    tap(app, "Close", exact: true)
    waitFor(app, "Open host settings")
    tap(app, "Back", exact: true)
    waitFor(app, "Open welcome")
  }

  // Launches the app with the runtime's settings.
  private func launch() -> XCUIApplication {
    let app = XCUIApplication()
    let env = ProcessInfo.processInfo.environment
    for name in ["PLUX_ENDPOINT", "PLUX_APP_ID", "PLUX_ENVIRONMENT", "PLUX_HOST_BUILD", "PLUX_ROOT_KEYS"] {
      if let value = env[name] { app.launchEnvironment[name] = value }
    }
    app.launch()
    waitFor(app, "Open welcome")
    return app
  }

  private func element(_ app: XCUIApplication, _ label: String, exact: Bool) -> XCUIElement {
    let predicate =
      exact ? NSPredicate(format: "label == %@", label) : NSPredicate(format: "label CONTAINS %@", label)
    return app.descendants(matching: .any).matching(predicate).firstMatch
  }

  // Waits up to a minute: the first page starts the engine and the runtime.
  @discardableResult
  private func waitFor(_ app: XCUIApplication, _ label: String, exact: Bool = false) -> XCUIElement {
    let found = element(app, label, exact: exact)
    XCTAssertTrue(found.waitForExistence(timeout: 60), "nothing on screen reads \"\(label)\"")
    return found
  }

  private func tap(_ app: XCUIApplication, _ label: String, exact: Bool = false) {
    waitFor(app, label, exact: exact).tap()
  }
}
