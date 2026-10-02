// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import UIKit

/// The host's native home screen. Its buttons open Plux pages by route:
/// full screen, or below a native header.
final class HomeViewController: UIViewController {
  override func viewDidLoad() {
    super.viewDidLoad()
    view.backgroundColor = .systemBackground
    let heading = UILabel()
    heading.text = "Native home"
    heading.font = .preferredFont(forTextStyle: .title1)
    let stack = UIStackView(arrangedSubviews: [
      heading,
      button("Open welcome") { [weak self] in
        self?.present(PluxHost.shared.page("welcome"), animated: true)
      },
      button("Open host link") { [weak self] in
        self?.present(EmbeddedViewController(route: "host-link"), animated: true)
      },
    ])
    stack.axis = .vertical
    stack.alignment = .leading
    stack.spacing = 16
    stack.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(stack)
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.leadingAnchor, constant: 24),
      stack.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: 24),
    ])
  }

  private func button(_ title: String, action: @escaping () -> Void) -> UIButton {
    UIButton(type: .system, primaryAction: UIAction(title: title) { _ in action() })
  }
}
