# 0059. Secure UI: secure widgets, secure pages, overlay filtering and inactivity lock

- **Status:** Proposed (P6 plan §2.1 B19 (b), (e))
- **Date:** 2026-10-07
- **Requirements:** `SEC-090`–`SEC-094`, `SCH-012`

## Context and problem

Banking screens need inputs that do not leak through keyboards or the clipboard, pages that
cannot be captured, protection against tapjacking, and re-authentication after idle time.

## Decision drivers

- Native platform behaviour where it exists; no keyboard learning or clipboard leakage.
- Settings from the security configuration (`screenshotBlockingDefault`,
  `inactivityLock`, `inactivityLockTimeout`).
- Sensitive values never reach telemetry, traces or logs.

## Considered options

1. **Core widgets (`SecureTextField`, `SecurePinPad`, `OtpInput`, `BiometricButton`) and a
   page-level `security.secure` mode implemented with platform flags.**
2. Native platform views for every secure input.

## Decision

Chosen option: **1**.

- **`SecureTextField`:** Flutter `TextField` with suggestions, autocorrect, IME
  personalised learning, clipboard and context menu disabled, obscured by default.
- **`SecurePinPad`:** a Plux keypad (no system keyboard), optional randomised layout per
  display, no press animation, haptics only.
- **`OtpInput`:** configurable length and alphabet, manual entry, paste with a format
  check, a completion event; Android SMS User Consent API, iOS `oneTimeCode` content type.
- **`BiometricButton`:** runs `biometricAuth` (ADR-0051's `biometrics` capability).
- **Secure pages:** Android `FLAG_SECURE` while a secure page is visible; iOS a covering
  view when capture is detected and in the app switcher; Android `filterTouchesWhenObscured`
  on the Flutter view while secure (`SEC-093`); the inactivity lock asks for biometrics or
  the device credential after the configured idle time.
- **Sensitive fields:** form fields with `sensitive: true` are masked unless the user
  unmasks them, and excluded from telemetry, traces and logs (`SCH-012`, `SEC-092`).

## Consequences

- **Positive:** small, testable widgets; behaviour driven by configuration.
- **Negative:** iOS cannot block screenshots, only cover recording and the switcher.
- **Follow-up:** S10; real-device evidence in S11.
