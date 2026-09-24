package com.sagisawa.gbfaccelerator.browser

import android.util.Log

/**
 * Base adapter strictly for Android browsers that rely on the standard Android WebView (android.webkit.WebView)
 * for rendering and networking.
 *
 * IMPORTANT ARCHITECTURAL CONSTRAINT:
 * This class is NOT suitable for standalone Chromium-based browsers (such as official Google Chrome, Kiwi, Brave),
 * as those browsers do not use the Android system WebView, but rather bundle their own custom Chromium Content Shell
 * and Cronet native network stack. Any future support for standalone Chromium browsers must implement BrowserAdapter
 * directly according to their specific network architecture.
 *
 * Encapsulates:
 * - WebViewClient / loadUrl lifecycle hook installation via reflection
 * - Single-instance ProxyController reverse-bypass configuration
 * - Process and package identification logic
 */
abstract class WebViewBrowserAdapter(
    override val id: String,
    override val name: String,
    override val targetPackages: Set<String>,
    val proxyConfigurator: ProxyConfigurator = ProxyConfigurator.DEFAULT
) : BrowserAdapter {

    companion object {
        const val TAG = "GBF-ACC"
    }

    override fun matchesPackage(packageName: String): Boolean {
        return packageName in targetPackages
    }

    override fun matchesProcess(processName: String): Boolean {
        return targetPackages.any { pkg ->
            processName == pkg || processName.startsWith("$pkg:")
        }
    }

    override fun onModuleLoaded(processName: String, hookRegistry: HookRegistry) {
        installWebViewHooks(hookRegistry)
    }

    override fun onPackageLoaded(packageName: String) {
        if (matchesPackage(packageName)) {
            Log.i(TAG, "[$name] Package loaded: $packageName")
        }
    }

    override fun onPackageReady(packageName: String) {
        if (matchesPackage(packageName)) {
            Log.i(TAG, "[$name] Package ready: $packageName")
        }
    }

    /**
     * Installs hooks on standard WebView methods using reflection.
     * Hooks:
     * 1. WebView.setWebViewClient(WebViewClient)
     * 2. WebView.loadUrl(String)
     */
    open fun installWebViewHooks(hookRegistry: HookRegistry, classLoader: ClassLoader = javaClass.classLoader ?: ClassLoader.getSystemClassLoader()) {
        // Hook 1: WebView.setWebViewClient(WebViewClient)
        try {
            val webViewClass = Class.forName("android.webkit.WebView", true, classLoader)
            val webViewClientClass = Class.forName("android.webkit.WebViewClient", true, classLoader)
            val setClientMethod = webViewClass.getMethod("setWebViewClient", webViewClientClass)

            val success = hookRegistry.hookMethod(setClientMethod) { thisObj, _ ->
                triggerProxyConfig(thisObj)
            }
            if (success) {
                Log.i(TAG, "[GBF-ACC] WebView hook installed: setWebViewClient")
            }
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook WebView.setWebViewClient: ${e.message}", e)
        }

        // Hook 2: WebView.loadUrl(String)
        try {
            val webViewClass = Class.forName("android.webkit.WebView", true, classLoader)
            val loadUrlMethod = webViewClass.getMethod("loadUrl", String::class.java)

            val success = hookRegistry.hookMethod(loadUrlMethod) { thisObj, _ ->
                triggerProxyConfig(thisObj)
            }
            if (success) {
                Log.i(TAG, "[GBF-ACC] WebView hook installed: loadUrl")
            }
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook WebView.loadUrl: ${e.message}", e)
        }
    }

    /**
     * Invoked when any hooked WebView activity is detected.
     */
    protected open fun triggerProxyConfig(webViewObj: Any?) {
        proxyConfigurator.applyProxyConfig { success, err ->
            if (success) {
                Log.i(TAG, "[GBF-ACC] ProxyController configured (Reverse Bypass active)")
            } else {
                Log.e(TAG, "[GBF-ACC][Proxy] Failed to configure ProxyController: ${err?.message}", err)
            }
        }
    }
}
