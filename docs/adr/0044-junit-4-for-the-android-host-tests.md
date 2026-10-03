# 0044. JUnit 4 is accepted for the Android add-to-app host's tests only

- **Status:** Accepted (maintainer, 2026-10-03, at P4's close)
- **Date:** 2026-10-03
- **Requirements:** `HST-033`, `CI-007`

## Context and problem

`HST-033` asks that the runtime work inside a Flutter module embedded in native apps. P4
proves it with a Kotlin host in `apps/add_to_app/android_host`, whose UiAutomator tests
run on the emulator ([plan p4](../plans/p4.md) §5.10). Android's instrumentation stack,
`androidx.test:runner` and `androidx.test.ext:junit` (Apache-2.0), has JUnit 4 as its test
API, so they bring `junit:junit` 4.13.2, licensed EPL-1.0, and `org.hamcrest:hamcrest-core`
1.3 (BSD-3-Clause).

EPL-1.0 is a weak copyleft licence. [dependencies.md](../engineering/dependencies.md) §1
point 3 accepts copyleft only where an ADR does.

## Decision drivers

- The host's tests must use the platform's standard UI test stack, which CI's emulators run
  and Firebase Test Lab would run unchanged later.
- Nothing Plux distributes may carry a copyleft obligation.

## Considered options

1. Accept JUnit 4 for the host's `androidTest` configuration only.
2. Drive the host from outside the device (`adb shell input`, `uiautomator dump`), with no
   instrumentation test on the device.

## Decision

Chosen option: **1**. JUnit 4 is a dependency of the `androidTest` source set of one test
fixture app: it runs on the emulator and is never compiled into the host's app, a Plux
package or anything else distributed. EPL-1.0's obligations attach to distributing the
library, which never happens. Option 2 would replace a standard, maintained stack with
shell scripts that read the screen through text dumps.

## Consequences

- **Positive:** the host's tests are ordinary Android instrumentation tests.
- **Negative:** the allowlist holds one weak copyleft library, scoped to test code.
- **Follow-up:** the acceptance covers `androidTest` only; JUnit 4 in any shipped
  configuration, or another EPL dependency, needs its own ADR.
