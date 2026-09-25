package com.sagisawa.gbfaccelerator.patch

import java.io.File

/**
 * Standby implementation of PatchEngine.
 *
 * Serves as an architectural placeholder until a full on-device patch engine is integrated.
 * Reports clean, objective failure without pretending to complete fake operations.
 */
class StubPatchEngine : PatchEngine {

    override val id: String = "stub"
    override val name: String = "Standby Patch Engine"
    override val isAvailable: Boolean = false

    override fun patch(
        source: BrowserSource,
        outputDirectory: File,
        options: PatchOptions,
        progressListener: (PatchProgress) -> Unit,
        completion: (PatchResult) -> Unit
    ) {
        progressListener(PatchProgress(step = "Init", progressPercent = 0, detailMessage = "Checking engine availability"))
        completion(
            PatchResult.Failure(
                errorCode = "ENGINE_NOT_IMPLEMENTED",
                message = "当前需要使用外部工具提前完成浏览器 Patch，本 App 暂不在手机内执行 APK Patch。"
            )
        )
    }
}
