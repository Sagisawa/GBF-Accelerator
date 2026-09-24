package com.sagisawa.gbfaccelerator.xposed

import android.util.Log
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.ProxyConfig
import androidx.webkit.ProxyController
import androidx.webkit.WebViewFeature
import io.github.libxposed.api.XposedModule
import io.github.libxposed.api.XposedModuleInterface.ModuleLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageReadyParam
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

class SkyLeapModule : XposedModule() {

    companion object {
        private const val TAG = "GBF-ACC"
        private val TARGET_PACKAGES = setOf(
            "com.dena.skyleap"
        )
    }

    private var currentProcessName: String = "unknown"
    private val isProxySetupStarted = AtomicBoolean(false)
    private val isProxyReady = AtomicBoolean(false)

    override fun onModuleLoaded(param: ModuleLoadedParam) {
        super.onModuleLoaded(param)
        currentProcessName = param.processName

        Log.i(TAG, "==================================================")
        Log.i(TAG, "[GBF-ACC] attachFramework called")
        val fwName = runCatching { frameworkName }.getOrDefault("unknown")
        val fwVer = runCatching { frameworkVersion }.getOrDefault("unknown")
        val fwCode = runCatching { frameworkVersionCode }.getOrDefault(-1L)
        val apiVer = runCatching { apiVersion }.getOrDefault(102)
        Log.i(TAG, "[GBF-ACC] framework bound (name: $fwName, ver: $fwVer ($fwCode), api: $apiVer)")
        Log.i(TAG, "[GBF-ACC] onModuleLoaded (process: $currentProcessName)")
        Log.i(TAG, "==================================================")

        setupHooks()
    }

    override fun onPackageLoaded(param: PackageLoadedParam) {
        super.onPackageLoaded(param)
        if (param.packageName in TARGET_PACKAGES) {
            Log.i(TAG, "[GBF-ACC] SkyLeap package loaded: ${param.packageName}")
        }
    }

    override fun onPackageReady(param: PackageReadyParam) {
        super.onPackageReady(param)
        if (param.packageName in TARGET_PACKAGES) {
            Log.i(TAG, "[GBF-ACC] SkyLeap package ready: ${param.packageName}")
        }
    }

    private fun setupHooks() {
        Log.i(TAG, "[GBF-ACC] setupHooks entered")

        // Hook 1: WebView.setWebViewClient(WebViewClient)
        try {
            val setClientMethod = WebView::class.java.getMethod("setWebViewClient", WebViewClient::class.java)
            hook(setClientMethod).intercept { chain ->
                val view = chain.thisObject as? WebView
                if (view != null) {
                    setupProxyOverride(view)
                }
                chain.proceed()
            }
            Log.i(TAG, "[GBF-ACC] WebView hook installed: setWebViewClient")
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook WebView.setWebViewClient: ${e.message}", e)
        }

        // Hook 2: WebView.loadUrl(String)
        try {
            val loadUrlMethod = WebView::class.java.getMethod("loadUrl", String::class.java)
            hook(loadUrlMethod).intercept { chain ->
                val view = chain.thisObject as? WebView
                if (view != null) {
                    setupProxyOverride(view)
                }
                chain.proceed()
            }
            Log.i(TAG, "[GBF-ACC] WebView hook installed: loadUrl")
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook WebView.loadUrl: ${e.message}", e)
        }
    }

    private fun setupProxyOverride(view: WebView) {
        if (!isProxySetupStarted.compareAndSet(false, true)) {
            return
        }
        Log.i(TAG, "[GBF-ACC] setupProxyOverride entered")

        val isProxyOverrideSupported = runCatching {
            WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)
        }.getOrDefault(false)

        val isReverseBypassSupported = runCatching {
            WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE_REVERSE_BYPASS)
        }.getOrDefault(false)

        Log.i(TAG, "[GBF-ACC][Proxy] Feature support: PROXY_OVERRIDE=$isProxyOverrideSupported, REVERSE_BYPASS=$isReverseBypassSupported")

        if (!isProxyOverrideSupported) {
            Log.w(TAG, "[GBF-ACC][Proxy] PROXY_OVERRIDE not supported!")
            return
        }

        try {
            val builder = ProxyConfig.Builder()
                .addProxyRule("http://127.0.0.1:8124")

            if (isReverseBypassSupported) {
                builder.addBypassRule("gbf.game.mbga.jp")
                builder.addBypassRule("*.granbluefantasy.jp")
                builder.addBypassRule("prd-game-a-gbf.akamaized.net")
                builder.setReverseBypassEnabled(true)
                Log.i(TAG, "[GBF-ACC][Proxy] Reverse Bypass configured: Only GBF domains -> 127.0.0.1:8124, rest DIRECT")
            } else {
                builder.addBypassRule("<local>")
                Log.i(TAG, "[GBF-ACC][Proxy] Standard Bypass configured (127.0.0.1:8124)")
            }

            val proxyConfig = builder.build()
            val executor = Executors.newSingleThreadExecutor()

            ProxyController.getInstance().setProxyOverride(proxyConfig, executor) {
                Log.i(TAG, "[GBF-ACC] ProxyController configured (Reverse Bypass active)")
                isProxyReady.set(true)
            }
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC][Proxy] Failed to configure ProxyController: ${e.message}", e)
        }
    }
}
