package com.sagisawa.gbfaccelerator.browser

import java.lang.reflect.Method

/**
 * Invocation callback for intercepted method calls.
 */
fun interface HookInvocationCallback {
    /**
     * @return true if the hook consumed/handled the invocation (skipping original execution),
     *         false to proceed with original method invocation.
     */
    fun onInvoked(thisObject: Any?, args: List<Any?>): Boolean
}

/**
 * Abstraction layer for registering method hooks, decoupling browser adapters
 * from concrete framework implementations (libxposed, sandhooks, dynamic proxies, unit test mocks).
 */
interface HookRegistry {
    /**
     * Installs a hook before the target method executes.
     * @return true if hook was successfully registered, false otherwise.
     */
    fun hookMethod(method: Method, callback: HookInvocationCallback): Boolean
}

/**
 * Universal interface for Android browser adapters.
 * Each supported browser provides an adapter that handles:
 * - Package and process identification
 * - Framework lifecycle callbacks
 * - WebView detection and proxy injection
 */
interface BrowserAdapter {
    val id: String
    val name: String
    val targetPackages: Set<String>

    /**
     * Determines whether the given package belongs to this browser.
     */
    fun matchesPackage(packageName: String): Boolean

    /**
     * Determines whether the given process name belongs to this browser.
     */
    fun matchesProcess(processName: String): Boolean

    /**
     * Called when the module is loaded into the target process.
     */
    fun onModuleLoaded(processName: String, hookRegistry: HookRegistry)

    /**
     * Called when a target package is loaded.
     */
    fun onPackageLoaded(packageName: String)

    /**
     * Called when a target package is fully initialized and ready.
     */
    fun onPackageReady(packageName: String)
}
