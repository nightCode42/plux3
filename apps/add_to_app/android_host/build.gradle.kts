// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

plugins {
    id("dev.flutter.flutter-gradle-plugin") apply false
}

allprojects {
    repositories {
        google()
        mavenCentral()
    }
}

// Inside a Flutter app, Flutter's Gradle plugin gives every plugin project
// its `flutter` extension (compileSdk, minSdk and the rest); a native host
// that includes a module from source gets no such step, so a plugin whose
// build reads flutter.compileSdkVersion, such as url_launcher_android, does
// not configure. Each plugin project gets the extension here, before it is
// evaluated, as Flutter's app plugin loader would give it.
subprojects {
    if (name != "app" && name != "flutter" && extensions.findByName("flutter") == null) {
        extensions.create("flutter", com.flutter.gradle.FlutterExtension::class.java)
    }
}
