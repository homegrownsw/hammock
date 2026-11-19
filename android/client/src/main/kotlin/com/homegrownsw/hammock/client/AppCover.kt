package com.homegrownsw.hammock.client

import kotlinx.coroutines.flow.MutableStateFlow

/**
 * Whether the app's own lock (Settings ▸ Privacy ▸ Require Unlock) covers it,
 * as the app sets it. Dialogs and sheets are windows of their own, drawn over
 * the cover: the panes' (in modules that can't see the app's LocalAppLocked)
 * don't show while it's set.
 */
object AppCover {
    val locked = MutableStateFlow(false)
}
