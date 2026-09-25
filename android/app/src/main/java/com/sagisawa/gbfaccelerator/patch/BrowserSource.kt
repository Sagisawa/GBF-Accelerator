package com.sagisawa.gbfaccelerator.patch

/**
 * Identifies the origin of a target browser package to be analyzed or patched.
 */
sealed class BrowserSource {

    /**
     * An application package currently installed on the Android operating system.
     *
     * @property packageName The unique Android application ID (e.g., "com.dena.skyleap").
     * @property label Human-readable application label.
     */
    data class InstalledPackage(
        val packageName: String,
        val label: String = ""
    ) : BrowserSource()

    /**
     * A standalone APK file (e.g., imported by user from local storage).
     *
     * @property fileUri Content URI string or file path representing the APK location.
     * @property fileName Original file name of the package.
     * @property fileSize File size in bytes.
     * @property expectedPackageName Optional expected application package ID.
     */
    data class LocalApk(
        val fileUri: String,
        val fileName: String,
        val fileSize: Long = 0L,
        val expectedPackageName: String? = null
    ) : BrowserSource()
}
