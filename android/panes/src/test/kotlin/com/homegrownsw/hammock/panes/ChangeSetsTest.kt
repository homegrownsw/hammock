package com.homegrownsw.hammock.panes

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ChangeSetsTest {
    @Test fun keepsAndInserts() {
        // "hello world" → "hello, brave world": keep 5, insert ",", keep 1, insert "brave ", keep 5.
        assertEquals("hello, brave world", ChangeSets.apply("""[5,[0,","],1,[0,"brave "],5]""", "hello world"))
    }

    @Test fun deletes() {
        assertEquals("held", ChangeSets.apply("""[2,[1],1,[1,"d"]]""", "hello"))
        assertEquals("", ChangeSets.apply("""[[5]]""", "hello"))
    }

    @Test fun insertsLinesJoinedByNewlines() {
        assertEquals("a\nb\nc", ChangeSets.apply("""[1,[0,"","b",""],1]""", "ac"))
    }

    @Test fun countsUTF16Units() {
        // An emoji is two UTF-16 units in CodeMirror's offsets, as in Kotlin's.
        val text = "a😀b"
        assertEquals("a😀!b", ChangeSets.apply("""[3,[0,"!"],1]""", text))
    }

    @Test fun refusesWhatDoesNotFit() {
        assertNull(ChangeSets.apply("[3]", "hello"))           // too short: doesn't cover the text
        assertNull(ChangeSets.apply("[6]", "hello"))           // past the end
        assertNull(ChangeSets.apply("""[4,[2]]""", "hello"))   // deletes past the end
        assertNull(ChangeSets.apply("""["x"]""", "hello"))     // not a change
        assertNull(ChangeSets.apply("not json", "hello"))
    }

    @Test fun emptyDocument() {
        assertEquals("hi", ChangeSets.apply("""[[0,"hi"]]""", ""))
        assertEquals("", ChangeSets.apply("[]", ""))
    }
}
