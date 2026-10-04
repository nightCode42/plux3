// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nativeCode is what a team adds to a generated project (GEN-006): a slot
// widget and a native route, registered in the PluxConfig they build.
const nativeCode = `import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';

import 'main.dart';
import 'plux/plux_options.g.dart';

class GenBadge extends StatelessWidget {
  const GenBadge({super.key, required this.label});

  final String label;

  @override
  Widget build(BuildContext context) => Text(label);
}

class GenProfileParams {
  const GenProfileParams();

  factory GenProfileParams.fromJson(Map<String, Object?> json) =>
      const GenProfileParams();
}

PluxConfig nativeConfig({String? storage, PluxErrorHandler? onError}) =>
    PluxConfig(
      appId: PluxOptions.appId,
      endpoint: Uri.parse(PluxOptions.endpoint),
      environment: PluxOptions.environment,
      channel: PluxOptions.channel,
      rootKeys: PluxOptions.rootKeys,
      navigatorKey: navigatorKey,
      storageDirectory: storage,
      onError: onError,
      nativeRoutes: {
        'gen-profile': PluxNativeRoute<GenProfileParams, void>(
          params: GenProfileParams.fromJson,
          builder: (context, params) =>
              const Scaffold(body: Center(child: Text('native profile'))),
        ),
      },
      nativeSlots: {
        'GenBadge': PluxNativeSlot(
          (context, slot) => GenBadge(label: slot['label']! as String),
        ),
      },
    );
`

// generatedTest runs the generated app on the host: from its embedded
// release (GEN-002), then a newer release without a rebuild (GEN-005),
// then a plugin page using the native code the team added (GEN-006).
const generatedTest = `// The host run has no platform key store.
// ignore_for_file: implementation_imports
import 'dart:async';
import 'dart:io';

import 'package:demo/main.dart';
import 'package:demo/native.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/core/runtime.dart' show RuntimeOverrides;
import 'package:plux_flutter/src/sync/sync_engine.dart' show MemoryCredentialStore;

Future<void> pumpUntil(WidgetTester tester, Finder finder) async {
  final end = DateTime.now().add(const Duration(seconds: 60));
  while (finder.evaluate().isEmpty) {
    if (DateTime.now().isAfter(end)) fail('waiting for $finder');
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 50)),
    );
    await tester.pump(const Duration(milliseconds: 50));
  }
}

void main() {
  testWidgets('the generated app runs its embedded release, then a newer one, then the native code added to it [GEN-002] [GEN-005] [GEN-006]', (tester) async {
    final dir = Directory.systemTemp.createTempSync('plux_generated');
    addTearDown(() => dir.deleteSync(recursive: true));
    final problems = <PluxException>[];
    Future<void> launch() async {
      await tester.runAsync(
        () => Plux.initializeWith(
          nativeConfig(storage: dir.path, onError: (e, _) => problems.add(e)),
          const RuntimeOverrides(credentials: MemoryCredentialStore.new),
        ),
      );
      await tester.pumpWidget(const App());
    }

    Future<void> stop() async {
      await tester.pumpWidget(const SizedBox());
      await tester.runAsync(Plux.dispose);
    }

    await launch();
    await pumpUntil(tester, find.text('Welcome to Plux'));
    final result = await tester.runAsync(() async => Plux.sync());
    expect(result, isNotNull);
    await stop();

    await launch();
    await pumpUntil(tester, find.text('Welcome to Plux, again'));
    unawaited(Plux.open<void>(navigatorKey.currentContext!, 'gen-native'));
    await pumpUntil(tester, find.text('native badge'));
    await tester.tap(find.text('Open profile'));
    await pumpUntil(tester, find.text('native profile'));
    expect(problems, isEmpty, reason: problems.join('\n'));
    await stop();
  }, timeout: const Timeout(Duration(minutes: 3)));
}
`

// Verifies: GEN-002, GEN-005, GEN-006.
// A project generated with plux create from the starter fixture's
// release: it builds and runs its embedded release unchanged, shows a
// newer release without a rebuild, and keeps every Plux capability once a
// team adds native code to it — plux native scan finds the route and slot,
// and a plugin page uses both. It runs only when PLUX_E2E_FLUTTER names
// the flutter executable (make e2e-starter).
func TestGeneratedAppAgainstTheServer(t *testing.T) {
	flutter := os.Getenv("PLUX_E2E_FLUTTER")
	if flutter == "" {
		t.Skip("set PLUX_E2E_FLUTTER to the flutter executable (make e2e-starter)")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	st := startStack(t, "127.0.0.1:18095")
	project := st.project(t, "starter")
	st.run(t, 0, "publish", "-C", project, "--env", "staging", "--promote", "staging")

	app := filepath.Join(st.dir, "generated")
	st.run(t, 0, "create", "--server", st.server, "--org", st.org, "--env", "staging", "--application-id", "dev.plux.generated",
		"--plux-path", filepath.Join(repo, "packages", "plux_flutter"), "--out", app, "demo")

	// The team adds native code: a slot and a route, the scanner as a dev
	// dependency, the slot listed in plux.yaml, then plux native scan.
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(app, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path) //nolint:gosec // the test's own files
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var catalogue map[string]any
	if err := json.Unmarshal([]byte(read(filepath.Join(project, "native-catalogue.json"))), &catalogue); err != nil {
		t.Fatal(err)
	}
	write("lib/native.dart", nativeCode)
	write("plux.yaml", read(filepath.Join(app, "plux.yaml"))+"  - GenBadge\ncatalogueId: "+catalogue["id"].(string)+"\n")
	write("pubspec.yaml", strings.Replace(read(filepath.Join(app, "pubspec.yaml")), "dev_dependencies:\n",
		"dev_dependencies:\n  plux_native_scan:\n    path: "+filepath.Join(repo, "packages", "plux_native_scan")+"\n", 1))
	flutterIn := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(st.ctx, flutter, args...) //nolint:gosec // G204: the flutter the developer named.
		cmd.Dir = app
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("flutter %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	if err := os.MkdirAll(filepath.Join(app, "test"), 0o750); err != nil {
		t.Fatal(err)
	}
	write("test/generated_test.dart", generatedTest)
	flutterIn("pub", "get")
	// The project as generated, with the team's code, analyses clean,
	// its launch test included, which only a device run compiles.
	flutterIn("analyze") //nolint:misspell // the flutter subcommand
	st.run(t, 0, "native", "scan", "--host", app, "--dart", filepath.Join(filepath.Dir(flutter), "dart"))
	var scanned map[string]any
	if err := json.Unmarshal([]byte(read(filepath.Join(app, "plux.catalogue.json"))), &scanned); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(filepath.Join(app, "plux.catalogue.json")), `"GenBadge"`) || !strings.Contains(read(filepath.Join(app, "plux.catalogue.json")), `"gen-profile"`) {
		t.Fatalf("the scan found no route or slot:\n%s", read(filepath.Join(app, "plux.catalogue.json")))
	}

	// A newer release: the welcome title changes (GEN-005), and a page uses
	// the scanned slot and route (GEN-006); the host's other slot stays.
	for _, key := range []string{"routes", "slots"} {
		have, _ := catalogue[key].([]any)
		add, _ := scanned[key].([]any)
		catalogue[key] = append(have, add...)
	}
	data, _ := json.MarshalIndent(catalogue, "", "  ")
	if err := os.WriteFile(filepath.Join(project, "native-catalogue.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	translations := filepath.Join(project, "translations", "en.json")
	if err := os.WriteFile(translations, []byte(strings.Replace(read(translations), `"Welcome to Plux"`, `"Welcome to Plux, again"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	page := `{"id": "01e0c450-6c00-7000-8000-000000000070", "key": "gen-native", "kind": "page", "pageKind": "screen", "route": "gen-native",
  "schemaVersion": "1.0.0", "title": "Native", "root": {"id": "01e0c450-6c00-7000-8000-000000000071", "type": "Column", "children": [
    {"id": "01e0c450-6c00-7000-8000-000000000072", "type": "GenBadge", "props": {"label": "native badge"}},
    {"id": "01e0c450-6c00-7000-8000-000000000073", "type": "TextButton",
     "events": {"onPressed": {"steps": [{"action": "navigate", "id": "go", "input": {"route": "gen-profile"}}]}},
     "slots": {"child": {"id": "01e0c450-6c00-7000-8000-000000000074", "type": "Text", "props": {"data": "Open profile"}}}}]}}`
	if err := os.WriteFile(filepath.Join(project, "plugins", "welcome", "pages", "gen-native.page.json"), []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}
	pluginFile := filepath.Join(project, "plugins", "welcome", "plugin.json")
	if err := os.WriteFile(pluginFile, []byte(strings.Replace(read(pluginFile), `"pages": [`, `"pages": ["01e0c450-6c00-7000-8000-000000000070", `, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	st.run(t, 0, "publish", "-C", project, "--env", "staging", "--promote", "staging", "--acknowledge-warnings")

	out := flutterIn("test", "--reporter=expanded", "test/generated_test.dart")
	if !strings.Contains(out, "All tests passed!") {
		t.Fatalf("the generated app:\n%s", out)
	}
	t.Logf("the generated app:\n%s", out)

	// On an emulator or simulator (the device jobs), the project's own
	// launch test runs there: the app built unchanged renders its entry
	// page (GEN-002).
	device := os.Getenv("PLUX_E2E_DEVICE")
	if device == "" {
		return
	}
	if bundle := os.Getenv("PLUX_E2E_XCRESULT"); bundle != "" {
		t.Setenv("PLUX_E2E_XCRESULT", strings.TrimSuffix(bundle, ".xcresult")+"-generated.xcresult")
	}
	steps, succeeded := deviceSteps(flutter, device, nil)
	var log strings.Builder
	for _, step := range steps {
		cmd := exec.CommandContext(st.ctx, step[0], step[1:]...) //nolint:gosec // G204: the flutter, Xcode and device the developer named.
		cmd.Dir = app
		cmd.Stdout, cmd.Stderr = &log, &log
		if err := cmd.Run(); err != nil {
			t.Fatalf("the generated app on %s: %s: %v\n%s", device, strings.Join(step[:2], " "), err, tail(log.String(), 150))
		}
	}
	if !strings.Contains(log.String(), succeeded) {
		t.Fatalf("the generated app on %s:\n%s", device, tail(log.String(), 150))
	}
	t.Logf("the generated app on %s:\n%s", device, tail(log.String(), 40))
}

// tail is the last n lines of s, so a device log stays readable in the
// job's output.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return "…\n" + strings.Join(lines[len(lines)-n:], "\n")
}
