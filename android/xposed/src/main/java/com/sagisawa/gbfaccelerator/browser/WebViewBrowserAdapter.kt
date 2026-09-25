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

    private val hookedClientClasses = java.util.Collections.synchronizedSet(mutableSetOf<Class<*>>())

    /**
     * Installs hooks on standard WebView methods using reflection.
     * Hooks:
     * 1. WebViewClient.onReceivedSslError(WebView, SslErrorHandler, SslError) on base WebViewClient
     * 2. WebView.setWebViewClient(WebViewClient)
     * 3. WebView.loadUrl(String)
     */
    open fun installWebViewHooks(hookRegistry: HookRegistry, classLoader: ClassLoader = javaClass.classLoader ?: ClassLoader.getSystemClassLoader()) {
        // Hook 1: Base WebViewClient.onReceivedSslError
        try {
            val webViewClass = Class.forName("android.webkit.WebView", true, classLoader)
            val sslHandlerClass = Class.forName("android.webkit.SslErrorHandler", true, classLoader)
            val sslErrorClass = Class.forName("android.net.http.SslError", true, classLoader)
            val webViewClientClass = Class.forName("android.webkit.WebViewClient", true, classLoader)

            val onReceivedSslErrorMethod = webViewClientClass.getMethod(
                "onReceivedSslError",
                webViewClass,
                sslHandlerClass,
                sslErrorClass
            )
            val success = hookRegistry.hookMethod(onReceivedSslErrorMethod) { _, args ->
                handleSslError(args)
            }
            if (success) {
                Log.i(TAG, "[GBF-ACC] WebView hook installed: base WebViewClient.onReceivedSslError")
            }
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook base WebViewClient.onReceivedSslError: ${e.message}", e)
        }

        // Hook 2: WebView.setWebViewClient(WebViewClient)
        try {
            val webViewClass = Class.forName("android.webkit.WebView", true, classLoader)
            val webViewClientClass = Class.forName("android.webkit.WebViewClient", true, classLoader)
            val setClientMethod = webViewClass.getMethod("setWebViewClient", webViewClientClass)

            val success = hookRegistry.hookMethod(setClientMethod) { thisObj, args ->
                triggerProxyConfig(thisObj)
                val client = args.firstOrNull()
                if (client != null && client.javaClass != webViewClientClass) {
                    hookCustomClientSslError(client.javaClass, hookRegistry, classLoader)
                }
                false
            }
            if (success) {
                Log.i(TAG, "[GBF-ACC] WebView hook installed: setWebViewClient")
            }
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook WebView.setWebViewClient: ${e.message}", e)
        }

        // Hook 3: WebView.loadUrl(String)
        try {
            val webViewClass = Class.forName("android.webkit.WebView", true, classLoader)
            val loadUrlMethod = webViewClass.getMethod("loadUrl", String::class.java)

            val success = hookRegistry.hookMethod(loadUrlMethod) { thisObj, _ ->
                triggerProxyConfig(thisObj)
                false
            }
            if (success) {
                Log.i(TAG, "[GBF-ACC] WebView hook installed: loadUrl")
            }
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook WebView.loadUrl: ${e.message}", e)
        }
    }

    private fun hookCustomClientSslError(clientClass: Class<*>, hookRegistry: HookRegistry, classLoader: ClassLoader) {
        if (!hookedClientClasses.add(clientClass)) return
        try {
            val webViewClass = Class.forName("android.webkit.WebView", true, classLoader)
            val sslHandlerClass = Class.forName("android.webkit.SslErrorHandler", true, classLoader)
            val sslErrorClass = Class.forName("android.net.http.SslError", true, classLoader)

            val method = clientClass.getDeclaredMethod("onReceivedSslError", webViewClass, sslHandlerClass, sslErrorClass)
            val success = hookRegistry.hookMethod(method) { _, args ->
                handleSslError(args)
            }
            if (success) {
                Log.i(TAG, "[GBF-ACC] Successfully hooked onReceivedSslError on custom class: ${clientClass.name}")
            }
        } catch (_: NoSuchMethodException) {
            // Class did not override onReceivedSslError, base WebViewClient hook will handle it
        } catch (e: Throwable) {
            Log.w(TAG, "[GBF-ACC] Could not hook onReceivedSslError on ${clientClass.name}: ${e.message}")
        }
    }

    /**
     * Inspects SSL error parameters.
     * If the destination URL belongs to Granblue Fantasy domains routed through the local proxy,
     * automatically calls SslErrorHandler.proceed() and returns true to bypass the error dialog/cancellation.
     */
    open fun handleSslError(args: List<Any?>): Boolean {
        val view = args.getOrNull(0)
        val handler = args.getOrNull(1)
        val error = args.getOrNull(2)

        val failingUrl = try {
            val getUrlMethod = error?.javaClass?.getMethod("getUrl")
            getUrlMethod?.invoke(error) as? String
        } catch (_: Throwable) {
            null
        } ?: try {
            val getUrlMethod = view?.javaClass?.getMethod("getUrl")
            getUrlMethod?.invoke(view) as? String
        } catch (_: Throwable) {
            null
        } ?: ""

        val isOurCaCert = try {
            val certMethod = error?.javaClass?.getMethod("getCertificate")
            val cert = certMethod?.invoke(error)
            val getIssuedByMethod = cert?.javaClass?.getMethod("getIssuedBy")
            val issuedBy = getIssuedByMethod?.invoke(cert)
            val getONameMethod = issuedBy?.javaClass?.getMethod("getOName")
            val oName = getONameMethod?.invoke(issuedBy) as? String
            val getCNameMethod = issuedBy?.javaClass?.getMethod("getCName")
            val cName = getCNameMethod?.invoke(issuedBy) as? String
            oName == "GBF Local Accelerator" || cName == "GBF Local Accelerator Root CA"
        } catch (_: Throwable) {
            false
        }

        if (isOurCaCert || (failingUrl.isNotEmpty() && GbfRoutingRules.shouldProxy(failingUrl))) {
            Log.i(TAG, "[GBF-ACC] Auto-accepting SSL error for GBF proxy (isOurCaCert=$isOurCaCert): $failingUrl")
            return try {
                val proceedMethod = handler?.javaClass?.getMethod("proceed")
                proceedMethod?.invoke(handler)
                true
            } catch (e: Throwable) {
                Log.e(TAG, "[GBF-ACC] Failed to call SslErrorHandler.proceed(): ${e.message}", e)
                false
            }
        }
        return false
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
