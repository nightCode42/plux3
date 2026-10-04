# Size job

What `plux_flutter` adds to a host app (`RT-061`, `NFR-009`,
[ADR-0036](../../docs/adr/0036-size-budgets-per-build.md)), measured against a blank
Flutter app: at most 4 MiB to what a device downloads from the App Bundle for each ABI, at
most 10 MiB to the release APK of each ABI, which stores the Dart code uncompressed, and at
most 3 MiB to the thinned iOS IPA.

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

`tools/cmd/sizegate` measures the APKs as they are, what the App Bundle delivers to a
device of each ABI (the base module with that ABI's native libraries, each file compressed
at the highest level; language and density splits are not applied, so it is an upper
bound), and the iOS apps as the ZIP archive an IPA is (`Payload/Runner.app`, highest
compression; an unsigned device build for arm64 is what App Store thinning delivers to one
device). Each of the seven builds fails the job when the runtime adds more than its budget,
or more than 10% over the overhead committed in [baseline.json](baseline.json) (`QA-007`).
After an intended change, `test/size/size.sh android -update` (or `ios`) rewrites the
committed overheads. Reports are written to `build/size`: the six Android builds side by
side in `android-compare.md`, each build's table and the files that grew most.

Each measurement round — the figures, where the bytes go, and what was decided — is
recorded in the [size journey](../../docs/benchmarks/size.md).
