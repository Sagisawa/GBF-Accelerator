package com.sagisawa.gbfaccelerator.xposed

import android.util.Log
import com.sagisawa.gbfaccelerator.browser.BrowserAdapterRegistry
import com.sagisawa.gbfaccelerator.browser.HookInvocationCallback
import com.sagisawa.gbfaccelerator.browser.HookRegistry
import com.sagisawa.gbfaccelerator.browser.UniversalBrowserAdapter
import com.sagisawa.gbfaccelerator.core.EmbeddedCoreManager
import io.github.libxposed.api.XposedInterface
import io.github.libxposed.api.XposedInterfaceWrapper
import io.github.libxposed.api.XposedModule
import io.github.libxposed.api.XposedModuleInterface.ModuleLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageReadyParam
import java.lang.reflect.Method

/**
 * Xposed / LSPosed module entry point.
 * Defined in META-INF/xposed/java_init.list.
 * Delegates browser identification, lifecycle events, and proxy interception to BrowserAdapterRegistry.
 *
 * Implements LibXposed modern API 102 specification.
 */
class SkyLeapModule : XposedModule {

    private var xposedBase: XposedInterface? = null

    constructor() : super()

    @Suppress("unused")
    constructor(base: XposedInterface, param: ModuleLoadedParam) : super() {
        xposedBase = base
        runCatching {
            attachFramework(base, Runnable {})
        }
        try {
            onModuleLoaded(param)
        } catch (t: Throwable) {
            Log.e(TAG, "[GBF-ACC] Error in onModuleLoaded(param) during constructor: ${t.message}", t)
        }
    }

    companion object {
        private const val TAG = "GBF-ACC"
    }

    private var currentProcessName: String = "unknown"

    private val hookRegistry = object : HookRegistry {
        override fun hookMethod(method: Method, callback: HookInvocationCallback): Boolean {
            return try {
                val target: XposedInterface = xposedBase ?: this@SkyLeapModule
                target.hook(method).intercept { chain ->
                    val thisObj = chain.thisObject
                    val args: List<Any?> = chain.args
                    val handled = callback.onInvoked(thisObj, args)
                    if (!handled) {
                        chain.proceed()
                    } else {
                        null
                    }
                }
                Log.i(TAG, "[GBF-ACC] Hook installed for ${method.declaringClass.simpleName}.${method.name}")
                true
            } catch (e: Throwable) {
                Log.e(TAG, "[GBF-ACC] Failed to hook method ${method.name}: ${e.message}", e)
                false
            }
        }
    }

    override fun onModuleLoaded(param: ModuleLoadedParam) {
        runCatching { super.onModuleLoaded(param) }
        currentProcessName = param.processName

        val appInfo = runCatching {
            val m = xposedBase?.javaClass?.getMethod("getApplicationInfo")
            m?.invoke(xposedBase) as? android.content.pm.ApplicationInfo
        }.getOrNull()
            ?: runCatching { getModuleApplicationInfo() }.getOrNull()
            ?: runCatching {
                val m = xposedBase?.javaClass?.getMethod("getModuleApplicationInfo")
                m?.invoke(xposedBase) as? android.content.pm.ApplicationInfo
            }.getOrNull()

        EmbeddedCoreManager.setModuleApplicationInfo(appInfo)
        if (appInfo != null) {
            Log.i(TAG, "[GBF-ACC] Resolved module ApplicationInfo via xposedBase: ${appInfo.nativeLibraryDir}")
        }

        Log.i(TAG, "==================================================")
        Log.i(TAG, "[GBF-ACC] attachFramework called")
        val fwName = runCatching { frameworkName }.getOrDefault("unknown")
        val fwVer = runCatching { frameworkVersion }.getOrDefault("unknown")
        val fwCode = runCatching { frameworkVersionCode }.getOrDefault(-1L)
        val apiVer = runCatching { apiVersion }.getOrDefault(102)
        Log.i(TAG, "[GBF-ACC] framework bound (name: $fwName, ver: $fwVer ($fwCode), api: $apiVer)")
        Log.i(TAG, "[GBF-ACC] onModuleLoaded (process: $currentProcessName)")
        Log.i(TAG, "==================================================")

        // Resolve adapter for current process, defaulting to SkyLeap for backward-compatibility in sandboxed environments,
        // or UniversalBrowserAdapter for app clones and standard WebView browsers.
        val adapter = BrowserAdapterRegistry.findAdapterByProcess(currentProcessName)
            ?: BrowserAdapterRegistry.findAdapterByPackage(currentProcessName)
            ?: run {
                val universal = UniversalBrowserAdapter(currentProcessName)
                BrowserAdapterRegistry.register(universal)
                universal
            }

        adapter.onModuleLoaded(currentProcessName, hookRegistry)
        tryFindAndBindContext(adapter)
    }

    override fun onPackageLoaded(param: PackageLoadedParam) {
        super.onPackageLoaded(param)
        val adapter = BrowserAdapterRegistry.findAdapterByPackage(param.packageName)
            ?: BrowserAdapterRegistry.findAdapterByProcess(currentProcessName)
            ?: run {
                val universal = UniversalBrowserAdapter(param.packageName)
                BrowserAdapterRegistry.register(universal)
                universal
            }
        adapter.onPackageLoaded(param.packageName)

        // Ensure module hooks are installed even if onModuleLoaded was bypassed
        adapter.onModuleLoaded(currentProcessName, hookRegistry)
        tryFindAndBindContext(adapter)
    }

    override fun onPackageReady(param: PackageReadyParam) {
        super.onPackageReady(param)
        val adapter = BrowserAdapterRegistry.findAdapterByPackage(param.packageName)
            ?: BrowserAdapterRegistry.findAdapterByProcess(currentProcessName)
            ?: run {
                val universal = UniversalBrowserAdapter(param.packageName)
                BrowserAdapterRegistry.register(universal)
                universal
            }
        adapter.onPackageReady(param.packageName)

        // Ensure hooks are installed with target package's ClassLoader
        if (adapter is com.sagisawa.gbfaccelerator.browser.WebViewBrowserAdapter) {
            adapter.installApplicationHooks(hookRegistry, param.classLoader)
            adapter.installWebViewHooks(hookRegistry, param.classLoader)
        }
        tryFindAndBindContext(adapter)
    }

    private var lifecycleRegistered = false

    private fun tryFindAndBindContext(adapter: com.sagisawa.gbfaccelerator.browser.BrowserAdapter) {
        try {
            val atClass = Class.forName("android.app.ActivityThread")
            val currentAppMethod = atClass.getMethod("currentApplication")
            val app = currentAppMethod.invoke(null) as? android.app.Application
            if (app != null) {
                Log.i(TAG, "[GBF-ACC] Application context discovered from ActivityThread.currentApplication()")
                val webAdapter = adapter as? com.sagisawa.gbfaccelerator.browser.WebViewBrowserAdapter
                if (webAdapter != null) {
                    webAdapter.onContextAvailable(app)
                    if (!lifecycleRegistered) {
                        lifecycleRegistered = true
                        app.registerActivityLifecycleCallbacks(object : android.app.Application.ActivityLifecycleCallbacks {
                            override fun onActivityCreated(activity: android.app.Activity, savedInstanceState: android.os.Bundle?) {
                                webAdapter.onContextAvailable(activity)
                                android.os.Handler(android.os.Looper.getMainLooper()).post {
                                    webAdapter.applyProxyConfigInternal()
                                }
                            }
                            override fun onActivityStarted(activity: android.app.Activity) {}
                            override fun onActivityResumed(activity: android.app.Activity) {
                                android.os.Handler(android.os.Looper.getMainLooper()).post {
                                    webAdapter.applyProxyConfigInternal()
                                }
                            }
                            override fun onActivityPaused(activity: android.app.Activity) {}
                            override fun onActivityStopped(activity: android.app.Activity) {}
                            override fun onActivitySaveInstanceState(activity: android.app.Activity, outState: android.os.Bundle) {}
                            override fun onActivityDestroyed(activity: android.app.Activity) {}
                        })
                    }
                    android.os.Handler(android.os.Looper.getMainLooper()).postDelayed({
                        webAdapter.applyProxyConfigInternal()
                    }, 500)
                }
            }
        } catch (_: Throwable) {
        }
    }
}
