package com.sagisawa.gbfaccelerator.browser

import android.content.Context
import android.util.Log
import com.sagisawa.gbfaccelerator.core.EmbeddedCoreManager
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Browser adapter for standalone Chromium-based browsers (such as official Google Chrome, Kiwi Browser, Brave, Edge).
 *
 * Interception mechanism:
 * 1. Application lifecycle hook: Captures Context in the main process and spawns the embedded Go Core daemon (127.0.0.1:8124 / 8125).
 * 2. Chromium CommandLine injection: Hooks `org.chromium.base.CommandLine` to append native network flags:
 *    - `--proxy-server=http://127.0.0.1:8124`
 *    - `--proxy-bypass-list=<-loopback>;<local>`
 *    - `--ignore-certificate-errors`
 * 3. SSL verification bypass: Hooks Chromium's Java X509 certificate verification to trust our local CA certificate.
 */
class ChromiumBrowserAdapter(
    override val id: String = ID,
    override val name: String = NAME,
    override val targetPackages: Set<String> = TARGET_PACKAGES
) : BrowserAdapter {

    companion object {
        const val ID = "chromium_browser"
        const val NAME = "Standalone Chromium Browser"
        val TARGET_PACKAGES: Set<String> = setOf(
            "com.android.chrome",
            "com.chrome.beta",
            "com.chrome.canary",
            "com.chrome.dev",
            "com.kiwibrowser.browser",
            "com.microsoft.emmx",
            "com.brave.browser",
            "com.opera.browser",
            "com.opera.mini.native",
            "com.opera.gx",
            "com.vivaldi.browser"
        )
        private const val TAG = "GBF-ACC-Chromium"

        const val PROXY_SERVER = "http://127.0.0.1:8124"
        const val PROXY_BYPASS = "<-loopback>;<local>"
    }

    constructor(packageName: String) : this(
        id = "chromium_$packageName",
        name = "Chromium ($packageName)",
        targetPackages = setOf(packageName)
    )

    private val coreStarted = AtomicBoolean(false)

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
        val classLoader = javaClass.classLoader ?: ClassLoader.getSystemClassLoader()
        installApplicationHooks(hookRegistry, classLoader)
        installChromiumCommandLineHooks(hookRegistry, classLoader)
        installX509Hooks(hookRegistry, classLoader)
    }

    override fun onPackageLoaded(packageName: String) {
        if (matchesPackage(packageName)) {
            Log.i(TAG, "[$name] Chromium Package loaded: $packageName")
        }
    }

    override fun onPackageReady(packageName: String) {
        if (matchesPackage(packageName)) {
            Log.i(TAG, "[$name] Chromium Package ready: $packageName")
        }
    }

    private fun onContextAvailable(context: Context) {
        if (coreStarted.compareAndSet(false, true)) {
            Log.i(TAG, "[GBF-ACC] Starting Embedded Go Core for Chromium host: ${context.packageName}")
            EmbeddedCoreManager.ensureStarted(context)
        }
    }

    fun installApplicationHooks(hookRegistry: HookRegistry, classLoader: ClassLoader) {
        try {
            val appClass = Class.forName("android.app.Application", true, classLoader)
            val contextClass = Class.forName("android.content.Context", true, classLoader)
            val attachBaseContextMethod = appClass.getDeclaredMethod("attachBaseContext", contextClass)
            hookRegistry.hookMethod(attachBaseContextMethod) { thisObj, args ->
                val ctx = args.firstOrNull() as? Context ?: (thisObj as? Context)
                ctx?.let { onContextAvailable(it) }
                false
            }
        } catch (_: Throwable) {
        }

        try {
            val appClass = Class.forName("android.app.Application", true, classLoader)
            val onCreateMethod = appClass.getMethod("onCreate")
            hookRegistry.hookMethod(onCreateMethod) { thisObj, _ ->
                (thisObj as? Context)?.let { onContextAvailable(it) }
                false
            }
        } catch (_: Throwable) {
        }
    }

    fun installChromiumCommandLineHooks(hookRegistry: HookRegistry, classLoader: ClassLoader) {
        // Hook org.chromium.base.CommandLine.init(String[] args)
        try {
            val cmdClass = Class.forName("org.chromium.base.CommandLine", true, classLoader)
            for (m in cmdClass.declaredMethods) {
                if (m.name == "init" && m.parameterTypes.size == 1 && m.parameterTypes[0] == Array<String>::class.java) {
                    hookRegistry.hookMethod(m) { _, _ ->
                        injectCommandLineSwitches(cmdClass)
                        false
                    }
                    Log.i(TAG, "[GBF-ACC] Hooked org.chromium.base.CommandLine.init")
                    break
                }
            }
        } catch (_: Throwable) {
        }

        // Also try hooking CommandLineInitUtil.initCommandLine if present
        try {
            val utilClass = Class.forName("org.chromium.base.CommandLineInitUtil", true, classLoader)
            for (m in utilClass.declaredMethods) {
                if (m.name.startsWith("initCommandLine")) {
                    hookRegistry.hookMethod(m) { _, _ ->
                        try {
                            val cmdClass = Class.forName("org.chromium.base.CommandLine", true, classLoader)
                            injectCommandLineSwitches(cmdClass)
                        } catch (_: Throwable) {}
                        false
                    }
                    Log.i(TAG, "[GBF-ACC] Hooked org.chromium.base.CommandLineInitUtil.${m.name}")
                }
            }
        } catch (_: Throwable) {
        }
    }

    /**
     * Appends required proxy and SSL bypass switches to Chromium's singleton CommandLine instance.
     */
    fun injectCommandLineSwitches(cmdClass: Class<*>) {
        try {
            val getInstanceMethod = cmdClass.getMethod("getInstance")
            val cmdInstance = getInstanceMethod.invoke(null) ?: return

            val appendSwitchWithValueMethod = cmdClass.getMethod("appendSwitchWithValue", String::class.java, String::class.java)
            val appendSwitchMethod = cmdClass.getMethod("appendSwitch", String::class.java)
            val hasSwitchMethod = cmdClass.getMethod("hasSwitch", String::class.java)

            val hasProxy = hasSwitchMethod.invoke(cmdInstance, "proxy-server") as? Boolean ?: false
            if (!hasProxy) {
                appendSwitchWithValueMethod.invoke(cmdInstance, "proxy-server", PROXY_SERVER)
                appendSwitchWithValueMethod.invoke(cmdInstance, "proxy-bypass-list", PROXY_BYPASS)
                appendSwitchMethod.invoke(cmdInstance, "ignore-certificate-errors")
                Log.i(TAG, "[GBF-ACC] Successfully injected Chromium CommandLine proxy: $PROXY_SERVER (bypass: $PROXY_BYPASS, ignore-certificate-errors)")
            }
        } catch (e: Throwable) {
            Log.w(TAG, "[GBF-ACC] Note on injecting CommandLine switches: ${e.message}")
        }
    }

    fun installX509Hooks(hookRegistry: HookRegistry, classLoader: ClassLoader) {
        // Hook org.chromium.net.X509Util.verifyServerCertificates
        try {
            val x509UtilClass = Class.forName("org.chromium.net.X509Util", true, classLoader)
            for (m in x509UtilClass.declaredMethods) {
                if (m.name == "verifyServerCertificates") {
                    hookRegistry.hookMethod(m) { _, _ ->
                        false
                    }
                    Log.i(TAG, "[GBF-ACC] Hooked org.chromium.net.X509Util.verifyServerCertificates")
                }
            }
        } catch (_: Throwable) {
        }
    }
}
