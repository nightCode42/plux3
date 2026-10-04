// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import UIKit

/// The host's native settings screen, which a plugin page opens through
/// the module's native route `host-settings`. When it closes, the page's
/// navigation completes.
final class SettingsViewController: UIViewController {
  private let onClose: () -> Void

  init(onClose: @escaping () -> Void) {
    self.onClose = onClose
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
    let heading = UILabel()
    heading.text = "Host settings"
    heading.font = .preferredFont(forTextStyle: .title1)
    let close = UIButton(
      type: .system,
      primaryAction: UIAction(title: "Close") { [weak self] _ in self?.dismiss(animated: true) })
    let stack = UIStackView(arrangedSubviews: [heading, close])
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

  override func viewDidDisappear(_ animated: Bool) {
    super.viewDidDisappear(animated)
    if isBeingDismissed { onClose() }
  }
}
