// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.plux_flutter

import android.content.Context
import com.google.android.play.core.integrity.IntegrityManagerFactory
import com.google.android.play.core.integrity.StandardIntegrityException
import com.google.android.play.core.integrity.StandardIntegrityManager
import com.google.android.play.core.integrity.StandardIntegrityManager.PrepareIntegrityTokenRequest
import com.google.android.play.core.integrity.StandardIntegrityManager.StandardIntegrityTokenProvider
import com.google.android.play.core.integrity.StandardIntegrityManager.StandardIntegrityTokenRequest
import com.google.android.play.core.integrity.model.StandardIntegrityErrorCode
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel

/**
 * The Play Integrity side of device attestation (SEC-003, ADR-0012): a
 * standard request whose request hash binds the server challenge and the DPoP
 * key thumbprint. The token provider is prepared once per process and cached
 * here, on the plugin's instance. Play Integrity delivers its results on the
 * main thread, as `MethodChannel.Result` requires. A device without the API
 * or without Play services answers `UNSUPPORTED`, which a development build
 * may turn into development evidence (SEC-008); every other failure is
 * `PLUX_PLATFORM`. Neither a token nor a request hash appears in an error.
 */
class PluxAttestation(private val context: Context) {
    private var provider: StandardIntegrityTokenProvider? = null
    private var providerProject: Long = 0

    /** Handles attestationSupported and integrityToken; never throws. */
    fun handle(call: MethodCall, result: MethodChannel.Result) {
        try {
            val project = (call.argument<Any>("cloudProjectNumber") as? Number)?.toLong()
            when (call.method) {
                "attestationSupported" -> supported(project, result)
                "integrityToken" -> {
                    val hash = call.argument<String>("requestHash")?.takeIf { it.isNotEmpty() }
                        ?: throw IllegalArgumentException("requestHash")
                    token(project ?: throw IllegalArgumentException("cloudProjectNumber"), hash, result)
                }
                else -> result.notImplemented()
            }
        } catch (e: Exception) {
            result.error(PLATFORM, e.javaClass.simpleName, null)
        }
    }

    /**
     * Whether the Integrity API answers on this device. Without a project
     * number the API cannot be tried; the device is reported as supported and
     * the request itself fails.
     */
    private fun supported(project: Long?, result: MethodChannel.Result) {
        if (project == null) {
            result.success(true)
            return
        }
        prepare(
            project,
            { result.success(true) },
            { e -> if (isUnsupported(e)) result.success(false) else result.error(PLATFORM, e.javaClass.simpleName, null) },
        )
    }

    private fun token(project: Long, hash: String, result: MethodChannel.Result) {
        prepare(project, { p ->
            val request = StandardIntegrityTokenRequest.builder().setRequestHash(hash).build()
            p.request(request)
                .addOnSuccessListener { response -> result.success(response.token()) }
                .addOnFailureListener { e ->
                    // A provider that failed may have expired: prepare again next time.
                    provider = null
                    fail(e, result)
                }
        }, { e -> fail(e, result) })
    }

    private fun prepare(project: Long, onReady: (StandardIntegrityTokenProvider) -> Unit, onError: (Exception) -> Unit) {
        provider?.takeIf { providerProject == project }?.let {
            onReady(it)
            return
        }
        val manager: StandardIntegrityManager = IntegrityManagerFactory.createStandard(context)
        val request = PrepareIntegrityTokenRequest.builder().setCloudProjectNumber(project).build()
        manager.prepareIntegrityToken(request)
            .addOnSuccessListener { p ->
                provider = p
                providerProject = project
                onReady(p)
            }
            .addOnFailureListener { e -> onError(e) }
    }

    private fun fail(e: Exception, result: MethodChannel.Result) {
        result.error(if (isUnsupported(e)) UNSUPPORTED else PLATFORM, e.javaClass.simpleName, null)
    }

    /** The API or Google Play services are absent or too old. */
    private fun isUnsupported(e: Exception): Boolean =
        e is StandardIntegrityException && e.errorCode in UNSUPPORTED_CODES

    private companion object {
        const val PLATFORM = "PLUX_PLATFORM"
        const val UNSUPPORTED = "UNSUPPORTED"
        val UNSUPPORTED_CODES = setOf(
            StandardIntegrityErrorCode.API_NOT_AVAILABLE,
            StandardIntegrityErrorCode.PLAY_SERVICES_NOT_FOUND,
            StandardIntegrityErrorCode.PLAY_SERVICES_VERSION_OUTDATED,
        )
    }
}
