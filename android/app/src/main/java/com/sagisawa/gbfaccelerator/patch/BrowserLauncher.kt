package com.sagisawa.gbfaccelerator.patch

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager

/**
 * Descriptor of a target browser to be managed and launched by the Host application.
 */
data class BrowserTarget(
    val id: String = "skyleap",
    val name: String = "SkyLeap",
    val targetPackages: Set<String> = setOf("com.dena.skyleap")
)

/**
 * Result of launching a target browser.
 */
sealed class LaunchResult {
    /** The target application was successfully launched into the foreground. */
    object Success : LaunchResult()

    /** The target application package is not installed on this device. */
    data class NotInstalled(val packageName: String) : LaunchResult()

    /** An unexpected error occurred while attempting to launch the browser. */
    data class Failure(val reason: String, val cause: Throwable? = null) : LaunchResult()
}

/**
 * Metadata about an installed browser package.
 */
data class BrowserAppInfo(
    val packageName: String,
    val isInstalled: Boolean,
    val versionName: String? = null,
    val versionCode: Long = 0L,
    val label: String? = null
)

/**
 * Interface responsible for querying installation status and launching target browsers.
 */
interface BrowserLauncher {

    /**
     * Queries package information for the primary package handled by this browser target.
     */
    fun getAppInfo(context: Context, target: BrowserTarget): BrowserAppInfo

    /**
     * Checks if at least one target package associated with the browser target is installed.
     */
    fun isInstalled(context: Context, target: BrowserTarget): Boolean

    /**
     * Launches the primary application associated with the given browser target.
     */
    fun launch(context: Context, target: BrowserTarget): LaunchResult
}

/**
 * Default implementation of BrowserLauncher using standard Android PackageManager.
 */
class DefaultBrowserLauncher : BrowserLauncher {

    override fun getAppInfo(context: Context, target: BrowserTarget): BrowserAppInfo {
        val pm = context.packageManager
        for (pkg in target.targetPackages) {
            try {
                val pInfo = pm.getPackageInfo(pkg, 0)
                val appLabel = pm.getApplicationLabel(pInfo.applicationInfo ?: continue).toString()
                val vCode = if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.P) {
                    pInfo.longVersionCode
                } else {
                    @Suppress("DEPRECATION")
                    pInfo.versionCode.toLong()
                }
                return BrowserAppInfo(
                    packageName = pkg,
                    isInstalled = true,
                    versionName = pInfo.versionName,
                    versionCode = vCode,
                    label = appLabel
                )
            } catch (_: PackageManager.NameNotFoundException) {
                // Try next package if any
            } catch (e: Throwable) {
                // Return uninstalled info on permission or lookup error
                return BrowserAppInfo(packageName = pkg, isInstalled = false)
            }
        }

        val primaryPkg = target.targetPackages.firstOrNull() ?: "unknown"
        return BrowserAppInfo(packageName = primaryPkg, isInstalled = false)
    }

    override fun isInstalled(context: Context, target: BrowserTarget): Boolean {
        return getAppInfo(context, target).isInstalled
    }

    override fun launch(context: Context, target: BrowserTarget): LaunchResult {
        val pm = context.packageManager
        for (pkg in target.targetPackages) {
            val launchIntent = pm.getLaunchIntentForPackage(pkg)
            if (launchIntent != null) {
                return try {
                    launchIntent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    context.startActivity(launchIntent)
                    LaunchResult.Success
                } catch (e: Throwable) {
                    LaunchResult.Failure("Failed to start activity for $pkg: ${e.message}", e)
                }
            }
        }

        val primaryPkg = target.targetPackages.firstOrNull() ?: "unknown"
        return LaunchResult.NotInstalled(primaryPkg)
    }
}
