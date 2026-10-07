// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.plux_flutter

import android.Manifest
import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import io.flutter.plugin.common.PluginRegistry
import java.io.File

/**
 * The device actions that need the platform (SEC-080): the share sheet and
 * permission prompts. Nothing here reads or returns a picked file's
 * contents; files are handed to the share sheet through a content URI, and
 * only files under the app's cache or files directories can be shared.
 */
internal class PluxDevice(private val context: Context) :
    PluginRegistry.RequestPermissionsResultListener {
    /** The foreground activity, or null while none is attached. */
    var activity: Activity? = null

    private val pending = HashMap<Int, MethodChannel.Result>()
    private var nextCode = REQUEST_BASE

    fun share(call: MethodCall, result: MethodChannel.Result) {
        val host: Context = activity ?: context
        val text = listOfNotNull(call.argument<String>("text"), call.argument<String>("url"))
            .joinToString("\n")
        val path = call.argument<String>("filePath")
        val mime = call.argument<String>("mimeType")
        val send = Intent(Intent.ACTION_SEND)
        if (path != null) {
            val file = File(path).canonicalFile
            if (!allowed(file)) {
                result.error("PLUX_PLATFORM", "file", null)
                return
            }
            val uri = FileProvider.getUriForFile(context, "${context.packageName}.plux.fileprovider", file)
            send.putExtra(Intent.EXTRA_STREAM, uri)
            send.type = mime ?: "application/octet-stream"
            send.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            if (text.isNotEmpty()) send.putExtra(Intent.EXTRA_TEXT, text)
        } else {
            send.type = "text/plain"
            send.putExtra(Intent.EXTRA_TEXT, text)
        }
        val chooser = Intent.createChooser(send, null)
        if (host !is Activity) chooser.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        host.startActivity(chooser)
        result.success(null)
    }

    private fun allowed(file: File): Boolean {
        val roots = listOf(context.cacheDir, context.filesDir).map { it.canonicalFile }
        return roots.any { file.path.startsWith(it.path + File.separator) }
    }

    fun requestPermission(call: MethodCall, result: MethodChannel.Result) {
        val name = call.argument<String>("permission")
        val wanted = name?.let { permissions(it) }
        if (wanted == null) {
            result.error("PLUX_PLATFORM", "permission", null)
            return
        }
        if (wanted.isEmpty() || granted(wanted)) {
            result.success(true)
            return
        }
        val host = activity
        if (host == null) {
            result.error("PLUX_PLATFORM", "activity", null)
            return
        }
        val code = nextCode++
        if (nextCode > REQUEST_BASE + REQUEST_RANGE) nextCode = REQUEST_BASE
        pending[code] = result
        host.requestPermissions(wanted.toTypedArray(), code)
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray,
    ): Boolean {
        val result = pending.remove(requestCode) ?: return false
        // Location is granted when either precision is; the others need
        // every permission they asked for.
        val any = permissions.any { it.startsWith("android.permission.ACCESS_") }
        val ok = if (any) grantResults.any { it == PackageManager.PERMISSION_GRANTED }
        else grantResults.isNotEmpty() && grantResults.all { it == PackageManager.PERMISSION_GRANTED }
        result.success(ok)
        return true
    }

    private fun granted(wanted: List<String>): Boolean {
        val states = wanted.map { ContextCompat.checkSelfPermission(context, it) == PackageManager.PERMISSION_GRANTED }
        return if (wanted.any { it.startsWith("android.permission.ACCESS_") }) states.any { it } else states.all { it }
    }

    /** The runtime permissions behind a device API; empty when none is needed. */
    private fun permissions(api: String): List<String>? = when (api) {
        "camera" -> listOf(Manifest.permission.CAMERA)
        "photos" -> if (Build.VERSION.SDK_INT >= 33) listOf(Manifest.permission.READ_MEDIA_IMAGES)
        else listOf(Manifest.permission.READ_EXTERNAL_STORAGE)
        "location" -> listOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION)
        "contacts" -> listOf(Manifest.permission.READ_CONTACTS)
        "notifications" -> if (Build.VERSION.SDK_INT >= 33) listOf(Manifest.permission.POST_NOTIFICATIONS)
        else emptyList()
        // USE_BIOMETRIC is a normal permission: granted at install.
        "biometrics" -> emptyList()
        else -> null
    }

    private companion object {
        const val REQUEST_BASE = 0x5100
        const val REQUEST_RANGE = 0xFF
    }
}
