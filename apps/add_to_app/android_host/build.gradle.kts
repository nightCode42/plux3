// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Flutter's Gradle plugin on the root classpath, applied nowhere here. When
// the module's :flutter project applies it, it gives every plugin project its
// `flutter` extension (compileSdk, minSdk and the rest). A plugin whose Kotlin
// build reads flutter.compileSdkVersion, such as url_launcher_android, gets a
// typed accessor for it only when the extension's class is on its build
// script's classpath: inside a Flutter app the plugin loader puts it there; a
// native host that includes a module from source has to, here.
plugins {
    id("dev.flutter.flutter-gradle-plugin") apply false
}

allprojects {
    repositories {
        google()
        mavenCentral()
    }
}
