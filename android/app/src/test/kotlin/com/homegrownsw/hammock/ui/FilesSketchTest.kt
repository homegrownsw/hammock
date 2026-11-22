package com.homegrownsw.hammock.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class FilesSketchTest {
    @Test fun openFoldersInTreeOrder() {
        val open = listOf("/me/app/src/ui", "/me/app/src", "/me/app/src-gen", "/me/app/docs", "/elsewhere/x")
        // A folder's children straight after it (src/ui before src-gen); others' left out.
        assertEquals(listOf(0 to "docs", 0 to "src", 1 to "ui", 0 to "src-gen"), filesSketch("/me/app", open))
    }

    @Test fun insideAClosedFolderIsLeftOut() {
        assertEquals(listOf(0 to "docs"), filesSketch("/me/app", listOf("/me/app/src/ui", "/me/app/docs")))
    }
}
