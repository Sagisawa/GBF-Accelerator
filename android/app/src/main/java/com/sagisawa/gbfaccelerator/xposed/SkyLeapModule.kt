package com.sagisawa.gbfaccelerator.xposed

import android.util.Log
import io.github.libxposed.api.XposedModule
import io.github.libxposed.api.XposedModuleInterface.ModuleLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageLoadedParam
import io.github.libxposed.api.XposedModuleInterface.PackageReadyParam

class SkyLeapModule : XposedModule() {

    companion object {
        private const val TAG = "GBF-ACC"
        private const val TARGET_PACKAGE = "com.dena.skyleap"
    }

    private var currentProcessName: String = "unknown"

    override fun onModuleLoaded(param: ModuleLoadedParam) {
        super.onModuleLoaded(param)
        currentProcessName = param.processName
        Log.i(TAG, "==================================================")
        Log.i(TAG, "[GBF-ACC] Xposed Module initialized successfully!")
        Log.i(TAG, "[GBF-ACC] Framework name : $frameworkName")
        Log.i(TAG, "[GBF-ACC] Framework ver  : $frameworkVersion ($frameworkVersionCode)")
        Log.i(TAG, "[GBF-ACC] API Version    : $apiVersion")
        Log.i(TAG, "[GBF-ACC] Process name   : $currentProcessName")
        Log.i(TAG, "==================================================")
    }

    override fun onPackageLoaded(param: PackageLoadedParam) {
        super.onPackageLoaded(param)
        if (param.packageName == TARGET_PACKAGE) {
            Log.i(TAG, "[GBF-ACC] Target package loaded: ${param.packageName} in process: $currentProcessName (isFirstPackage=${param.isFirstPackage})")
        }
    }

    override fun onPackageReady(param: PackageReadyParam) {
        super.onPackageReady(param)
        if (param.packageName == TARGET_PACKAGE) {
            Log.i(TAG, "==================================================")
            Log.i(TAG, "[GBF-ACC] SkyLeap package ready / loaded!")
            Log.i(TAG, "[GBF-ACC] Target Package : ${param.packageName}")
            Log.i(TAG, "[GBF-ACC] Current Process: $currentProcessName")
            Log.i(TAG, "[GBF-ACC] ClassLoader    : ${param.classLoader}")
            Log.i(TAG, "[GBF-ACC] Injection verification: SUCCESS (LSPosed -> GBF-ACC -> SkyLeap)")
            Log.i(TAG, "==================================================")
        }
    }
}
