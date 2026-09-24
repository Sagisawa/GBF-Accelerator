package com.sagisawa.gbfaccelerator.browser

import android.util.Log
import androidx.webkit.ProxyConfig
import androidx.webkit.ProxyController
import androidx.webkit.WebViewFeature
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

enum class ProxyState {
    UNINITIALIZED,
    CONFIGURING,
    READY,
    FAILED
}

/**
 * Pluggable executor for applying proxy override to the underlying WebView runtime.
 * Allows pure unit testing without requiring an active Android device or mockito-dexmaker.
 */
interface ProxyOverrideExecutor {
    fun isSupported(): Boolean
    fun isReverseBypassSupported(): Boolean
    fun apply(
        proxyUrl: String,
        bypassRules: List<String>,
        reverseBypass: Boolean,
        onSuccess: () -> Unit,
        onError: (Throwable) -> Unit
    )
}

/**
 * Standard AndroidX WebKit ProxyController executor.
 */
class AndroidProxyOverrideExecutor : ProxyOverrideExecutor {

    companion object {
        private const val TAG = "GBF-ACC"
    }

    override fun isSupported(): Boolean {
        return runCatching {
            WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)
        }.getOrDefault(false)
    }

    override fun isReverseBypassSupported(): Boolean {
        return runCatching {
            WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE_REVERSE_BYPASS)
        }.getOrDefault(false)
    }

    override fun apply(
        proxyUrl: String,
        bypassRules: List<String>,
        reverseBypass: Boolean,
        onSuccess: () -> Unit,
        onError: (Throwable) -> Unit
    ) {
        val isProxyOverrideSupported = isSupported()
        val isReverseBypassSupported = isReverseBypassSupported()

        Log.i(
            TAG,
            "[GBF-ACC][Proxy] Feature support: PROXY_OVERRIDE=$isProxyOverrideSupported, REVERSE_BYPASS=$isReverseBypassSupported"
        )

        if (!isProxyOverrideSupported) {
            val err = UnsupportedOperationException("PROXY_OVERRIDE is not supported on this device/WebView")
            Log.w(TAG, "[GBF-ACC][Proxy] PROXY_OVERRIDE not supported!")
            onError(err)
            return
        }

        try {
            val builder = ProxyConfig.Builder()
                .addProxyRule(proxyUrl)

            if (reverseBypass && isReverseBypassSupported) {
                bypassRules.forEach { rule ->
                    builder.addBypassRule(rule)
                }
                builder.setReverseBypassEnabled(true)
                Log.i(
                    TAG,
                    "[GBF-ACC][Proxy] Reverse Bypass configured: GBF domains -> $proxyUrl, rest DIRECT"
                )
            } else {
                builder.addBypassRule("<local>")
                Log.i(TAG, "[GBF-ACC][Proxy] Standard Bypass configured ($proxyUrl)")
            }

            val proxyConfig = builder.build()
            val executor = Executors.newSingleThreadExecutor()

            ProxyController.getInstance().setProxyOverride(proxyConfig, executor) {
                Log.i(TAG, "[GBF-ACC] ProxyController configured (Reverse Bypass active)")
                onSuccess()
            }
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC][Proxy] Failed to configure ProxyController: ${e.message}", e)
            // Fail-Closed: Strictly DO NOT call clearProxyOverride() to fall back to DIRECT.
            onError(e)
        }
    }
}

/**
 * Configures WebView ProxyController with GBF Reverse Bypass rules.
 * Enforces Fail-Closed security: if configuration fails, traffic is blocked rather than silently routed DIRECT.
 */
class ProxyConfigurator(
    val proxyUrl: String = GbfRoutingRules.DEFAULT_PROXY_URL,
    val bypassRules: List<String> = GbfRoutingRules.GBF_BYPASS_RULES,
    val reverseBypass: Boolean = true,
    private val executor: ProxyOverrideExecutor = AndroidProxyOverrideExecutor()
) {
    companion object {
        private const val TAG = "GBF-ACC"
        val DEFAULT = ProxyConfigurator()
    }

    private val isProxySetupStarted = AtomicBoolean(false)
    private val state = AtomicReference(ProxyState.UNINITIALIZED)
    private val lastError = AtomicReference<Throwable?>(null)

    val currentState: ProxyState get() = state.get()
    val isReady: Boolean get() = state.get() == ProxyState.READY
    val isFailed: Boolean get() = state.get() == ProxyState.FAILED
    val failureError: Throwable? get() = lastError.get()

    /**
     * Applies proxy configuration atomically.
     * Guaranteed idempotent: subsequent calls while CONFIGURING or after READY are no-ops.
     */
    fun applyProxyConfig(onComplete: ((success: Boolean, error: Throwable?) -> Unit)? = null) {
        if (!isProxySetupStarted.compareAndSet(false, true)) {
            val current = state.get()
            onComplete?.invoke(current == ProxyState.READY, lastError.get())
            return
        }

        Log.i(TAG, "[GBF-ACC] setupProxyOverride entered")

        if (!executor.isSupported()) {
            val err = UnsupportedOperationException("PROXY_OVERRIDE is not supported on this device/WebView")
            Log.w(TAG, "[GBF-ACC][Proxy] PROXY_OVERRIDE not supported! Maintaining Fail-Closed.")
            state.set(ProxyState.FAILED)
            lastError.set(err)
            onComplete?.invoke(false, err)
            return
        }

        state.set(ProxyState.CONFIGURING)

        executor.apply(
            proxyUrl = proxyUrl,
            bypassRules = bypassRules,
            reverseBypass = reverseBypass,
            onSuccess = {
                state.set(ProxyState.READY)
                lastError.set(null)
                onComplete?.invoke(true, null)
            },
            onError = { error ->
                state.set(ProxyState.FAILED)
                lastError.set(error)
                // Fail-Closed: DO NOT clearProxyOverride()
                Log.e(TAG, "[GBF-ACC][Proxy] Configuration failed; maintaining Fail-Closed state", error)
                onComplete?.invoke(false, error)
            }
        )
    }

    /**
     * Resets configurator state (primarily for lifecycle or testing purposes).
     */
    fun reset() {
        isProxySetupStarted.set(false)
        state.set(ProxyState.UNINITIALIZED)
        lastError.set(null)
    }
}
