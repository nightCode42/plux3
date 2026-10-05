// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The services the device actions find through `StepContext.service`
/// (RT-060): the core's own, and the implementations of the packages the
/// host registered.
library;

import 'package:plux_flutter/src/device/platform.dart';
import 'package:plux_flutter/src/device/types.dart';

/// Builds the device services of a runtime: the platform calls, the picked
/// files and everything [packages] provide. A device action whose package
/// is not among them fails with `PLX-5401`.
Map<Type, Object> deviceServices({
  required List<PluxDevicePackage> packages,
  required Future<bool> Function(Uri link) openLink,
  UrlOpener urls = const PlatformUrlOpener(),
  DevicePlatform platform = const ChannelDevicePlatform(),
  PickedFiles? files,
}) => {
  UrlOpener: urls,
  DeepLinkOpener: _Links(openLink),
  DevicePlatform: platform,
  PickedFiles: files ?? PickedFiles(),
  for (final p in packages) ...p.services,
};

final class _Links implements DeepLinkOpener {
  const _Links(this._open);

  final Future<bool> Function(Uri link) _open;

  @override
  Future<bool> open(Uri link) => _open(link);
}
