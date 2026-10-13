package com.sagisawa.gbfaccelerator.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Verifies the access boundary of the optional control action extra that MainActivity accepts.
 *
 * MainActivity must stay exported for the launcher entry point, so the action extra is the only
 * externally reachable control surface; it may only be honoured for intents this app launched.
 */
class MainActivityIntentTrustTest {

    private val selfPackage = "com.sagisawa.gbfaccelerator"

    @Test
    fun ownPackageIsTrusted() {
        assertTrue(
            "an intent launched by this app must be able to control the core",
            isTrustedActionCaller(selfPackage, selfPackage),
        )
    }

    @Test
    fun launcherIsNotTrusted() {
        assertFalse(
            "the desktop launcher only opens the activity, it must not control the core",
            isTrustedActionCaller("com.android.launcher3", selfPackage),
        )
    }

    @Test
    fun adbShellIsNotTrusted() {
        assertFalse(
            "an adb launched activity must not control the core",
            isTrustedActionCaller("com.android.shell", selfPackage),
        )
    }

    @Test
    fun unknownCallerIsNotTrusted() {
        assertFalse(
            "an unknown launcher package must not control the core",
            isTrustedActionCaller(null, selfPackage),
        )
        assertFalse(
            "an empty launcher package must not control the core",
            isTrustedActionCaller("", selfPackage),
        )
    }
}
