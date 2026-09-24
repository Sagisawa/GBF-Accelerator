package com.sagisawa.gbfaccelerator.xposed

import android.os.Build
import android.util.Log
import android.webkit.SslErrorHandler
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.ProxyConfig
import androidx.webkit.ProxyController
import androidx.webkit.WebViewFeature
import io.github.libxposed.api.XposedInterface
import io.github.libxposed.api.XposedModule
import io.github.libxposed.api.XposedModuleInterface.ModuleLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageReadyParam
import java.lang.reflect.Method
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

class SkyLeapModule : XposedModule {

    companion object {
        private const val TAG = "GBF-ACC"
        private val TARGET_PACKAGES = setOf(
            "com.dena.skyleap",
            "com.dena.skyleap2"
        )
    }

    private var currentProcessName: String = "unknown"
    private var isModuleInitialized = false
    private var baseInterface: XposedInterface? = null

    // Proxy state flags
    private val isProxySetupStarted = AtomicBoolean(false)
    private val isProxyReady = AtomicBoolean(false)

    // Request metrics for Phase 3 verification
    private val staticCdnCount = AtomicInteger(0)
    private val dynamicApiCount = AtomicInteger(0)
    private val documentCount = AtomicInteger(0)
    private val otherCount = AtomicInteger(0)

    private val hookedClientClasses = ConcurrentHashMap.newKeySet<String>()
    private val hookedChromiumLoaders = ConcurrentHashMap.newKeySet<Int>()

    // Default constructor (required by libxposed modern API 102+)
    constructor() : super()

    // 2-argument constructor (required by LSPosed 1.9.x / API 100-101 reflection)
    @Suppress("UNUSED_PARAMETER")
    constructor(base: XposedInterface, param: ModuleLoadedParam) : super() {
        this.baseInterface = base
        var clazz: Class<*>? = this.javaClass.superclass
        while (clazz != null && clazz != Any::class.java) {
            for (field in clazz.declaredFields) {
                if (XposedInterface::class.java.isAssignableFrom(field.type)) {
                    try {
                        field.isAccessible = true
                        field.set(this, base)
                    } catch (_: Throwable) {}
                }
            }
            clazz = clazz.superclass
        }
        initModule(base, param)
    }

    override fun onModuleLoaded(param: ModuleLoadedParam) {
        super.onModuleLoaded(param)
        initModule(null, param)
    }

    private fun initModule(base: XposedInterface?, param: ModuleLoadedParam) {
        if (isModuleInitialized) return
        isModuleInitialized = true
        currentProcessName = param.processName

        val activeBase = base ?: baseInterface
        val fwName = runCatching { activeBase?.frameworkName ?: frameworkName }.getOrDefault("LSPosed")
        val fwVer = runCatching { activeBase?.frameworkVersion ?: frameworkVersion }.getOrDefault("unknown")
        val fwCode = runCatching { activeBase?.frameworkVersionCode ?: frameworkVersionCode }.getOrDefault(-1L)
        val apiVer = runCatching { activeBase?.apiVersion ?: apiVersion }.getOrDefault(100)

        Log.i(TAG, "==================================================")
        Log.i(TAG, "[GBF-ACC] Phase 3 Proxy Verification Module Initialized!")
        Log.i(TAG, "[GBF-ACC] Framework name : $fwName")
        Log.i(TAG, "[GBF-ACC] Framework ver  : $fwVer ($fwCode)")
        Log.i(TAG, "[GBF-ACC] API Version    : $apiVer")
        Log.i(TAG, "[GBF-ACC] Process name   : $currentProcessName")
        Log.i(TAG, "==================================================")

        activeBase?.let { xposed ->
            setupHooks(xposed)
        }
    }

    private fun setupHooks(base: XposedInterface) {
        NetworkProbeHookers.setListener(object : NetworkProbeHookers.InterceptListener {
            override fun onSetWebViewClient(view: WebView, client: WebViewClient) {
                handleSetWebViewClient(base, view, client)
            }

            override fun onLoadUrl(view: WebView, url: String) {
                Log.i(TAG, "[GBF-ACC][WebView-LoadUrl] url=$url")
            }

            override fun onShouldInterceptRequest(view: WebView, request: WebResourceRequest) {
                handleInterceptedRequest(view, request)
            }

            override fun onOkHttpRequest(method: String, url: String) {}
            override fun onHttpUrlConnection(method: String, url: String) {}
        })

        val hookMethod = base.javaClass.getMethod("hook", Method::class.java, Class::class.java)

        // 1. Hook WebView.setWebViewClient(WebViewClient)
        try {
            val setClientMethod = WebView::class.java.getMethod("setWebViewClient", WebViewClient::class.java)
            hookMethod.invoke(base, setClientMethod, NetworkProbeHookers.SetWebViewClientHooker::class.java)
            Log.i(TAG, "[GBF-ACC] Hooked WebView.setWebViewClient(WebViewClient)")
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook WebView.setWebViewClient: ${e.message}")
        }

        // 2. Hook WebView.loadUrl(String)
        try {
            val loadUrlMethod = WebView::class.java.getMethod("loadUrl", String::class.java)
            hookMethod.invoke(base, loadUrlMethod, NetworkProbeHookers.LoadUrlHooker::class.java)
            Log.i(TAG, "[GBF-ACC] Hooked WebView.loadUrl(String)")
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook WebView.loadUrl: ${e.message}")
        }

        // 3. Hook WebViewClient.shouldInterceptRequest(WebView, WebResourceRequest) base method
        try {
            val interceptMethod = WebViewClient::class.java.getMethod(
                "shouldInterceptRequest",
                WebView::class.java,
                WebResourceRequest::class.java
            )
            hookMethod.invoke(base, interceptMethod, NetworkProbeHookers.ShouldInterceptRequestHooker::class.java)
            Log.i(TAG, "[GBF-ACC] Hooked WebViewClient.shouldInterceptRequest")
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook base shouldInterceptRequest: ${e.message}")
        }

        // 4. Hook WebViewClient.onReceivedSslError(WebView, SslErrorHandler, SslError)
        try {
            val sslMethod = WebViewClient::class.java.getMethod(
                "onReceivedSslError",
                WebView::class.java,
                SslErrorHandler::class.java,
                android.net.http.SslError::class.java
            )
            hookMethod.invoke(base, sslMethod, NetworkProbeHookers.OnReceivedSslErrorHooker::class.java)
            Log.i(TAG, "[GBF-ACC] Hooked WebViewClient.onReceivedSslError")
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC] Failed to hook onReceivedSslError: ${e.message}")
        }
    }

    private fun handleSetWebViewClient(base: XposedInterface, view: WebView, client: WebViewClient) {
        val clientClass = client.javaClass
        val className = clientClass.name

        // Inspect WebView and Chromium details
        runCatching {
            val ua = view.settings.userAgentString
            val webViewPackage = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                WebView.getCurrentWebViewPackage()?.let { "${it.packageName} v${it.versionName} (${it.versionCode})" } ?: "unknown"
            } else "N/A"

            Log.i(TAG, "--------------------------------------------------")
            Log.i(TAG, "[GBF-ACC][WebView-Info] Instance     : $view")
            Log.i(TAG, "[GBF-ACC][WebView-Info] Client Class : $className (superclass: ${clientClass.superclass?.name})")
            Log.i(TAG, "[GBF-ACC][WebView-Info] User-Agent   : $ua")
            Log.i(TAG, "[GBF-ACC][WebView-Info] Chromium/Pkg : $webViewPackage")
            Log.i(TAG, "--------------------------------------------------")
        }

        // Hook subclass onReceivedSslError if overridden
        if (hookedClientClasses.add(className) && clientClass != WebViewClient::class.java) {
            try {
                val sslMethod = clientClass.getDeclaredMethod(
                    "onReceivedSslError",
                    WebView::class.java,
                    SslErrorHandler::class.java,
                    android.net.http.SslError::class.java
                )
                val hookMethod = base.javaClass.getMethod("hook", Method::class.java, Class::class.java)
                hookMethod.invoke(base, sslMethod, NetworkProbeHookers.OnReceivedSslErrorHooker::class.java)
                Log.i(TAG, "[GBF-ACC] Hooked subclass onReceivedSslError on: $className")
            } catch (_: NoSuchMethodException) {}
        }

        // Try to hook Chromium native cert verifier in WebView's classloader
        hookChromiumCertVerifierIfPresent(base, view.javaClass.classLoader)

        // Setup AndroidX ProxyController override
        setupProxyOverride(view)
    }

    private fun hookChromiumCertVerifierIfPresent(base: XposedInterface, classLoader: ClassLoader?) {
        if (classLoader == null) return
        val loaderHash = System.identityHashCode(classLoader)
        if (!hookedChromiumLoaders.add(loaderHash)) return

        try {
            val networkLibraryClass = classLoader.loadClass("org.chromium.net.AndroidNetworkLibrary")
            val verifyMethod = networkLibraryClass.getMethod(
                "verifyServerCertificates",
                Array<ByteArray>::class.java,
                String::class.java,
                String::class.java
            )
            val hookMethod = base.javaClass.getMethod("hook", Method::class.java, Class::class.java)
            hookMethod.invoke(base, verifyMethod, NetworkProbeHookers.VerifyServerCertificatesHooker::class.java)
            Log.i(TAG, "[GBF-ACC][Chromium-SSL] Hooked AndroidNetworkLibrary.verifyServerCertificates successfully!")
        } catch (e: ClassNotFoundException) {
            Log.d(TAG, "[GBF-ACC][Chromium-SSL] AndroidNetworkLibrary not in this classloader")
        } catch (e: Throwable) {
            Log.d(TAG, "[GBF-ACC][Chromium-SSL] Failed to hook AndroidNetworkLibrary: ${e.message}")
        }

        try {
            val x509UtilClass = classLoader.loadClass("org.chromium.net.X509Util")
            val verifyMethod = x509UtilClass.getMethod(
                "verifyServerCertificates",
                Array<ByteArray>::class.java,
                String::class.java,
                String::class.java
            )
            val hookMethod = base.javaClass.getMethod("hook", Method::class.java, Class::class.java)
            hookMethod.invoke(base, verifyMethod, NetworkProbeHookers.VerifyServerCertificatesHooker::class.java)
            Log.i(TAG, "[GBF-ACC][Chromium-SSL] Hooked X509Util.verifyServerCertificates successfully!")
        } catch (_: Throwable) {}
    }

    private fun setupProxyOverride(view: WebView) {
        if (!isProxySetupStarted.compareAndSet(false, true)) {
            return
        }

        val isProxyOverrideSupported = runCatching {
            WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)
        }.getOrDefault(false)

        val isReverseBypassSupported = runCatching {
            WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE_REVERSE_BYPASS)
        }.getOrDefault(false)

        Log.i(TAG, "==================================================")
        Log.i(TAG, "[GBF-ACC][Proxy] Checking ProxyController Feature Support:")
        Log.i(TAG, "  -> PROXY_OVERRIDE supported: $isProxyOverrideSupported")
        Log.i(TAG, "  -> REVERSE_BYPASS supported: $isReverseBypassSupported")
        Log.i(TAG, "==================================================")

        if (!isProxyOverrideSupported) {
            Log.w(TAG, "[GBF-ACC][Proxy] PROXY_OVERRIDE not supported, keeping DIRECT mode!")
            return
        }

        try {
            val builder = ProxyConfig.Builder()
                .addProxyRule("http://127.0.0.1:8124")

            if (isReverseBypassSupported) {
                // Requirement 7: Only test GBF related domains
                builder.addBypassRule("gbf.game.mbga.jp")
                builder.addBypassRule("*.granbluefantasy.jp")
                builder.addBypassRule("prd-game-a-gbf.akamaized.net")
                builder.setReverseBypassEnabled(true)
                Log.i(TAG, "[GBF-ACC][Proxy] Configured Reverse Bypass: Only GBF domains -> 127.0.0.1:8124, rest DIRECT")
            } else {
                builder.addBypassRule("<local>")
                Log.i(TAG, "[GBF-ACC][Proxy] Standard Bypass configured (127.0.0.1:8124)")
            }

            val proxyConfig = builder.build()
            val executor = Executors.newSingleThreadExecutor()

            ProxyController.getInstance().setProxyOverride(proxyConfig, executor) {
                Log.i(TAG, "==================================================")
                Log.i(TAG, "[GBF-ACC][Proxy] >>> Proxy override callback completed! <<<")
                Log.i(TAG, "[GBF-ACC][Proxy] Target: 127.0.0.1:8124 active for GBF traffic")
                Log.i(TAG, "==================================================")
                isProxyReady.set(true)

                // Requirement 17: wait for callback to complete, then reload page
                view.post {
                    Log.i(TAG, "[GBF-ACC][Proxy] Reloading WebView to ensure new proxy is active for all connections")
                    view.reload()
                }
            }
        } catch (e: Throwable) {
            Log.e(TAG, "[GBF-ACC][Proxy] Error setting proxy override: ${e.message}", e)
            // Requirement 18: Fallback to direct on failure
            runCatching {
                ProxyController.getInstance().clearProxyOverride(Executors.newSingleThreadExecutor()) {
                    Log.i(TAG, "[GBF-ACC][Proxy] Successfully cleared proxy override (DIRECT mode)")
                }
            }
        }
    }

    private fun handleInterceptedRequest(view: WebView, request: WebResourceRequest) {
        val url = request.url.toString()
        val method = request.method
        val isMainFrame = request.isForMainFrame
        val headerKeys = request.requestHeaders?.keys?.sorted()?.joinToString(",") ?: ""

        val (category, description) = classifyUrl(url)

        val total: Int = when (category) {
            "GBF_STATIC_CDN" -> staticCdnCount.incrementAndGet()
            "GBF_DYNAMIC_API" -> dynamicApiCount.incrementAndGet()
            "GBF_DOCUMENT" -> documentCount.incrementAndGet()
            else -> otherCount.incrementAndGet()
        }

        val proxyStatus = if (isProxyReady.get()) "PROXY:8124" else "DIRECT"

        if (category.startsWith("GBF")) {
            Log.i(TAG, "[GBF-ACC][Request #$total][$proxyStatus] [$category] $method $url (mainFrame=$isMainFrame, headers=[$headerKeys], info=$description)")
        } else {
            Log.d(TAG, "[GBF-ACC][Request #$total][$proxyStatus] [$category] $method $url")
        }
    }

    private fun classifyUrl(url: String): Pair<String, String> {
        val uri = runCatching { android.net.Uri.parse(url) }.getOrNull()
        val host = uri?.host?.lowercase() ?: ""
        val path = uri?.path ?: ""

        val isGbfDynamicHost = host == "game.granbluefantasy.jp" || host == "gbf.game.mbga.jp"
        return when {
            (host.startsWith("game-a") && host.endsWith("granbluefantasy.jp")) || host.contains("akamaized.net") -> {
                "GBF_STATIC_CDN" to "Official Game/Akamai CDN host ($host)"
            }
            url.contains("/assets_") || url.contains("/sound/") || url.contains("/css/") || url.contains("/js/") -> {
                "GBF_STATIC_CDN" to "Static asset path"
            }
            url.endsWith(".js") || url.endsWith(".css") || url.endsWith(".png") || url.endsWith(".jpg") ||
            url.endsWith(".jpeg") || url.endsWith(".gif") || url.endsWith(".webp") || url.endsWith(".mp4") ||
            url.endsWith(".wasm") || url.endsWith(".mp3") || url.endsWith(".ogg") -> {
                "GBF_STATIC_CDN" to "Static asset extension"
            }

            isGbfDynamicHost && (
                path.startsWith("/rest/") || path.startsWith("/quest/") || path.startsWith("/party/") ||
                path.startsWith("/user/") || path.startsWith("/deck/") || path.startsWith("/gacha/") ||
                path.startsWith("/casino/") || path.startsWith("/mypage/") || path.startsWith("/ob/r") ||
                path.startsWith("/od/query") || path.startsWith("/rest/error/js") || path.startsWith("/present/") ||
                path.startsWith("/item/") || path.startsWith("/guild/") || path.startsWith("/profile/") ||
                path.startsWith("/comic/") || path.startsWith("/skyscope/")
            ) -> {
                "GBF_DYNAMIC_API" to "GBF Dynamic API path ($path on $host)"
            }

            isGbfDynamicHost -> {
                "GBF_DOCUMENT" to "GBF Game Page document ($path on $host)"
            }

            else -> {
                "OTHER" to "Non-GBF host ($host)"
            }
        }
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
}
