// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import UIKit

/// A native screen that shows a Plux page below its native header, with
/// the module's FlutterViewController as a child. When the page pops, the
/// module closes this screen.
final class EmbeddedViewController: UIViewController {
  private let route: String

  init(route: String) {
    self.route = route
    super.init(nibName: nil, bundle: nil)
    modalPresentationStyle = .fullScreen
  }

  /// Not supported: the screen is created in code.
  @available(*, unavailable)
  required init?(coder: NSCoder) {
    return nil
  }

  override func viewDidLoad() {
    super.viewDidLoad()
    view.backgroundColor = .systemBackground
    let header = UILabel()
    header.text = "Native header"
    header.font = .preferredFont(forTextStyle: .headline)
    header.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(header)
    let flutter = PluxHost.shared.controller(route)
    addChild(flutter)
    flutter.view.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(flutter.view)
    NSLayoutConstraint.activate([
      header.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: 16),
      header.leadingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.leadingAnchor, constant: 16),
      flutter.view.topAnchor.constraint(equalTo: header.bottomAnchor, constant: 16),
      flutter.view.leadingAnchor.constraint(equalTo: view.leadingAnchor),
      flutter.view.trailingAnchor.constraint(equalTo: view.trailingAnchor),
      flutter.view.bottomAnchor.constraint(equalTo: view.bottomAnchor),
    ])
    flutter.didMove(toParent: self)
  }
}
