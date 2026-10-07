// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Plux Express's end-to-end flows (DX-004, QA-006) against a running Plux
/// server that has the `plux_express` fixture project
/// (schema/testdata/documents/plux_express) promoted to the configured
/// environment, and the reference backend (test/refapi) its environments
/// name. The same flows run on emulators and simulators
/// (`integration_test/app_test.dart`) and on the development machine
/// against a server the Go driver starts (`test/e2e_test.dart`,
/// `make e2e-starter`).
library;

import 'dart:async';
import 'dart:convert';
import 'dart:isolate';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_express/express.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The reference backend's base URL, as the Go driver passes it.
const String referenceApiUrl = String.fromEnvironment('PLUX_REFAPI_URL');

/// The texts on screen, for the message of a wait that timed out.
List<String> _texts() => [
  for (final e in find.byType(Text).evaluate()) ?(e.widget as Text).data,
];

/// Waits in real time until [finder] finds at least [count] widgets,
/// pumping frames; the runtime's isolates answer outside the test's fake
/// clock.
Future<void> pumpUntil(
  WidgetTester tester,
  Finder finder, {
  int count = 1,
  Duration timeout = const Duration(seconds: 60),
  List<Object> reported = const [],
}) async {
  final end = DateTime.now().add(timeout);
  while (finder.evaluate().length < count) {
    if (DateTime.now().isAfter(end)) {
      throw TimeoutException(
        'waiting for $count of $finder; on screen: ${_texts()}; '
        'the runtime reported: $reported',
        timeout,
      );
    }
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 50)),
    );
    await tester.pump(const Duration(milliseconds: 50));
  }
}

/// Taps [finder] once the screen has settled: a page still sliding in
/// moves its widgets, and a tap aimed at a moving widget can miss it.
Future<void> tapSettled(WidgetTester tester, Finder finder) async {
  await tester.pumpAndSettle();
  await tester.tap(finder);
}

/// The deliveries the reference backend applied to [job], read the way the
/// app reads it: over HTTPS, trusting only the backend's CA. The request
/// leaves from another isolate, since `flutter test` answers every
/// `HttpClient` of the test's own with an empty 400.
Future<List<Object?>> deliveriesOf(WidgetTester tester, String job) async {
  final url = '$referenceApiUrl/express/v1/courier/jobs/$job/deliveries';
  final items = await tester.runAsync(
    () => Isolate.run(() => _fetchItems(referenceCa, url)),
  );
  return items!;
}

Future<List<Object?>> _fetchItems(String base64Pem, String url) async {
  final client = trustingHttpClient(base64Pem);
  try {
    final request = await client.getUrl(Uri.parse(url));
    final response = await request.close();
    final body = await utf8.decodeStream(response);
    if (response.statusCode != 200) {
      throw StateError('GET $url answered ${response.statusCode}: $body');
    }
    return (jsonDecode(body) as Map<String, Object?>)['items']!
        as List<Object?>;
  } finally {
    client.close(force: true);
  }
}

/// Registers the flows. [config] is the app's configuration; the flows are
/// skipped without one. [storage] gives the release store's directory for
/// each launch, null for the platform's, as a device does. [initialize]
/// starts the runtime: `Plux.initialize` on a device, with the platform's
/// key store; the host run replaces the key store, which has no platform
/// side there. The flows start without the app's embedded baseline: their
/// first launch syncs from the server.
void expressFlows({
  required ExpressConfig? Function() config,
  Future<String?> Function()? storage,
  Future<PluxStartup> Function(PluxConfig) initialize = Plux.initialize,
}) {
  final problems = <PluxException>[];

  /// [pumpUntil], naming what the runtime reported if it times out.
  Future<void> waitFor(WidgetTester tester, Finder finder, {int count = 1}) =>
      pumpUntil(tester, finder, count: count, reported: problems);
  late ExpressHost host;

  Future<void> launch(WidgetTester tester) async {
    // A flow that failed left its runtime running.
    await tester.runAsync(Plux.dispose);
    final c = config()!;
    final dir = await tester.runAsync(() async => storage?.call());
    host = ExpressHost();
    final startup = await tester.runAsync(
      () => initialize(
        c.toPluxConfig(
          host: host,
          storageDirectory: dir,
          onError: (e, _) => problems.add(e),
          baseline: null,
        ),
      ),
    );
    expect(startup!.ready, isTrue, reason: '${startup.error}');
    await tester.pumpWidget(
      ExpressApp(config: c, startup: startup, host: host),
    );
  }

  Future<void> stop(WidgetTester tester) async {
    await tester.pumpWidget(const SizedBox());
    await tester.runAsync(Plux.dispose);
  }

  final skip = config() == null
      ? 'no server configured: set PLUX_APP_ID (make e2e-starter)'
      : null;

  testWidgets(
    'the catalogue loads its first page and pages on as the list is '
    'scrolled to its end [DX-004] [DAT-001] [DAT-011]',
    (tester) async {
      problems.clear();
      await launch(tester);
      await waitFor(tester, find.text('Loaded 10 products'));
      expect(find.text('Product 01'), findsOneWidget);

      final list = find
          .descendant(
            of: find.byType(ListView),
            matching: find.byType(Scrollable),
          )
          .first;
      await tester.scrollUntilVisible(
        find.text('Product 10'),
        200,
        scrollable: list,
      );
      await waitFor(tester, find.text('Loaded 20 products'));
      await tester.scrollUntilVisible(
        find.text('Product 20'),
        200,
        scrollable: list,
      );
      await waitFor(tester, find.text('Loaded 30 products'));
      expect(problems, isEmpty, reason: problems.join('\n'));
      await stop(tester);
    },
    skip: skip != null,
    timeout: const Timeout(Duration(minutes: 3)),
  );

  testWidgets(
    'a product goes into the persisted cart, which keeps it across pages, '
    'and the order placed from it is tracked over a WebSocket until it is '
    'delivered [DX-004] [STA-003] [DAT-012]',
    (tester) async {
      problems.clear();
      await launch(tester);
      await waitFor(tester, find.text('Product 01'));
      expect(find.text('Cart (0)'), findsOneWidget);

      await tapSettled(tester, find.text('Product 01'));
      await waitFor(tester, find.text('In cart: 0'));
      // The product page's own price: the catalogue's row under the page
      // shows the same text, and a tap before the product loaded would put
      // its fallbacks, the id and no price, into the cart.
      await waitFor(
        tester,
        find.descendant(
          of: find.byWidgetPredicate(
            (w) => w is Semantics && w.properties.identifier == 'product-price',
          ),
          matching: find.text('1.99 EUR'),
        ),
      );
      await tapSettled(tester, find.text('Add to cart'));
      await waitFor(tester, find.text('In cart: 1'));

      // The cart outlives the page that filled it.
      await tester.pageBack();
      await waitFor(tester, find.text('Cart (1)'));
      await tapSettled(tester, find.text('Cart (1)'));
      await waitFor(tester, find.text('Total: 1.99 EUR'));
      // The cart's line, once the page has slid over the catalogue.
      await tester.pumpAndSettle();
      expect(find.text('Product 01'), findsOneWidget);
      await tester.pageBack();
      await waitFor(tester, find.text('Cart (1)'));
      await tapSettled(tester, find.text('Cart (1)'));
      await waitFor(tester, find.text('Total: 1.99 EUR'));

      // Placing the order opens its tracking page: the server's status
      // steps arrive over the WebSocket until the order is delivered.
      await tapSettled(tester, find.text('Place order'));
      await waitFor(tester, find.text('Order ord-001'));
      await waitFor(tester, find.text('Status: delivered'));
      expect(find.text('ETA: 0 min'), findsOneWidget);
      expect(problems, isEmpty, reason: problems.join('\n'));
      await stop(tester);
    },
    skip: skip != null,
    timeout: const Timeout(Duration(minutes: 3)),
  );

  testWidgets(
    'a delivery confirmed while the host reports the network gone is '
    'queued, and replays exactly once when it is back [DX-004] [DAT-020]',
    (tester) async {
      problems.clear();
      await launch(tester);
      await waitFor(tester, find.text('Product 01'));

      host.setOnline(online: false);
      await tapSettled(tester, find.text('Courier'));
      await waitFor(tester, find.text('Confirm delivery'), count: 3);
      await tapSettled(tester, find.text('Confirm delivery').first);
      await waitFor(tester, find.text('Queued job-001'));
      // Nothing reached the backend while the host said it was offline.
      expect(await deliveriesOf(tester, 'job-001'), isEmpty);

      host.setOnline(online: true);
      await waitFor(tester, find.text('Synced'));
      final applied = await deliveriesOf(tester, 'job-001');
      expect(applied, hasLength(1));
      expect((applied.single! as Map<String, Object?>)['jobId'], 'job-001');

      // No later replay applies it again.
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(seconds: 2)),
      );
      await tester.pump();
      expect(await deliveriesOf(tester, 'job-001'), hasLength(1));
      expect(problems, isEmpty, reason: problems.join('\n'));
      await stop(tester);
    },
    skip: skip != null,
    timeout: const Timeout(Duration(minutes: 3)),
  );
}
