package com.sagisawa.gbfaccelerator.xposed

import android.util.Log
import com.sagisawa.gbfaccelerator.browser.BrowserAdapterRegistry
import com.sagisawa.gbfaccelerator.browser.HookInvocationCallback
import com.sagisawa.gbfaccelerator.browser.HookRegistry
import com.sagisawa.gbfaccelerator.browser.UniversalBrowserAdapter
import io.github.libxposed.api.XposedInterface
import io.github.libxposed.api.XposedModule
import io.github.libxposed.api.XposedModuleInterface.ModuleLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageReadyParam
import java.lang.reflect.Method

/**
 * Xposed / LSPosed module entry point.
 * Defined in META-INF/xposed/java_init.list.
 * Delegates browser identification, lifecycle events, and proxy interception to BrowserAdapterRegistry.
 */
class SkyLeapModule : XposedModule() {

    companion object {
        private const val TAG = "GBF-ACC"
    }

    private var currentProcessName: String = "unknown"

    private val hookRegistry = object : HookRegistry {
        override fun hookMethod(method: Method, callback: HookInvocationCallback): Boolean {
            return try {
                hook(method).intercept { chain: XposedInterface.Chain ->
                    val thisObj = chain.thisObject
                    val args: List<Any?> = chain.args
                    val handled = callback.onInvoked(thisObj, args)
                    if (!handled) {
                        chain.proceed()
                    } else {
                        null
                    }
                }
                true
            } catch (e: Throwable) {
                Log.e(TAG, "[GBF-ACC] Failed to hook method ${method.name}: ${e.message}", e)
                false
            }
        }
    }

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

        // Resolve adapter for current process, defaulting to SkyLeap for backward-compatibility in sandboxed environments,
        // or UniversalBrowserAdapter for app clones and standard WebView browsers.
        val adapter = BrowserAdapterRegistry.findAdapterByProcess(currentProcessName)
            ?: BrowserAdapterRegistry.findAdapterByPackage("com.dena.skyleap")
            ?: UniversalBrowserAdapter(currentProcessName)

        adapter.onModuleLoaded(currentProcessName, hookRegistry)
    }

    override fun onPackageLoaded(param: PackageLoadedParam) {
        super.onPackageLoaded(param)
        val adapter = BrowserAdapterRegistry.findAdapterByPackage(param.packageName)
            ?: UniversalBrowserAdapter(param.packageName)
        adapter.onPackageLoaded(param.packageName)
    }

    override fun onPackageReady(param: PackageReadyParam) {
        super.onPackageReady(param)
        val adapter = BrowserAdapterRegistry.findAdapterByPackage(param.packageName)
            ?: UniversalBrowserAdapter(param.packageName)
        adapter.onPackageReady(param.packageName)
    }
}
