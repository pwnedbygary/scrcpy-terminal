package io.github.pwnedbygary.scterm

import kotlin.test.Test
import kotlin.test.assertEquals

class PackageNameTest {
    @Test
    fun debugBuildsInstallNextToTheRelease() {
        // With the release's package name, installing a debug build means uninstalling the release first, which deletes its pairings.
        val expected = if (BuildConfig.DEBUG) "io.github.pwnedbygary.scterm.debug" else "io.github.pwnedbygary.scterm"
        assertEquals(expected, BuildConfig.APPLICATION_ID)
    }
}
