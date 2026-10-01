# Screen-reader test script: Layer 2 components (P3)

`WGT-020` asks every Layer 2 component for a screen-reader test script. This one covers the P3 components: `EmptyState`, `ErrorState`, `OfflineBanner` and `SkeletonLoader` (`packages/plux_flutter/lib/src/render/builders/layer2.dart`).

The automated half runs in every CI run: `gallery_test.dart` › *Layer 2 components read as their screen-reader script says* checks the semantics each step below relies on (headers, the retry button, live regions, hidden placeholders). The manual half is run on a device before each release with TalkBack (Android) and VoiceOver (iOS), on the widget gallery's `layer2` page (`schema/testdata/documents/widgets/plugins/gallery/pages/layer2.page.json`), in the example host app from R8.

## Setup

1. Install the example host app with the gallery release; open the `layer2` page.
2. Turn on TalkBack or VoiceOver. Set the system text size to the largest setting for step 6.
3. Turn on airplane mode and pull to refresh (or restart the app), so the next sync reaches no server: the offline banner shows only then, and the page's second banner, with `visible` false, never does.

## Steps and expected announcements

| # | Action | Expected |
|---|---|---|
| 1 | Swipe to the first element after the app bar. | "You are offline". The banner is one element; when it appears while the page is open, it is announced without moving focus (live region). |
| 2 | Swipe on. | The skeleton placeholders are skipped: they carry no meaning. |
| 3 | Swipe on. | "Nothing here, heading", then "Items you add appear here.", then "add item, button". The icon is not announced (decorative). |
| 4 | Swipe on. | "Could not load, heading", then "Check your connection and try again.", then "Retry, button". When the error state appears while the page is open, its title and message are announced (live region). |
| 5 | Double-tap "Retry". | The button activates (in P3 its actions are reported as `PLX-4010`; from P5 they run). |
| 6 | Repeat steps 1–4 at the largest text size, then in a right-to-left language. | The same announcements, in the same order; nothing is clipped. |
| 7 | Use the headings rotor (VoiceOver) or heading navigation (TalkBack). | It stops at "Nothing here" and "Could not load" only. |

Record the platform, OS version, screen reader and result of each step in the release checklist; a step that fails blocks the release (`QA-011` from P8 automates the reference-app passes).
