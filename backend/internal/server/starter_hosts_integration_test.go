// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hostLinkPage is a plugin page that opens the host's native settings
// screen through the module's native route host-settings.
const hostLinkPage = `{"id": "01e0c450-6c00-7000-8000-000000000080", "key": "host-link", "kind": "page", "pageKind": "screen", "route": "host-link",
  "schemaVersion": "1.0.0", "title": "Host link", "root": {"id": "01e0c450-6c00-7000-8000-000000000081", "type": "Scaffold", "slots": {
    "appBar": {"id": "01e0c450-6c00-7000-8000-000000000082", "type": "AppBar", "slots": {
      "title": {"id": "01e0c450-6c00-7000-8000-000000000083", "type": "Text", "props": {"data": "Host link"}}}},
    "body": {"id": "01e0c450-6c00-7000-8000-000000000084", "type": "Center", "slots": {"child":
      {"id": "01e0c450-6c00-7000-8000-000000000085", "type": "TextButton",
       "events": {"onPressed": {"steps": [{"action": "navigate", "id": "go", "input": {"route": "host-settings"}}]}},
       "slots": {"child": {"id": "01e0c450-6c00-7000-8000-000000000086", "type": "Text", "props": {"data": "Open host settings"}}}}}}}}}`

// hostFlow is one add-to-app flow: the module's Dart test plays it on the
// development machine, the hosts' UI tests on a device. Each runs in a
// process of its own.
type hostFlow struct {
	name     string // PLUX_FLOW of apps/add_to_app/plux_module/test/host_flows_test.dart
	endpoint string // where the runtime syncs from
	android  string // the HostFlowsTest method (android_host)
	ios      string // the HostFlowsTests method (ios_host)
}

// Verifies: HST-033.
// The Plux module embedded in native Android and iOS apps
// (apps/add_to_app): a native screen opens a Plux page by route, rendered
// from the baseline while the server is out of reach; sync applies a newer
// release once no Plux page is open; a plugin page opens the host's native
// screen through a native route. On the development machine the module's
// Dart test plays the host; with PLUX_E2E_DEVICE the Kotlin host's
// UiAutomator tests run on the emulator, or with PLUX_E2E_XCTEST the Swift
// host's XCUITests on the simulator (test/e2e/android.sh, ios.sh). It runs
// only when PLUX_E2E_FLUTTER names the flutter executable (make
// e2e-starter).
func TestAddToAppAgainstTheServer(t *testing.T) {
	flutter := os.Getenv("PLUX_E2E_FLUTTER")
	if flutter == "" {
		t.Skip("set PLUX_E2E_FLUTTER to the flutter executable (make e2e-starter)")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	hosts := filepath.Join(repo, "apps", "add_to_app")
	module := filepath.Join(hosts, "plux_module")
	// The device reaches the server on its loopback port 18094 (adb
	// reverse on Android), free again once the starter's test has ended.
	st := startStack(t, "127.0.0.1:18094")
	project := st.project(t, "starter")
	addHostLink(t, project)
	st.run(t, 0, "publish", "-C", project, "--env", "staging", "--promote", "staging")

	// The module embeds release 1 as its baseline; release 2 changes the
	// welcome page's title.
	baseline := filepath.Join(module, "assets", "plux")
	t.Cleanup(func() { clearBaseline(t, baseline) })
	st.run(t, 0, "pull", "-C", project, "--env", "staging", "-o", baseline)
	var keys struct {
		Keys []struct{ KeyID, PublicKey string }
	}
	decode(t, st.run(t, 0, "keys", "-C", project, "--env", "staging", "--json"), &keys)
	var roots []string
	for _, k := range keys.Keys {
		roots = append(roots, k.KeyID+":"+k.PublicKey)
	}
	translations := filepath.Join(project, "translations", "en.json")
	edit(t, translations, `"Welcome to Plux"`, `"Welcome to Plux, again"`)
	st.run(t, 0, "publish", "-C", project, "--env", "staging", "--promote", "staging")

	settings := map[string]string{"appId": st.app.ID, "environment": "staging", "hostBuild": "add-to-app", "rootKeys": strings.Join(roots, ",")}
	flows := []hostFlow{
		// Nothing listens on the discard port: the server is out of reach.
		{name: "offline", endpoint: "http://127.0.0.1:9", android: "rendersOfflineFromTheBaseline", ios: "testRendersOfflineFromTheBaseline"},
		{name: "online", endpoint: st.server, android: "appliesUpdatesAndOpensNativeScreens", ios: "testAppliesUpdatesAndOpensNativeScreens"},
	}
	for _, f := range flows {
		cmd := exec.CommandContext(st.ctx, flutter, "test", "--reporter=expanded", "test/host_flows_test.dart", //nolint:gosec // G204: the flutter the developer named.
			"--dart-define=PLUX_FLOW="+f.name, "--dart-define=PLUX_ENDPOINT="+f.endpoint, "--dart-define=PLUX_APP_ID="+settings["appId"],
			"--dart-define=PLUX_ENVIRONMENT="+settings["environment"], "--dart-define=PLUX_ROOT_KEYS="+settings["rootKeys"])
		cmd.Dir = module
		out, err := cmd.CombinedOutput()
		// One flow runs and the other is skipped.
		if err != nil || !strings.Contains(string(out), "+1 ~1: All tests passed!") {
			t.Fatalf("the module's %s flow: %v\n%s", f.name, err, tail(string(out), 150))
		}
	}

	device := os.Getenv("PLUX_E2E_DEVICE")
	if device == "" {
		return
	}
	ctx, cancel := context.WithTimeout(st.ctx, deviceTimeout(t))
	defer cancel()
	if os.Getenv("PLUX_E2E_XCTEST") != "" {
		iosHost(ctx, t, filepath.Join(hosts, "ios_host"), device, flows, settings)
	} else {
		androidHost(ctx, t, hosts, device, flows, settings)
	}
}

// addHostLink adds the host-link page to the starter project's plugin and
// the host's native route host-settings to its native catalogue.
func addHostLink(t *testing.T, project string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(project, "plugins", "welcome", "pages", "host-link.page.json"), []byte(hostLinkPage), 0o600); err != nil {
		t.Fatal(err)
	}
	edit(t, filepath.Join(project, "plugins", "welcome", "plugin.json"), `"pages": [`, `"pages": ["01e0c450-6c00-7000-8000-000000000080", `)
	edit(t, filepath.Join(project, "native-catalogue.json"), `"routes": [`,
		`"routes": [{"name": "host-settings", "description": "The host's native settings screen."}, `)
}

// edit replaces the first from in the file at path with to, and fails the
// test when the file has no from.
func edit(t *testing.T, path, from, to string) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // the test's own files
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("%s has no %s", path, from)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), from, to, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// clearBaseline removes the baseline plux pull wrote into the module,
// keeping the directories' placeholders and README.
func clearBaseline(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == ".gitkeep" || p == filepath.Join(dir, "README.md") {
			return err
		}
		return os.Remove(p)
	})
	if err != nil {
		t.Errorf("clear the module's baseline: %v", err)
	}
}

// androidHost builds the Kotlin host and its UiAutomator tests with the
// Gradle wrapper flutter pub get wrote into the module, installs both on
// the emulator, and runs each flow in an instrumentation of its own: a
// new process, so a new engine and runtime.
func androidHost(ctx context.Context, t *testing.T, hosts, device string, flows []hostFlow, settings map[string]string) {
	t.Helper()
	host := filepath.Join(hosts, "android_host")
	run := func(dir string, name string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: the SDK's tools and the device the developer named.
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", filepath.Base(name), strings.Join(args, " "), err, tail(string(out), 150))
		}
		return string(out)
	}
	run(host, filepath.Join(hosts, "plux_module", ".android", "gradlew"), "--no-daemon", "--console=plain",
		"-Ptarget-platform=android-x64", ":app:assembleDebug", ":app:assembleDebugAndroidTest")
	adb := "adb"
	for _, v := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if sdk := os.Getenv(v); sdk != "" {
			adb = filepath.Join(sdk, "platform-tools", "adb")
			break
		}
	}
	apks := filepath.Join(host, "app", "build", "outputs", "apk")
	run(host, adb, "-s", device, "install", "-r", "-t", filepath.Join(apks, "debug", "app-debug.apk"))
	run(host, adb, "-s", device, "install", "-r", "-t", filepath.Join(apks, "androidTest", "debug", "app-debug-androidTest.apk"))
	run(host, adb, "-s", device, "shell", "pm", "clear", "dev.plux.addtoapp.host")
	for _, f := range flows {
		run(host, adb, "-s", device, "logcat", "-c")
		args := []string{"-s", device, "shell", "am", "instrument", "-w", "-e", "class", "dev.plux.addtoapp.host.HostFlowsTest#" + f.android, "-e", "endpoint", f.endpoint}
		for _, k := range []string{"appId", "environment", "hostBuild", "rootKeys"} {
			args = append(args, "-e", k, settings[k])
		}
		out := run(host, adb, append(args, "dev.plux.addtoapp.host.test/androidx.test.runner.AndroidJUnitRunner")...)
		if !strings.Contains(out, "OK (1 test)") {
			// The module's and the runtime's messages, and crashes.
			log, _ := exec.CommandContext(ctx, adb, "-s", device, "logcat", "-d", "-s", "flutter:V", "AndroidRuntime:E").CombinedOutput() //nolint:gosec // G204: the SDK's adb and the device the developer named.
			t.Fatalf("the Android host's %s flow on %s:\n%s\nthe device log:\n%s", f.name, device, tail(out, 150), tail(string(log), 80))
		}
		t.Logf("the Android host's %s flow on %s:\n%s", f.name, device, tail(out, 10))
	}
}

// iosHost installs the module's pods into the Swift host, builds it and
// its XCUITests once, and runs each flow in an xcodebuild run of its own;
// each test launches the app anew. The runtime's settings reach the tests
// as TEST_RUNNER_PLUX_* variables, and PLUX_E2E_XCRESULT, when set, names
// the result bundles.
func iosHost(ctx context.Context, t *testing.T, host, device string, flows []hostFlow, settings map[string]string) {
	t.Helper()
	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: CocoaPods, Xcode and the simulator the developer named.
		cmd.Dir = host
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, tail(string(out), 150))
		}
	}
	run("pod", "install")
	xcodebuild := []string{
		"-workspace", "HostApp.xcworkspace", "-scheme", "HostApp", "-configuration", "Debug",
		"-destination", "platform=iOS Simulator,id=" + device, "-derivedDataPath", filepath.Join(t.TempDir(), "derived"),
	}
	run("xcodebuild", append([]string{"build-for-testing"}, xcodebuild...)...)
	// A new install: the offline flow starts without the runtime's state.
	_ = exec.CommandContext(ctx, "xcrun", "simctl", "uninstall", device, "dev.plux.addtoapp.host").Run() //nolint:gosec // G204: the simulator the developer named.
	for _, f := range flows {
		args := append([]string{"test-without-building"}, xcodebuild...)
		args = append(args, "-only-testing:HostAppUITests/HostFlowsTests/"+f.ios, "-parallel-testing-enabled", "NO")
		if bundle := os.Getenv("PLUX_E2E_XCRESULT"); bundle != "" {
			args = append(args, "-resultBundlePath", strings.TrimSuffix(bundle, ".xcresult")+"-add-to-app-"+f.name+".xcresult")
		}
		env := []string{
			"TEST_RUNNER_PLUX_ENDPOINT=" + f.endpoint,
			"TEST_RUNNER_PLUX_APP_ID=" + settings["appId"],
			"TEST_RUNNER_PLUX_ENVIRONMENT=" + settings["environment"],
			"TEST_RUNNER_PLUX_HOST_BUILD=" + settings["hostBuild"],
			"TEST_RUNNER_PLUX_ROOT_KEYS=" + settings["rootKeys"],
		}
		cmd := exec.CommandContext(ctx, "xcodebuild", args...) //nolint:gosec // G204: Xcode and the simulator the developer named.
		cmd.Dir = host
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "** TEST EXECUTE SUCCEEDED **") {
			// The module's and the runtime's messages.
			log, _ := exec.CommandContext(ctx, "xcrun", "simctl", "spawn", device, "log", "show", "--last", "10m", "--style", "compact", //nolint:gosec // G204: the simulator the developer named.
				"--predicate", `process == "HostApp" AND eventMessage CONTAINS "flutter:"`).CombinedOutput()
			t.Fatalf("the iOS host's %s flow on %s: %v\n%s\nthe app's log:\n%s", f.name, device, err, tail(string(out), 150), tail(string(log), 80))
		}
		t.Logf("the iOS host's %s flow on %s:\n%s", f.name, device, tail(string(out), 10))
	}
}
