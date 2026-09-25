package com.sagisawa.gbfaccelerator.browser

import android.content.Context
import android.util.Log
import android.view.View
import com.sagisawa.gbfaccelerator.core.EmbeddedCoreManager

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
        return "*" in targetPackages || packageName in targetPackages
    }

    override fun matchesProcess(processName: String): Boolean {
        if ("*" in targetPackages) return true
        return targetPackages.any { pkg ->
            processName == pkg || processName.startsWith("$pkg:")
        }
    }

    override fun onModuleLoaded(processName: String, hookRegistry: HookRegistry) {
        installApplicationHooks(hookRegistry)
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
     * Installs lifecycle hooks on Application class to capture Context and ensure Go Core is started early.
     */
    open fun installApplicationHooks(hookRegistry: HookRegistry, classLoader: ClassLoader = javaClass.classLoader ?: ClassLoader.getSystemClassLoader()) {
        try {
            val appClass = Class.forName("android.app.Application", true, classLoader)
            val contextClass = Class.forName("android.content.Context", true, classLoader)
            val attachBaseContextMethod = appClass.getDeclaredMethod("attachBaseContext", contextClass)
            val success = hookRegistry.hookMethod(attachBaseContextMethod) { thisObj, args ->
                val ctx = args.firstOrNull() as? Context ?: (thisObj as? Context)
                ctx?.let { onContextAvailable(it) }
                false
            }
            if (success) {
                Log.i(TAG, "[GBF-ACC] Application.attachBaseContext hook installed")
            }
        } catch (_: Throwable) {
        }

        try {
            val appClass = Class.forName("android.app.Application", true, classLoader)
            val onCreateMethod = appClass.getMethod("onCreate")
            val success = hookRegistry.hookMethod(onCreateMethod) { thisObj, _ ->
                (thisObj as? Context)?.let { onContextAvailable(it) }
                false
            }
            if (success) {
                Log.i(TAG, "[GBF-ACC] Application.onCreate hook installed")
            }
        } catch (_: Throwable) {
        }
    }

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
        if (webViewObj is View) {
            onContextAvailable(webViewObj.context)
        } else if (webViewObj != null) {
            try {
                val getContextMethod = webViewObj.javaClass.getMethod("getContext")
                val ctx = getContextMethod.invoke(webViewObj) as? Context
                ctx?.let { onContextAvailable(it) }
            } catch (_: Throwable) {
            }
        }

        proxyConfigurator.applyProxyConfig { success, err ->
            if (success) {
                Log.i(TAG, "[GBF-ACC] ProxyController configured (Reverse Bypass active)")
            } else {
                Log.e(TAG, "[GBF-ACC][Proxy] Failed to configure ProxyController: ${err?.message}", err)
            }
        }
    }

    /**
     * Invoked whenever an Android Context is obtained to ensure the embedded Go Core is active.
     */
    open fun onContextAvailable(context: Context) {
        EmbeddedCoreManager.ensureStarted(context)
    }
}
