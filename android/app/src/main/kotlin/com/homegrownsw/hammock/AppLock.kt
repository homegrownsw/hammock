package com.homegrownsw.hammock

import android.os.SystemClock
import android.view.WindowManager
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_STRONG
import androidx.biometric.BiometricManager.Authenticators.DEVICE_CREDENTIAL
import androidx.biometric.BiometricPrompt
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.focusable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner

/**
 * Settings ▸ Privacy ▸ Require Unlock: the app covered until you unlock
 * it with your fingerprint, face or screen lock, again after it's been away
 * longer than Lock After. While it's on, the app's screen is hidden from
 * Recents and screenshots.
 */
/** Whether the app is covered by its lock: sheets and dialogs (their own windows) don't show then. */
val LocalAppLocked = androidx.compose.runtime.compositionLocalOf { false }

@Composable
fun AppLock(content: @Composable () -> Unit) {
    val activity = LocalContext.current as FragmentActivity
    val on by rememberBoolPref(Prefs.APP_LOCK, false)
    val lockAfter by rememberPref(Prefs.LOCK_AFTER, "0")
    var locked by remember { mutableStateOf(on) }
    var leftAt by remember { mutableLongStateOf(0L) }
    val owner = LocalLifecycleOwner.current

    LaunchedEffect(on) {
        if (on) activity.window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        else { activity.window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE); locked = false }
    }
    DisposableEffect(owner, on) {
        val obs = LifecycleEventObserver { _, e ->
            if (!on) return@LifecycleEventObserver
            when (e) {
                Lifecycle.Event.ON_STOP -> leftAt = SystemClock.elapsedRealtime()
                Lifecycle.Event.ON_START -> {
                    val away = (SystemClock.elapsedRealtime() - leftAt) / 1000
                    if (leftAt > 0 && away >= (lockAfter.toLongOrNull() ?: 0)) locked = true
                }
                else -> {}
            }
        }
        owner.lifecycle.addObserver(obs)
        onDispose { owner.lifecycle.removeObserver(obs) }
    }

    fun unlock() {
        val prompt = BiometricPrompt(activity, ContextCompat.getMainExecutor(activity), object : BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) { locked = false }
        })
        prompt.authenticate(BiometricPrompt.PromptInfo.Builder()
            .setTitle("Unlock Hammock")
            .setAllowedAuthenticators(BIOMETRIC_STRONG or DEVICE_CREDENTIAL)
            .build())
    }

    val covered = on && locked
    Box(Modifier.fillMaxSize()) {
        // Locked: the app stays as it was beneath (so it comes back where you
        // were), but nothing reaches it, by touch, keyboard or TalkBack.
        Box(Modifier.fillMaxSize().then(if (covered) Modifier.clearAndSetSemantics { } else Modifier)) {
            CompositionLocalProvider(LocalAppLocked provides covered) { content() }
        }
        if (covered) {
            LaunchedEffect(Unit) { unlock() }
            Column(
                Modifier.fillMaxSize().background(MaterialTheme.colorScheme.surface)
                    .pointerInput(Unit) { awaitPointerEventScope { while (true) awaitPointerEvent().changes.forEach { it.consume() } } }
                    .focusable(),
                verticalArrangement = Arrangement.spacedBy(20.dp, Alignment.CenterVertically),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Icon(Icons.Default.Lock, null, Modifier.size(48.dp), tint = MaterialTheme.colorScheme.primary)
                Text("Hammock is locked", style = MaterialTheme.typography.titleLarge)
                Button(onClick = ::unlock) { Text("Unlock") }
            }
        }
    }
}
