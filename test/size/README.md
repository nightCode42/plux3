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
make size-android   # release APK, --target-platform android-arm64 --split-per-abi (Android SDK)
make size-ios       # release app for arm64, --no-codesign, archived as an IPA (Xcode)
```

`tools/cmd/sizegate` measures the APKs as they are and the iOS apps as the ZIP archive an
IPA is (`Payload/Runner.app`, highest compression; an unsigned device build for arm64 is
what App Store thinning delivers to one device). It fails when the runtime adds more than
3 MiB, or more than 10% over the overhead committed in [baseline.json](baseline.json)
(`QA-007`). After an intended change, `test/size/size.sh android -update` (or `ios`)
rewrites the committed overhead; the report is written to `build/size/<platform>.md`.
