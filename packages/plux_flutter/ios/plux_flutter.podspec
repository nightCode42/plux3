# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The iOS side of plux_flutter's platform channel (ADR-0029): storage
# directory and Keychain-held secrets. No third-party dependency.
Pod::Spec.new do |s|
  s.name             = 'plux_flutter'
  s.version          = '0.1.0'
  s.summary          = 'The Plux runtime for Flutter.'
  s.description      = 'Platform services of the Plux runtime: storage and Keychain secrets.'
  s.homepage         = 'https://github.com/nightCode42/plux3'
  s.license          = { :type => 'Apache-2.0' }
  s.author           = { 'Plux contributors' => 'https://github.com/nightCode42/plux3' }
  s.source           = { :path => '.' }
  s.source_files     = 'plux_flutter/Sources/plux_flutter/**/*.swift'
  s.dependency 'Flutter'
  # RT-002: iOS 15 and later.
  s.platform = :ios, '15.0'
  s.pod_target_xcconfig = { 'DEFINES_MODULE' => 'YES', 'EXCLUDED_ARCHS[sdk=iphonesimulator*]' => 'i386' }
  s.swift_version = '5.0'
end
