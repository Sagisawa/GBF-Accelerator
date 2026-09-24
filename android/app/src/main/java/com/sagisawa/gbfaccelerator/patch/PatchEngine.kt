package com.sagisawa.gbfaccelerator.patch

import java.io.File

/**
 * Progress telemetry reported during APK patching execution.
 */
data class PatchProgress(
    val step: String,
    val progressPercent: Int = 0,
    val detailMessage: String = ""
)

/**
 * Configuration parameters for the APK patch pipeline.
 */
data class PatchOptions(
    val embedModule: Boolean = true,
    val forceCleartext: Boolean = false,
    val signatureBypassLevel: Int = 0,
    val customKeystorePath: String? = null
)

/**
 * Result outcome of a browser patching operation.
 */
sealed class PatchResult {

    /**
     * Patching completed successfully.
     *
     * @property outputApkFile The generated and signed APK file ready for installation.
     * @property packageName The package ID of the patched application.
     * @property summary Summary of patch modifications.
     */
    data class Success(
        val outputApkFile: File,
        val packageName: String,
        val summary: String
    ) : PatchResult()

    /**
     * Patching failed with an error.
     *
     * @property errorCode Machine-readable error code.
     * @property message Human-readable error explanation.
     * @property cause Underlying exception if present.
     */
    data class Failure(
        val errorCode: String,
        val message: String,
        val cause: Throwable? = null
    ) : PatchResult()

    /**
     * Patching was explicitly cancelled by user or system.
     */
    object Cancelled : PatchResult()
}

/**
 * Contract defining an APK patching engine.
 *
 * Future implementations may bundle or interface with on-device injection engines
 * to inject the GBF-Accelerator Xposed module directly into user-provided APKs.
 */
interface PatchEngine {

    /** Unique identifier for the patch engine implementation. */
    val id: String

    /** Human-readable engine name. */
    val name: String

    /** Whether this engine is operational in the current runtime environment. */
    val isAvailable: Boolean

    /**
     * Executes the patch process for the specified browser source.
     *
     * @param source The input browser source (installed package or APK file).
     * @param outputDirectory Destination folder for the generated patched APK.
     * @param options Configurable patch parameters.
     * @param progressListener Callback for progress updates.
     * @param completion Callback when patching finishes with a final PatchResult.
     */
    fun patch(
        source: BrowserSource,
        outputDirectory: File,
        options: PatchOptions = PatchOptions(),
        progressListener: (PatchProgress) -> Unit = {},
        completion: (PatchResult) -> Unit
    )
}
