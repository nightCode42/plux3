// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// A native Android app that embeds the Plux module (HST-033): the module's
// :flutter project and its plugins come from include_flutter.groovy, which
// `flutter pub get` in ../plux_module writes.

pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

plugins {
    id("com.android.application") version "9.1.0" apply false
    id("com.android.library") version "9.1.0" apply false
    id("org.jetbrains.kotlin.android") version "2.4.0" apply false
}

rootProject.name = "plux_add_to_app_host"
include(":app")
apply(from = File(settingsDir.parentFile, "plux_module/.android/include_flutter.groovy"))
