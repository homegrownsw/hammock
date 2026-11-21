package com.homegrownsw.hammock

import com.homegrownsw.hammock.ui.HammockTheme
import hammock.v1.Hammock.HostTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Test

/** Settings ▸ Theme's choice, the computer's theme and the wallpaper's colours: which one shows. */
class ThemeTest {
    private val wallpaper = HammockTheme(HammockTheme.WALLPAPER, 1L to 2L, 3L to 4L, 5L to 6L, 7L to 8L)
    private val host = HostTheme.newBuilder().setName("Nord").build()

    @Test fun wallpaperWhenPicked() {
        assertSame(wallpaper, HammockTheme.resolve(HammockTheme.WALLPAPER, host, wallpaper))
    }

    @Test fun wallpaperPickedButNoneOnThisPhoneFallsBackToTheComputers() {
        assertEquals("Nord", HammockTheme.resolve(HammockTheme.WALLPAPER, host, null).name)
    }

    @Test fun otherChoicesIgnoreTheWallpaper() {
        assertEquals("Ocean", HammockTheme.resolve("Ocean", host, wallpaper).name)
        assertEquals("Nord", HammockTheme.resolve(HammockTheme.SAME_AS_COMPUTER, host, wallpaper).name)
    }
}
