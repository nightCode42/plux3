// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package codegen

import (
	"fmt"
	"strings"
)

// RootKey is a signing root public key a host app embeds (SEC-051).
type RootKey struct {
	KeyID, Algorithm, Role string
	PublicKey              []byte
}

// OptionsSpec is what a host app's generated configuration holds: the
// Plux app, server and environment, and the environment's root keys.
type OptionsSpec struct {
	AppKey, AppID, Endpoint, Environment string
	// Channel is the environment's channel; empty for production.
	Channel string
	Keys    []RootKey
}

func channel(c string) string {
	if c == "" {
		return "production"
	}
	return c
}

// Options is lib/plux/plux_options.g.dart, the configuration `plux init`
// and `plux create` write into a host app (HST-032, GEN-001).
func Options(o OptionsSpec) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, `// Written by plux for the app %q in its %q environment. Do not edit:
// run plux init again to update it, for example after the keys rotate.
//
// ignore_for_file: type=lint

import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// Where this app's Plux content comes from, and the root keys its
/// releases are verified with, embedded at build time (SEC-051, HST-032).
abstract final class PluxOptions {
  /// The Plux app.
  static const appId = %s;

  /// The Plux server.
  static const endpoint = %s;

  /// The environment the app syncs from.
  static const environment = %s;

  /// The environment's channel.
  static const channel = %s;

  /// The environment's root public keys.
  static final rootKeys = <PluxPublicKey>[
`, o.AppKey, o.Environment, DartString(o.AppID), DartString(o.Endpoint), DartString(o.Environment), DartString(channel(o.Channel)))
	for _, k := range o.Keys {
		hex := make([]string, 0, len(k.PublicKey))
		for _, by := range k.PublicKey {
			hex = append(hex, fmt.Sprintf("0x%02x", by))
		}
		fmt.Fprintf(&b, "    PluxPublicKey(\n      keyId: %s,\n      algorithm: %s,\n      role: %s,\n      publicKey: Uint8List.fromList([%s]),\n    ),\n",
			DartString(k.KeyID), DartString(k.Algorithm), DartString(k.Role), strings.Join(hex, ", "))
	}
	b.WriteString(`  ];

  /// The runtime's configuration with these values; [navigatorKey] is the
  /// app's root navigator, where deep links open their pages. Pass other
  /// options to PluxConfig yourself where you need them.
  static PluxConfig config({GlobalKey<NavigatorState>? navigatorKey}) =>
      PluxConfig(
        appId: appId,
        endpoint: Uri.parse(endpoint),
        environment: environment,
        channel: channel,
        rootKeys: rootKeys,
        navigatorKey: navigatorKey,
      );
}
`)
	return []byte(b.String())
}
