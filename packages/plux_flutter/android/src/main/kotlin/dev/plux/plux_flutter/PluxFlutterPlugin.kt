// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.plux_flutter

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import io.flutter.embedding.engine.plugins.FlutterPlugin
import io.flutter.embedding.engine.plugins.activity.ActivityAware
import io.flutter.embedding.engine.plugins.activity.ActivityPluginBinding
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * The platform services of the Plux runtime (ADR-0029): where the release
 * store lives, and secrets kept encrypted under an Android Keystore key, so
 * that no secret is ever written in the clear. The device keys live in
 * [PluxKeys] (SEC-001), the Play Integrity attestation in [PluxAttestation]
 * (SEC-003). The device actions that need the platform, the share
 * sheet and permission prompts, run here too (SEC-080). Bundle data never crosses this channel.
 */
class PluxFlutterPlugin : FlutterPlugin, ActivityAware, MethodChannel.MethodCallHandler {
    private lateinit var channel: MethodChannel
    private lateinit var context: Context
    private lateinit var device: PluxDevice
    private lateinit var keys: PluxKeys
    private lateinit var attestation: PluxAttestation
    private var activityBinding: ActivityPluginBinding? = null

    override fun onAttachedToActivity(binding: ActivityPluginBinding) {
        activityBinding = binding
        device.activity = binding.activity
        binding.addRequestPermissionsResultListener(device)
    }

    override fun onDetachedFromActivityForConfigChanges() = onDetachedFromActivity()

    override fun onReattachedToActivityForConfigChanges(binding: ActivityPluginBinding) =
        onAttachedToActivity(binding)

    override fun onDetachedFromActivity() {
        activityBinding?.removeRequestPermissionsResultListener(device)
        activityBinding = null
        device.activity = null
    }

    override fun onAttachedToEngine(binding: FlutterPlugin.FlutterPluginBinding) {
        context = binding.applicationContext
        device = PluxDevice(context)
        keys = PluxKeys()
        attestation = PluxAttestation(context)
        channel = MethodChannel(binding.binaryMessenger, CHANNEL)
        channel.setMethodCallHandler(this)
    }

    override fun onDetachedFromEngine(binding: FlutterPlugin.FlutterPluginBinding) {
        channel.setMethodCallHandler(null)
        keys.close()
    }

    override fun onMethodCall(call: MethodCall, result: MethodChannel.Result) {
        try {
            when (call.method) {
                // Not backed up: releases are re-downloaded, never restored
                // onto another device.
                "storageDirectory" -> result.success(File(context.noBackupFilesDir, "plux").absolutePath)
                "secretRead" -> result.success(readSecret(name(call)))
                "secretWrite" -> {
                    writeSecret(name(call), call.argument<String>("value") ?: "")
                    result.success(null)
                }
                "secretDelete" -> {
                    secretFile(name(call)).delete()
                    result.success(null)
                }
                "keyCreate", "keyPublic", "keySign", "keyDelete" -> keys.handle(call, result)
                "attestationSupported", "integrityToken" -> attestation.handle(call, result)
                "share" -> device.share(call, result)
                "permissionRequest" -> device.requestPermission(call, result)
                else -> result.notImplemented()
            }
        } catch (e: Exception) {
            result.error("PLUX_PLATFORM", e.javaClass.simpleName, null)
        }
    }

    private fun name(call: MethodCall): String {
        val n = call.argument<String>("name") ?: throw IllegalArgumentException("name")
        require(NAME.matches(n)) { "name" }
        return n
    }

    private fun secretFile(name: String): File =
        File(File(context.noBackupFilesDir, "plux-secrets").apply { mkdirs() }, "$name.bin")

    private fun key(): SecretKey {
        val store = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (store.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    private fun writeSecret(name: String, value: String) {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val sealed = cipher.iv + cipher.doFinal(value.toByteArray(Charsets.UTF_8))
        val target = secretFile(name)
        val tmp = File(target.parentFile, "$name.tmp")
        tmp.outputStream().use { out ->
            out.write(sealed)
            out.fd.sync()
        }
        if (!tmp.renameTo(target)) throw IllegalStateException("rename")
    }

    private fun readSecret(name: String): String? {
        val f = secretFile(name)
        if (!f.exists()) return null
        val sealed = f.readBytes()
        if (sealed.size < IV_BYTES) return null
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, sealed, 0, IV_BYTES))
        return String(cipher.doFinal(sealed, IV_BYTES, sealed.size - IV_BYTES), Charsets.UTF_8)
    }

    private companion object {
        const val CHANNEL = "dev.plux/runtime"
        const val KEYSTORE = "AndroidKeyStore"
        const val ALIAS = "dev.plux.secrets"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_BYTES = 12
        val NAME = Regex("^[a-z0-9._-]{1,64}$")
    }
}
