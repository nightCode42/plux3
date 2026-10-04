// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.addtoapp.host

import android.app.Activity
import android.app.Application
import android.content.Intent
import android.os.Bundle
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.embedding.engine.FlutterEngineCache
import io.flutter.embedding.engine.dart.DartExecutor
import io.flutter.plugin.common.MethodChannel

/**
 * The host app (HST-033). One Flutter engine runs the Plux module: it is
 * created when a native screen first opens a Plux page, and kept in
 * [FlutterEngineCache] while the process lives, so later pages open
 * without starting Flutter again. The module and the host talk on the
 * `dev.plux/host` channel (plux_module/lib/plux_module.dart).
 */
class HostApp : Application() {
    /** The runtime's settings the module asks for. */
    private var settings: Map<String, Any> = emptyMap()

    private var channel: MethodChannel? = null
    private var resumed: Activity? = null
    private var nativeScreen: MethodChannel.Result? = null

    override fun onCreate() {
        super.onCreate()
        registerActivityLifecycleCallbacks(
            object : Application.ActivityLifecycleCallbacks {
                override fun onActivityResumed(activity: Activity) {
                    resumed = activity
                }

                override fun onActivityPaused(activity: Activity) {
                    if (resumed === activity) resumed = null
                }

                override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) = Unit

                override fun onActivityStarted(activity: Activity) = Unit

                override fun onActivityStopped(activity: Activity) = Unit

                override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) = Unit

                override fun onActivityDestroyed(activity: Activity) = Unit
            },
        )
    }

    /**
     * Sets the runtime's settings from [extras]: `endpoint`, `appId`,
     * `environment`, `hostBuild` and `rootKeys` (comma-separated
     * `keyId:hex`). They apply when the engine starts.
     */
    fun configure(extras: Bundle) {
        settings =
            buildMap {
                for (key in listOf("endpoint", "appId", "environment", "hostBuild")) {
                    extras.getString(key)?.let { put(key, it) }
                }
                extras.getString("rootKeys")?.let { keys ->
                    put("rootKeys", keys.split(",").filter { it.isNotEmpty() })
                }
            }
    }

    /** Opens the Plux page at [route] full screen, in a FlutterActivity. */
    fun openPage(from: Activity, route: String) {
        module().invokeMethod("open", mapOf("route" to route))
        from.startActivity(FlutterActivity.withCachedEngine(ENGINE).build(from))
    }

    /** Opens the Plux page at [route] below a native header, in a FlutterFragment. */
    fun openEmbedded(from: Activity, route: String) {
        module().invokeMethod("open", mapOf("route" to route))
        from.startActivity(Intent(from, EmbeddedActivity::class.java))
    }

    /** The native screen the module opened has closed. */
    fun nativeScreenClosed() {
        nativeScreen?.success(null)
        nativeScreen = null
    }

    // The channel to the module, starting its engine on first use.
    private fun module(): MethodChannel {
        channel?.let { return it }
        val engine = FlutterEngine(this)
        val channel = MethodChannel(engine.dartExecutor.binaryMessenger, "dev.plux/host")
        channel.setMethodCallHandler { call, result ->
            when (call.method) {
                "config" -> result.success(settings)
                "openNative" -> openNative(call.argument<String>("screen"), result)
                else -> result.notImplemented()
            }
        }
        engine.dartExecutor.executeDartEntrypoint(DartExecutor.DartEntrypoint.createDefault())
        FlutterEngineCache.getInstance().put(ENGINE, engine)
        this.channel = channel
        return channel
    }

    // Shows the native screen a plugin page asked for; the result follows
    // when it closes.
    private fun openNative(screen: String?, result: MethodChannel.Result) {
        val from = resumed
        if (screen != "settings" || from == null) {
            result.error("screen", "the host cannot show the native screen $screen now", null)
            return
        }
        nativeScreenClosed()
        nativeScreen = result
        from.startActivity(Intent(from, SettingsActivity::class.java))
    }

    companion object {
        /** The cached engine's ID. */
        const val ENGINE = "plux"
    }
}
