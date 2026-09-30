package io.github.pwnedbygary.scterm.update

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull

class AppUpdatesTest {
    @Test
    fun versionCodesFollowTheReleaseBuilds() {
        // Must match versionCodeOf in app/build.gradle.kts, or updates are misjudged.
        assertEquals(20100, AppUpdates.versionCodeOf("2.1.0"))
        assertEquals(20000, AppUpdates.versionCodeOf("v2.0.0"))
        assertEquals(10203, AppUpdates.versionCodeOf("1.2.3"))
        for (bad in listOf("dev", "2.1", "2.1.0.1", "2.100.0", "2.1.0-rc1", "")) {
            assertNull(AppUpdates.versionCodeOf(bad), bad)
        }
    }

    @Test
    fun readsTheApkFromGitHubsLatestRelease() {
        val release = assertNotNull(
            AppUpdates.parseRelease(
                """
                {"tag_name":"v2.1.0","html_url":"https://github.com/o/r/releases/tag/v2.1.0","body":"What changed",
                 "assets":[
                   {"name":"scterm","browser_download_url":"https://example.invalid/scterm","size":8530920,"digest":"sha256:a338"},
                   {"name":"scterm-android-2.1.0.apk","browser_download_url":"https://example.invalid/apk","size":425662,
                    "digest":"sha256:DD08AB"}]}
                """,
            ),
        )
        assertEquals("2.1.0", release.version)
        assertEquals(20100, release.versionCode)
        assertEquals("scterm-android-2.1.0.apk", release.apkName)
        assertEquals("https://example.invalid/apk", release.apkUrl)
        assertEquals(425662, release.size)
        assertEquals("dd08ab", release.sha256)
        assertEquals("What changed", release.notes)
        assertEquals("https://github.com/o/r/releases/tag/v2.1.0", release.page)
    }

    @Test
    fun releasesWithoutAnApkOrAVersionOfferNothing() {
        assertNull(AppUpdates.parseRelease("""{"tag_name":"v1.1.0","assets":[{"name":"scterm","browser_download_url":"https://x/s"}]}"""))
        assertNull(AppUpdates.parseRelease("""{"tag_name":"nightly","assets":[{"name":"scterm-android-x.apk","browser_download_url":"https://x/a"}]}"""))
        assertNull(AppUpdates.parseRelease("""{"assets":[]}"""))
    }

    @Test
    fun anUnpublishedDigestIsNotInvented() {
        val release = assertNotNull(
            AppUpdates.parseRelease(
                """{"tag_name":"v2.1.0","assets":[{"name":"scterm-android-2.1.0.apk","browser_download_url":"https://x/a","digest":"md5:00"}]}""",
            ),
        )
        assertNull(release.sha256)
        assertEquals(-1, release.size)
    }
}
