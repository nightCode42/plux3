// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.addtoapp.host

import android.content.Intent
import android.os.SystemClock
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.UiObject2
import org.junit.Test
import org.junit.runner.RunWith
import java.util.regex.Pattern

/**
 * The add-to-app flows (HST-033) on an emulator, against the server
 * TestAddToAppAgainstTheServer starts. They find what is on screen by its
 * accessibility label: Flutter reports a widget's label as the content
 * description, a native view its text. The driver runs each test in an
 * instrumentation of its own, so each starts a new process and engine;
 * the instrumentation's arguments are the runtime's settings.
 */
@RunWith(AndroidJUnit4::class)
class HostFlowsTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val device = UiDevice.getInstance(instrumentation)

    /** A native screen opens a Plux page, rendered from the baseline with the server out of reach. */
    @Test
    fun rendersOfflineFromTheBaseline() {
        launch()
        tap("Open welcome")
        waitFor("Welcome to Plux", exact = true)
        device.pressBack()
        waitFor("Open welcome")
    }

    /**
     * Sync applies the newer release once no Plux page is open, and a
     * plugin page in a FlutterFragment opens the host's native screen.
     */
    @Test
    fun appliesUpdatesAndOpensNativeScreens() {
        launch()
        val deadline = SystemClock.uptimeMillis() + 60_000
        while (true) {
            tap("Open welcome")
            waitFor("Welcome to Plux")
            if (find("Welcome to Plux, again", exact = true) != null) break
            if (SystemClock.uptimeMillis() > deadline) throw AssertionError("the update never applied")
            device.pressBack()
            waitFor("Open welcome")
            SystemClock.sleep(500)
        }
        device.pressBack()
        waitFor("Open welcome")

        tap("Open host link")
        waitFor("Native header", exact = true)
        tap("Open host settings")
        waitFor("Host settings", exact = true)
        device.pressBack()
        waitFor("Open host settings")
        device.pressBack()
        waitFor("Open welcome")
    }

    // Starts the native home screen with the runtime's settings.
    private fun launch() {
        val args = InstrumentationRegistry.getArguments()
        val context = instrumentation.targetContext
        val intent =
            Intent(context, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
        for (key in listOf("endpoint", "appId", "environment", "hostBuild", "rootKeys")) {
            args.getString(key)?.let { intent.putExtra(key, it) }
        }
        context.startActivity(intent)
        waitFor("Open welcome")
    }

    private fun find(label: String, exact: Boolean = false): UiObject2? {
        val selectors =
            if (exact) {
                listOf(By.desc(label), By.text(label))
            } else {
                listOf(By.descContains(label), By.textContains(label))
            }
        return selectors.firstNotNullOfOrNull { device.findObject(it) }
    }

    // Waits up to a minute: the first page starts the engine and the runtime.
    private fun waitFor(label: String, exact: Boolean = false): UiObject2 {
        val deadline = SystemClock.uptimeMillis() + 60_000
        while (true) {
            find(label, exact)?.let { return it }
            if (SystemClock.uptimeMillis() > deadline) {
                throw AssertionError(
                    "nothing on screen reads \"$label\"; it reads: ${labels()}; ${resumedActivity()}",
                )
            }
            SystemClock.sleep(200)
        }
    }

    // Every text and content description on screen, for a failure's message;
    // a view that went away meanwhile is left out.
    private fun labels(): String {
        val labels =
            device.findObjects(By.clazz(Pattern.compile(".*"))).flatMap { o ->
                runCatching { listOfNotNull(o.text, o.contentDescription) }
                    .getOrDefault(emptyList())
                    .filter { it.isNotEmpty() }
            }
        return if (labels.isEmpty()) "nothing" else labels.joinToString { "\"$it\"" }
    }

    // The activity in front, for a failure's message.
    private fun resumedActivity(): String =
        runCatching {
            device
                .executeShellCommand("dumpsys activity activities")
                .lines()
                .map { it.trim() }
                .filter { it.contains("ResumedActivity") }
                .distinct()
                .joinToString("; ")
        }.getOrElse { "no activity dump: $it" }

    private fun tap(label: String) = waitFor(label).click()
}
