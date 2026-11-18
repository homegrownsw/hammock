package com.homegrownsw.hammock.agentview

import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.speech.RecognitionListener
import android.speech.RecognizerIntent
import android.speech.SpeechRecognizer
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import java.util.Locale

/**
 * Words going into the composer as you say them, recognised on the phone
 * (SpeechRecognizer's on-device recogniser: nothing leaves it). It listens
 * phrase by phrase until stopped: each settled phrase is kept, and spoken
 * slash commands become the command ("slash review" is "/review").
 */
@Stable
class Dictation(private val context: Context) {
    var active by mutableStateOf(false)
        private set
    /** How loud it hears you, 0–1, for the level meter. */
    var level by mutableFloatStateOf(0f)
        private set
    /** The last few levels, oldest first. */
    val levels = mutableStateListOf(0f, 0f, 0f, 0f, 0f)
    /** Why it stopped or can't start, to show; null when all's well. */
    var problem by mutableStateOf<String?>(null)

    private var recognizer: SpeechRecognizer? = null
    private var base = ""
    private var settled = ""
    private var commands: List<String> = emptyList()
    private var onText: (String) -> Unit = {}
    /** Silences in a row: it stops after a few rather than listen forever. */
    private var misses = 0

    companion object {
        fun available(context: Context): Boolean = SpeechRecognizer.isOnDeviceRecognitionAvailable(context)
    }

    /** Starts listening, with [base] the composer's text so far; [onText] gets the whole text as it changes. */
    fun start(base: String, commands: List<String>, onText: (String) -> Unit) {
        if (active) return
        problem = null
        this.base = base
        this.settled = ""
        this.commands = commands
        this.onText = onText
        misses = 0
        val r = runCatching { SpeechRecognizer.createOnDeviceSpeechRecognizer(context) }.getOrNull()
        if (r == null) { problem = "Dictation isn't available on this phone."; return }
        r.setRecognitionListener(listener)
        recognizer = r
        active = true
        listen()
    }

    /** Stops listening; what's being said settles first. */
    fun stop() {
        if (!active) return
        active = false
        recognizer?.stopListening()
    }

    /** Stops at once, dropping what's not settled. */
    fun cancel() {
        active = false
        teardown()
    }

    private fun teardown() {
        recognizer?.destroy()
        recognizer = null
        level = 0f
        for (i in levels.indices) levels[i] = 0f
    }

    private fun intent() = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
        putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
        putExtra(RecognizerIntent.EXTRA_LANGUAGE, Locale.getDefault().toLanguageTag())
        putExtra(RecognizerIntent.EXTRA_PARTIAL_RESULTS, true)
        // The commands as you'd say them, so "review PR" is heard as that.
        putStringArrayListExtra(RecognizerIntent.EXTRA_BIASING_STRINGS, ArrayList(DictationText.vocabulary(commands)))
    }

    private fun listen() { recognizer?.startListening(intent()) }

    private fun text(partial: String) = DictationText.join(base, DictationText.join(settled, partial))

    private val listener = object : RecognitionListener {
        override fun onReadyForSpeech(params: Bundle?) {}
        override fun onBeginningOfSpeech() {}
        override fun onRmsChanged(rmsdB: Float) {
            // Roughly -2 (silence) to 10 (loud).
            level = ((rmsdB + 2f) / 12f).coerceIn(0f, 1f)
            levels.removeAt(0)
            levels.add(level)
        }
        override fun onBufferReceived(buffer: ByteArray?) {}
        override fun onEndOfSpeech() {}
        override fun onEvent(eventType: Int, params: Bundle?) {}

        override fun onPartialResults(partialResults: Bundle?) {
            val words = partialResults?.getStringArrayList(SpeechRecognizer.RESULTS_RECOGNITION)?.firstOrNull() ?: return
            onText(text(words))
        }

        override fun onResults(results: Bundle?) {
            val words = results?.getStringArrayList(SpeechRecognizer.RESULTS_RECOGNITION)?.firstOrNull() ?: ""
            settled = DictationText.mapSlashCommands(DictationText.join(settled, words), commands)
            onText(text(""))
            misses = 0
            if (active) listen() else teardown()
        }

        override fun onError(error: Int) {
            when (error) {
                SpeechRecognizer.ERROR_NO_MATCH, SpeechRecognizer.ERROR_SPEECH_TIMEOUT -> {
                    // A pause: listen on, unless it's been quiet a while.
                    if (active && ++misses < 3) { listen(); return }
                }
                SpeechRecognizer.ERROR_CLIENT -> {} // stopped on purpose
                SpeechRecognizer.ERROR_INSUFFICIENT_PERMISSIONS -> problem = "Hammock needs the microphone to dictate."
                SpeechRecognizer.ERROR_LANGUAGE_NOT_SUPPORTED, SpeechRecognizer.ERROR_LANGUAGE_UNAVAILABLE -> {
                    // The on-device model for your language isn't here yet: ask for it.
                    runCatching { recognizer?.triggerModelDownload(intent()) }
                    problem = "Downloading speech recognition for your language. Try again once it's done."
                }
                SpeechRecognizer.ERROR_NETWORK, SpeechRecognizer.ERROR_NETWORK_TIMEOUT, SpeechRecognizer.ERROR_SERVER ->
                    problem = "Dictation couldn't reach its speech model."
                SpeechRecognizer.ERROR_RECOGNIZER_BUSY -> problem = "The microphone is busy with something else."
                else -> problem = "Dictation stopped."
            }
            active = false
            teardown()
        }
    }
}

/** Your voice while dictating: a few bars that rise and fall with it. */
@Composable
internal fun DictationLevels(d: Dictation) {
    val c = LocalAgentColors.current
    Row(horizontalArrangement = Arrangement.spacedBy(3.dp), verticalAlignment = Alignment.CenterVertically) {
        for (l in d.levels) {
            val h by animateFloatAsState(4f + l * 18f, label = "level")
            Box(Modifier.width(4.dp).height(h.dp).clip(RoundedCornerShape(2.dp)).background(c.accent))
        }
    }
}
