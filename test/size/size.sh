#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Plux contributors
# SPDX-License-Identifier: Apache-2.0
#
# The size job (RT-061, NFR-009), run by `make size-android` and
# `make size-ios`. See test/size/README.md.
#
#   size.sh android [-update]   release APK for arm64, and the App Bundle
#                               per ABI (needs the Android SDK)
#   size.sh ios [-update]       release app for arm64, unsigned (needs Xcode)
#
# Builds the blank app and the same app with plux_flutter, then
# tools/cmd/sizegate fails when the runtime adds more than 3 MiB, or more
# than 10% over the overhead committed in test/size/baseline.json;
# -update rewrites that overhead instead. The Markdown report is written
# to $SIZE_OUT/<platform>.md (default build/size); for Android, Flutter's
# code-size analysis of the plux app to android-code-size.txt beside it,
# and the comparison of what plux_flutter adds to the release APK of each
# ABI and to what the App Bundle delivers to a device of that ABI,
# compressed as Play downloads it, to android-compare.md (only the arm64
# APK is gated). Each target's table is $SIZE_OUT/<target>.md.
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
	mkdir -p "$out"
	for app in blank plux; do
		platform "$app" android
	done
	# Where the plux app's Dart code goes, by package: an analysis build,
	# made first because it writes the same APK the gate measures.
	(cd "$here/plux" && flutter build apk --release --target-platform android-arm64 \
		--analyze-size --code-size-directory "$out/code-size") | tee "$out/android-code-size.txt"
	# The APK of each ABI, and the App Bundle for the three of them.
	for app in blank plux; do
		(cd "$here/$app" && flutter build apk --release --split-per-abi \
			--target-platform android-arm,android-arm64,android-x64)
		(cd "$here/$app" && flutter build appbundle --release)
	done
	aab=build/app/outputs/bundle/release/app-release.aab
	# report <target> <build>: the target's table, written to $out/<target>.md
	# and not gating; the gate below decides.
	report() {
		(cd "$root/tools" && go run ./cmd/sizegate -target "$1" -report \
			-blank "$here/blank/$2" -plux "$here/plux/$2" \
			-baseline "$here/baseline.json") >"$out/$1.md"
	}
	for abi in arm64-v8a armeabi-v7a x86_64; do
		report "android-$abi-aab" "$aab"
		apk=android-$abi-apk
		if [ "$abi" = arm64-v8a ]; then apk=android-arm64-apk; fi
		report "$apk" "build/app/outputs/flutter-apk/app-$abi-release.apk"
	done
	# What each ABI adds, side by side: the APK as a file, and what Play
	# downloads from the App Bundle.
	row() { # <label> <report>
		awk -F'|' -v label="$1" '/^\| blank app/ {b = $4} /^\| with plux_flutter/ {w = $4}
			/^\| added by plux_flutter/ {a = $4} END {printf "| %s |%s|%s|%s|\n", label, b, w, a}' "$2"
	}
	{
		printf '| ABI | Build | Blank, MiB | With plux_flutter, MiB | Added, MiB |\n|---|---|---:|---:|---:|\n'
		for abi in arm64-v8a armeabi-v7a x86_64; do
			apk=android-$abi-apk
			if [ "$abi" = arm64-v8a ]; then apk=android-arm64-apk; fi
			row "$abi | APK file" "$out/$apk.md"
			row "$abi | App Bundle download" "$out/android-$abi-aab.md"
		done
	} | tee "$out/android-compare.md"
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
