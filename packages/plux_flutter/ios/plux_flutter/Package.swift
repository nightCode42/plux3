// swift-tools-version: 5.9
// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import PackageDescription

let package = Package(
    name: "plux_flutter",
    platforms: [
        .iOS("15.0")
    ],
    products: [
        .library(name: "plux-flutter", targets: ["plux_flutter"])
    ],
    dependencies: [
        .package(name: "FlutterFramework", path: "../FlutterFramework")
    ],
    targets: [
        .target(
            name: "plux_flutter",
            dependencies: [
                .product(name: "FlutterFramework", package: "FlutterFramework")
            ]
        )
    ]
)
