# Size job

What `plux_flutter` adds to a host app's download (`RT-061`, `NFR-009`): at most 3 MiB to
a release APK for arm64 and to a thinned iOS IPA, measured against a blank Flutter app.

- [blank](blank) is a `MaterialApp` with a scaffold and a text.
- [plux](plux) is the same app with the runtime: it calls `Plux.initialize` and shows a
  `PluxView`, so everything a host app ships — sync, verification, the renderer and
  every widget builder — is built in.

Both are pub workspace members, so they resolve with the repository's lockfile. Their
platform folders are made by `flutter create` when the job runs and are not committed.

```bash
make size-android   # release APK, --target-platform android-arm64 --split-per-abi,
                    # and the release App Bundle per ABI (Android SDK)
make size-ios       # release app for arm64, --no-codesign, archived as an IPA (Xcode)
```

`tools/cmd/sizegate` measures the APKs as they are and the iOS apps as the ZIP archive an
IPA is (`Payload/Runner.app`, highest compression; an unsigned device build for arm64 is
what App Store thinning delivers to one device). It fails when the runtime adds more than
3 MiB, or more than 10% over the overhead committed in [baseline.json](baseline.json)
(`QA-007`). After an intended change, `test/size/size.sh android -update` (or `ios`)
rewrites the committed overhead; the report is written to `build/size/<platform>.md`.

The Android job also reports, without gating, what the release App Bundle delivers to a
device of each ABI (`arm64-v8a`, `armeabi-v7a`, `x86_64`), compressed as Play downloads
it: the base module's files with only that ABI's native libraries, each compressed at the
highest level into one ZIP archive (`build/size/android-aab.md`). Language and density
splits are not applied, so the figure is an upper bound of Play's download size. It is
comparable with the iOS figure, which is compressed too; RT-061 names the APK, so the
APK stays the gate until the maintainer decides otherwise (work log, open decisions).
