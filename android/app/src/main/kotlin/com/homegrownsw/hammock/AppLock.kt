package com.homegrownsw.hammock

import android.os.SystemClock
import android.view.WindowManager
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_STRONG
import androidx.biometric.BiometricManager.Authenticators.DEVICE_CREDENTIAL
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.filled.Fingerprint
import androidx.compose.material.icons.filled.LockOpen
import androidx.compose.material3.ButtonDefaults
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
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

    var asking by remember { mutableStateOf(false) }
    var opening by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    fun unlock() {
        if (asking || opening) return
        asking = true
        val prompt = BiometricPrompt(activity, ContextCompat.getMainExecutor(activity), object : BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                asking = false
                // The lock opens, then the cover goes.
                opening = true
                scope.launch {
                    delay(350)
                    locked = false
                    opening = false
                }
            }
            override fun onAuthenticationError(errorCode: Int, errString: CharSequence) { asking = false }
        })
        prompt.authenticate(BiometricPrompt.PromptInfo.Builder()
            .setTitle("Unlock Hammock")
            .setAllowedAuthenticators(BIOMETRIC_STRONG or DEVICE_CREDENTIAL)
            .build())
    }

    val covered = on && locked
    // For the panes' own dialogs and sheets, in modules that can't see LocalAppLocked.
    androidx.compose.runtime.SideEffect { com.homegrownsw.hammock.client.AppCover.locked.value = covered }
    Box(Modifier.fillMaxSize()) {
        // Locked: the app stays as it was beneath (so it comes back where you
        // were), but nothing reaches it, by touch, keyboard or TalkBack.
        Box(Modifier.fillMaxSize().then(if (covered) Modifier.clearAndSetSemantics { } else Modifier)) {
            CompositionLocalProvider(LocalAppLocked provides covered) { content() }
        }
        if (covered) {
            LaunchedEffect(Unit) { unlock() }
            LockScreen(asking, opening, ::unlock)
        }
    }
}

/** How this phone unlocks: its biometrics if it has them set up, else its screen lock. */
private fun biometrics(ctx: android.content.Context) =
    BiometricManager.from(ctx).canAuthenticate(BIOMETRIC_STRONG) == BiometricManager.BIOMETRIC_SUCCESS

/** The cover, as on the iPhone: the app's colour drifting behind a lock, Hammock, and the way to unlock. */
@Composable
private fun LockScreen(asking: Boolean, opening: Boolean, unlock: () -> Unit) {
    val ctx = LocalContext.current
    val bio = remember { biometrics(ctx) }
    val grow by animateFloatAsState(if (opening) 1.08f else 1f, spring(dampingRatio = 0.6f), label = "grow")
    // Dark behind the status bar: its icons light while the cover shows.
    val window = (ctx as? android.app.Activity)?.window
    // (After a frame: the theme sets them for the app's appearance, a frame in.)
    val bars = remember(window) { window?.let { androidx.core.view.WindowCompat.getInsetsController(it, it.decorView) } }
    var was by remember { mutableStateOf<Boolean?>(null) }
    LaunchedEffect(bars) {
        androidx.compose.runtime.withFrameNanos { }
        was = bars?.isAppearanceLightStatusBars
        bars?.isAppearanceLightStatusBars = false
    }
    DisposableEffect(bars) { onDispose { was?.let { bars?.isAppearanceLightStatusBars = it } } }
    val pulse = rememberInfiniteTransition(label = "pulse")
    val beat by pulse.animateFloat(1f, 0.55f, infiniteRepeatable(tween(900), RepeatMode.Reverse), label = "beat")
    Box(
        Modifier.fillMaxSize()
            .pointerInput(Unit) { awaitPointerEventScope { while (true) awaitPointerEvent().changes.forEach { it.consume() } } }
            .focusable(),
    ) {
        Backdrop(MaterialTheme.colorScheme.primary)
        Column(
            Modifier.fillMaxSize().systemBarsPadding().padding(horizontal = 24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Spacer(Modifier.weight(1f))
            Box(
                Modifier.size(128.dp).scale(grow).clip(CircleShape)
                    .background(Color.White.copy(alpha = 0.12f))
                    .border(1.dp, Color.White.copy(alpha = 0.22f), CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    if (opening) Icons.Default.LockOpen else Icons.Default.Lock, null,
                    Modifier.size(46.dp).alpha(if (asking) beat else 1f), tint = Color.White,
                )
            }
            Text(
                "Hammock", color = Color.White, fontSize = 40.sp, fontWeight = FontWeight.Bold,
                modifier = Modifier.padding(top = 28.dp),
            )
            // Locked goes without saying; unlocking, a welcome (its room kept,
            // so nothing moves as it shows).
            Text(
                "Welcome back", color = Color.White.copy(alpha = 0.75f), style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(top = 4.dp).alpha(if (opening) 1f else 0f),
            )
            Spacer(Modifier.weight(1f))
            val showButton = !asking && !opening
            Button(
                onClick = unlock, enabled = showButton,
                colors = ButtonDefaults.buttonColors(
                    containerColor = Color.White.copy(alpha = 0.16f), contentColor = Color.White,
                    disabledContainerColor = Color.Transparent, disabledContentColor = Color.Transparent,
                ),
                contentPadding = PaddingValues(horizontal = 22.dp, vertical = 14.dp),
                modifier = Modifier.padding(bottom = 40.dp),
            ) {
                Icon(if (bio) Icons.Default.Fingerprint else Icons.Default.Lock, null, Modifier.size(20.dp))
                Spacer(Modifier.width(8.dp))
                Text(if (bio) "Unlock" else "Unlock with Screen Lock", style = MaterialTheme.typography.titleSmall)
            }
        }
    }
}

/**
 * A slow drift of the theme's colour (the iPhone's mesh, shade for shade):
 * its brightest the colour itself, the rest darker; dimmed if the colour is
 * so light the white text wouldn't read. Still with animations off.
 */
@Composable
private fun Backdrop(accent: Color) {
    val c = if (accent.luminance() > 0.5f) accent.copy(red = accent.red * 0.6f, green = accent.green * 0.6f, blue = accent.blue * 0.6f) else accent
    fun shade(k: Float) = Color(c.red * k, c.green * k, c.blue * k)
    val drift = rememberInfiniteTransition(label = "drift")
    val t by drift.animateFloat(0f, (2 * Math.PI).toFloat(), infiniteRepeatable(tween(40_000, easing = LinearEasing)), label = "t")
    val still = android.provider.Settings.Global.getFloat(LocalContext.current.contentResolver, android.provider.Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f
    val s = if (still) 0f else t
    Canvas(Modifier.fillMaxSize()) {
        drawRect(Brush.verticalGradient(listOf(shade(0.19f), shade(0.4f), shade(0.12f))))
        val r = size.maxDimension * 0.75f
        drawCircle(
            Brush.radialGradient(listOf(shade(1f).copy(alpha = 0.85f), Color.Transparent),
                center = Offset(size.width * (0.5f + 0.12f * kotlin.math.sin(s * 3)), size.height * (0.5f + 0.1f * kotlin.math.sin(s * 4))), radius = r * 0.7f),
            radius = r * 0.7f, center = Offset(size.width * (0.5f + 0.12f * kotlin.math.sin(s * 3)), size.height * (0.5f + 0.1f * kotlin.math.sin(s * 4))),
        )
        val p = Offset(size.width * (0.15f + 0.08f * kotlin.math.sin(s * 2)), size.height * (0.3f + 0.07f * kotlin.math.sin(s * 5)))
        drawCircle(Brush.radialGradient(listOf(shade(0.64f).copy(alpha = 0.7f), Color.Transparent), center = p, radius = r * 0.5f), radius = r * 0.5f, center = p)
        val q = Offset(size.width * (0.85f + 0.08f * kotlin.math.sin(s * 5)), size.height * (0.75f + 0.07f * kotlin.math.sin(s * 3)))
        drawCircle(Brush.radialGradient(listOf(shade(0.58f).copy(alpha = 0.7f), Color.Transparent), center = q, radius = r * 0.5f), radius = r * 0.5f, center = q)
    }
}
