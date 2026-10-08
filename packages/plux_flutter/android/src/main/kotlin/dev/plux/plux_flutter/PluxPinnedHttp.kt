// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package dev.plux.plux_flutter

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.util.Base64
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import org.chromium.net.CronetEngine
import org.chromium.net.CronetException
import org.chromium.net.NetworkException
import org.chromium.net.UploadDataProviders
import org.chromium.net.UrlRequest
import org.chromium.net.UrlResponseInfo
import java.net.URI
import java.nio.ByteBuffer
import java.util.Date
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

/**
 * HTTP/2 to the Plux server over Cronet with its certificate chain pinned
 * (SEC-041, SYN-010). `cronet_http` builds its engine without public-key
 * pins and gives no way to hand it one, so the plugin builds the engine:
 * `addPublicKeyPins` for the server's host, the pins being the SPKI SHA-256
 * hashes of any certificate in the chain, and no bypass for local trust
 * anchors, so a user-installed CA cannot stand in for a pin.
 *
 * Dart pulls: `httpOpen` starts a request and answers with the status and
 * headers, `httpRead` answers with the next chunk of the body (Cronet
 * reads only when asked, which is the download's flow control), and
 * `httpClose` ends the request. A chain that matches no pin is reported as
 * `PLUX_PIN_MISMATCH`; every other failure as `PLUX_HTTP` with Cronet's
 * numeric error code and nothing of the request. `MethodChannel.Result`
 * is always answered on the main thread.
 *
 * An engine serves one set of pins. When Dart sends a different set (signed
 * metadata replaced the pins), a new engine is built; the old one is shut
 * down once its requests have finished.
 */
class PluxPinnedHttp(private val context: Context) {
    private val main = Handler(Looper.getMainLooper())
    private val executor = Executors.newCachedThreadPool()
    private val requests = ConcurrentHashMap<Int, Exchange>()
    private val ids = AtomicInteger()
    private val lock = Any()
    private var current: Engine? = null

    /** An engine for one host, user agent and set of pins. */
    private class Engine(val key: String, val engine: CronetEngine) {
        val active = AtomicInteger()
        @Volatile var retired = false
    }

    /** Handles httpOpen, httpRead and httpClose; never throws. */
    fun handle(call: MethodCall, result: MethodChannel.Result) {
        try {
            when (call.method) {
                "httpOpen" -> open(call, result)
                "httpRead" -> read(id(call), result)
                "httpClose" -> {
                    requests.remove(id(call))?.cancel()
                    result.success(null)
                }
                else -> result.notImplemented()
            }
        } catch (e: Exception) {
            result.error(HTTP, e.javaClass.simpleName, null)
        }
    }

    /** Cancels every request and shuts the engine down. */
    fun close() {
        requests.values.forEach { it.cancel() }
        requests.clear()
        synchronized(lock) {
            current?.let { retire(it) }
            current = null
        }
    }

    private fun id(call: MethodCall): Int =
        call.argument<Int>("id") ?: throw IllegalArgumentException("id")

    private fun open(call: MethodCall, result: MethodChannel.Result) {
        val url = call.argument<String>("url") ?: throw IllegalArgumentException("url")
        val host = URI(url).host ?: throw IllegalArgumentException("host")
        val pins = call.argument<List<String>>("pins")
            ?.map { Base64.decode(it, Base64.DEFAULT) }
            ?.onEach { require(it.size == 32) { "pin" } }
            ?.toSet()
        require(!pins.isNullOrEmpty()) { "pins" }
        val agent = call.argument<String>("userAgent") ?: ""
        val key = "$host|$agent|" + call.argument<List<String>>("pins")!!.sorted().joinToString(",")
        val engine = engineFor(key, host, agent, pins)
        val exchange = Exchange(
            engine = engine,
            follow = call.argument<Boolean>("followRedirects") ?: true,
            maxRedirects = call.argument<Int>("maxRedirects") ?: 5,
            opened = result,
        )
        val id = ids.incrementAndGet()
        exchange.id = id
        requests[id] = exchange
        try {
            val builder = engine.engine.newUrlRequestBuilder(url, exchange, executor)
                .setHttpMethod(call.argument<String>("method") ?: "GET")
                .disableCache()
            call.argument<Map<String, String>>("headers")?.forEach { (k, v) ->
                if (!k.equals("content-length", ignoreCase = true)) builder.addHeader(k, v)
            }
            call.argument<ByteArray>("body")?.let {
                builder.setUploadDataProvider(UploadDataProviders.create(it), executor)
            }
            exchange.request = builder.build()
            exchange.request!!.start()
        } catch (e: Exception) {
            requests.remove(id)
            release(engine)
            throw e
        }
    }

    private fun read(id: Int, result: MethodChannel.Result) {
        val exchange = requests[id] ?: return result.success(null)
        exchange.read(result)
    }

    /**
     * The engine for [key], built when the pins differ from those of the
     * engine in use.
     */
    private fun engineFor(key: String, host: String, agent: String, pins: Set<ByteArray>): Engine {
        synchronized(lock) {
            current?.let { if (it.key == key) { it.active.incrementAndGet(); return it } }
            val builder = CronetEngine.Builder(context)
                .enableHttp2(true)
                .enableQuic(false)
                .enableHttpCache(CronetEngine.Builder.HTTP_CACHE_DISABLED, 0)
                // A pin is not waived for a CA the user installed.
                .enablePublicKeyPinningBypassForLocalTrustAnchors(false)
                // Far in the future: a pin that lapsed would unpin silently.
                .addPublicKeyPins(host, pins, false, Date(FAR_FUTURE))
            if (agent.isNotEmpty()) builder.setUserAgent(agent)
            val next = Engine(key, builder.build())
            current?.let { retire(it) }
            current = next
            next.active.incrementAndGet()
            return next
        }
    }

    /** Shuts [e] down now, or when its last request has finished. */
    private fun retire(e: Engine) {
        e.retired = true
        if (e.active.get() == 0) e.engine.shutdown()
    }

    private fun release(e: Engine) {
        if (e.active.decrementAndGet() == 0 && e.retired) e.engine.shutdown()
    }

    private fun onMain(block: () -> Unit) {
        main.post(block)
    }

    /** One request: Cronet's callbacks on one side, Dart's pulls on the other. */
    private inner class Exchange(
        private val engine: Engine,
        private val follow: Boolean,
        private val maxRedirects: Int,
        private var opened: MethodChannel.Result?,
    ) : UrlRequest.Callback() {
        var id = 0
        var request: UrlRequest? = null
        private val buffer = ByteBuffer.allocateDirect(CHUNK)
        private var redirects = 0
        private var pending: MethodChannel.Result? = null
        private var finished = false
        private var failure: CronetException? = null
        private var released = false

        @Synchronized
        fun read(result: MethodChannel.Result) {
            when {
                failure != null -> fail(result, failure!!)
                finished -> onMain { result.success(null) }
                pending != null -> onMain { result.error(HTTP, "busy", null) }
                else -> {
                    pending = result
                    buffer.clear()
                    request!!.read(buffer)
                }
            }
        }

        fun cancel() {
            request?.cancel()
        }

        override fun onRedirectReceived(request: UrlRequest, info: UrlResponseInfo, newLocationUrl: String) {
            if (follow && ++redirects <= maxRedirects) {
                request.followRedirect()
            } else {
                // Deliver the redirect itself as the response.
                answerOpen(info)
                synchronized(this) { finished = true }
                request.cancel()
            }
        }

        override fun onResponseStarted(request: UrlRequest, info: UrlResponseInfo) {
            answerOpen(info)
        }

        override fun onReadCompleted(request: UrlRequest, info: UrlResponseInfo, byteBuffer: ByteBuffer) {
            byteBuffer.flip()
            val chunk = ByteArray(byteBuffer.remaining())
            byteBuffer.get(chunk)
            val to = synchronized(this) { pending.also { pending = null } }
            if (to != null) onMain { to.success(chunk) }
        }

        override fun onSucceeded(request: UrlRequest, info: UrlResponseInfo) {
            val to = synchronized(this) { finished = true; pending.also { pending = null } }
            if (to != null) onMain { to.success(null) }
            done()
        }

        override fun onFailed(request: UrlRequest, info: UrlResponseInfo?, error: CronetException) {
            val open = opened
            val to = synchronized(this) { failure = error; pending.also { pending = null } }
            opened = null
            (open ?: to)?.let { fail(it, error) }
            done()
        }

        override fun onCanceled(request: UrlRequest, info: UrlResponseInfo?) {
            val to = synchronized(this) { finished = true; pending.also { pending = null } }
            if (to != null) onMain { to.success(null) }
            done()
        }

        private fun answerOpen(info: UrlResponseInfo) {
            val to = opened ?: return
            opened = null
            val headers = LinkedHashMap<String, String>()
            for ((k, v) in info.allHeadersAsList) {
                val name = k.lowercase()
                headers[name] = headers[name]?.let { "$it, $v" } ?: v
            }
            val answer = mapOf("id" to id, "status" to info.httpStatusCode, "headers" to headers)
            onMain { to.success(answer) }
        }

        private fun fail(to: MethodChannel.Result, error: CronetException) {
            // Cronet's ERR_SSL_PINNED_KEY_NOT_IN_CERT_CHAIN.
            val pin = error is NetworkException && error.cronetInternalErrorCode == PINNED_KEY_NOT_IN_CHAIN
            val code = if (pin) PIN_MISMATCH else HTTP
            val detail = if (error is NetworkException) "${error.cronetInternalErrorCode}" else "cronet"
            onMain { to.error(code, detail, null) }
        }

        @Synchronized
        private fun done() {
            if (released) return
            released = true
            requests.remove(id)
            release(engine)
        }
    }

    private companion object {
        const val HTTP = "PLUX_HTTP"
        const val PIN_MISMATCH = "PLUX_PIN_MISMATCH"
        const val PINNED_KEY_NOT_IN_CHAIN = -150
        const val CHUNK = 32 * 1024
        const val FAR_FUTURE = 253402300799000L // 9999-12-31
    }
}
