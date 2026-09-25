package com.sagisawa.gbfaccelerator.patch

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import androidx.core.content.FileProvider
import java.io.File

/**
 * Result of requesting installation of a patched browser APK.
 */
sealed class InstallResult {
    /** The Android package installer prompt has been presented to the user. */
    object Prompted : InstallResult()

    /** Installation request could not be dispatched. */
    data class Failure(val reason: String, val cause: Throwable? = null) : InstallResult()
}

/**
 * Interface for prompting system installation of a generated patched browser APK.
 */
interface PatchedBrowserInstaller {

    /**
     * Dispatches an installation intent for the specified APK file.
     *
     * @param context Application or activity context.
     * @param apkFile The file to install.
     * @return InstallResult indicating whether the prompt was launched or failed.
     */
    fun install(context: Context, apkFile: File): InstallResult
}

/**
 * Standard implementation leveraging Android's PackageInstaller intent via FileProvider.
 */
class SystemBrowserInstaller(
    private val fileProviderAuthority: String? = null
) : PatchedBrowserInstaller {

    override fun install(context: Context, apkFile: File): InstallResult {
        if (!apkFile.exists() || !apkFile.isFile) {
            return InstallResult.Failure("Target APK file does not exist: ${apkFile.absolutePath}")
        }

        return try {
            val contentUri: Uri = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
                val authority = fileProviderAuthority ?: "${context.packageName}.fileprovider"
                FileProvider.getUriForFile(context, authority, apkFile)
            } else {
                Uri.fromFile(apkFile)
            }

            val intent = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(contentUri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }

            context.startActivity(intent)
            InstallResult.Prompted
        } catch (e: Throwable) {
            InstallResult.Failure("Failed to launch system package installer: ${e.message}", e)
        }
    }
}
