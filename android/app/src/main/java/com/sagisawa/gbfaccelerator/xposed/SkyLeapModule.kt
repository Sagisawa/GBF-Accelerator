package com.sagisawa.gbfaccelerator.xposed

import android.util.Log
import io.github.libxposed.api.XposedInterface
import io.github.libxposed.api.XposedModule
import io.github.libxposed.api.XposedModuleInterface.ModuleLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageReadyParam

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
        Log.i(TAG, "[GBF-ACC] Xposed Module initialized successfully!")
        Log.i(TAG, "[GBF-ACC] Framework name : $fwName")
        Log.i(TAG, "[GBF-ACC] Framework ver  : $fwVer ($fwCode)")
        Log.i(TAG, "[GBF-ACC] API Version    : $apiVer")
        Log.i(TAG, "[GBF-ACC] Process name   : $currentProcessName")
        Log.i(TAG, "==================================================")
    }

    override fun onPackageLoaded(param: PackageLoadedParam) {
        super.onPackageLoaded(param)
        if (param.packageName in TARGET_PACKAGES) {
            val classLoader = runCatching { param.defaultClassLoader }.getOrNull()
            Log.i(TAG, "==================================================")
            Log.i(TAG, "[GBF-ACC] SkyLeap package loaded!")
            Log.i(TAG, "[GBF-ACC] Target Package : ${param.packageName}")
            Log.i(TAG, "[GBF-ACC] Current Process: $currentProcessName")
            Log.i(TAG, "[GBF-ACC] ClassLoader    : $classLoader")
            Log.i(TAG, "[GBF-ACC] First Package  : ${param.isFirstPackage}")
            Log.i(TAG, "[GBF-ACC] Injection verification: SUCCESS (LSPosed -> GBF-ACC -> SkyLeap)")
            Log.i(TAG, "==================================================")
        }
    }

    override fun onPackageReady(param: PackageReadyParam) {
        super.onPackageReady(param)
        if (param.packageName in TARGET_PACKAGES) {
            Log.i(TAG, "==================================================")
            Log.i(TAG, "[GBF-ACC] SkyLeap package ready!")
            Log.i(TAG, "[GBF-ACC] Target Package : ${param.packageName}")
            Log.i(TAG, "[GBF-ACC] Current Process: $currentProcessName")
            Log.i(TAG, "[GBF-ACC] ClassLoader    : ${param.classLoader}")
            Log.i(TAG, "[GBF-ACC] Injection verification: SUCCESS (LSPosed -> GBF-ACC -> SkyLeap)")
            Log.i(TAG, "==================================================")
        }
    }
}
