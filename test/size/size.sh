#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The size job (RT-061, NFR-009), run by `make size-android` and
# `make size-ios`. See test/size/README.md.
#
#   size.sh android [-update]   release APK for arm64 (needs the Android SDK)
#   size.sh ios [-update]       release app for arm64, unsigned (needs Xcode)
#
# Builds the blank app and the same app with plux_flutter, then
# tools/cmd/sizegate fails when the runtime adds more than 3 MiB, or more
# than 10% over the overhead committed in test/size/baseline.json;
# -update rewrites that overhead instead. The Markdown report is written
# to $SIZE_OUT/<platform>.md (default build/size).
set -euo pipefail

root=$(git rev-parse --show-toplevel)
here=$root/test/size
out=${SIZE_OUT:-$root/build/size}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# platform <app> <platform>: adds the project `flutter create` makes for
# the platform, which is not committed, unless the app has one.
platform() {
	[ -d "$here/$1/$2" ] && return
	(cd "$work" && flutter create --platforms="$2" --project-name "plux_size_$1" \
		--org dev.plux --no-pub "$1" >/dev/null)
	cp -r "$work/$1/$2" "$here/$1/$2"
	if [ "$2" = android ]; then
		# The Android Gradle plugin 9 refuses two libraries that declare
		# one namespace, and Play Services Cronet (cronet_http, SYN-010)
		# brings cronet-api and cronet-shared, both org.chromium.net. A
		# host app sets this until they differ; the blank app too, so both
		# builds are made the same way.
		printf '\n# Play Services Cronet: cronet-api and cronet-shared share a namespace.\nandroid.uniquePackageNames=false\n' \
			>>"$here/$1/android/gradle.properties"
	fi
}

target=${1:-}
case "$target" in
android)
	for app in blank plux; do
		platform "$app" android
		(cd "$here/$app" && flutter build apk --release --target-platform android-arm64 --split-per-abi)
	done
	build=build/app/outputs/flutter-apk/app-arm64-v8a-release.apk
	gate=android-arm64-apk
	;;
ios)
	for app in blank plux; do
		platform "$app" ios
		(cd "$here/$app" && flutter build ios --release --no-codesign)
	done
	build=build/ios/iphoneos/Runner.app
	gate=ios-arm64-ipa
	;;
*)
	echo "usage: size.sh android|ios [-update]" >&2
	exit 2
	;;
esac

mkdir -p "$out"
(cd "$root/tools" && go run ./cmd/sizegate -target "$gate" \
	-blank "$here/blank/$build" -plux "$here/plux/$build" \
	-baseline "$here/baseline.json" ${2:+"$2"}) | tee "$out/$target.md"
