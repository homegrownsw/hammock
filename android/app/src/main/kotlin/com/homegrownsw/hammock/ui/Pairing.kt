package com.homegrownsw.hammock.ui

import android.Manifest
import android.content.pm.PackageManager
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.QrCodeScanner
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.google.mlkit.vision.barcode.BarcodeScannerOptions
import com.google.mlkit.vision.barcode.BarcodeScanning
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.common.InputImage
import com.homegrownsw.hammock.Session
import java.util.concurrent.Executors

/**
 * Pairing with a computer: scan the code it shows (Hammock ▸ Settings ▸
 * Devices on a Mac, `hmk pair` on a server), or paste its link. First run
 * also offers the demo.
 */
@Composable
fun PairingScreen(session: Session, firstRun: Boolean, onDemo: (() -> Unit)? = null, onDone: (() -> Unit)? = null) {
    val error by session.pairingError.collectAsStateWithLifecycle()
    val pairing by session.pairing.collectAsStateWithLifecycle()
    var code by remember { mutableStateOf("") }
    var scanning by remember { mutableStateOf(false) }
    val ctx = LocalContext.current
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted -> scanning = granted }

    // Stays until the pairing's done: one that fails says why here.
    var started by remember { mutableStateOf(false) }
    LaunchedEffect(started, pairing, error) {
        // (The session's own values: the collected ones may be a frame behind.)
        if (started && !session.pairing.value) {
            started = false
            if (session.pairingError.value == null) onDone?.invoke()
        }
    }

    fun pair(c: String) {
        scanning = false
        session.pair(c, switching = true)
        started = true
    }

    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.surface) {
        Column(
            Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(vertical = 16.dp).padding(top = if (firstRun) 48.dp else 0.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            // The iPhone's form: what to do on the computer, then the scanner, then the link, each in its card.
            Column(Modifier.widthIn(max = 560.dp)) {
                if (firstRun) Text("Connect to your computer", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold,
                    modifier = Modifier.padding(horizontal = CardInset + 4.dp, vertical = 8.dp))
                GroupedCard {
                    Text(buildAnnotatedString {
                        append("On your Mac, open Hammock ▸ Settings ▸ Devices, turn on remote access and choose ")
                        withStyle(SpanStyle(fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.onSurface)) { append("Show Pairing Code") }
                        append(". On a server, run ")
                        withStyle(SpanStyle(fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.onSurface)) { append("hmk pair") }
                        append(".")
                    }, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(16.dp))
                }

                SectionHeader("Scan the code")
                GroupedCard {
                    if (scanning) {
                        QrScanner(Modifier.fillMaxWidth().height(280.dp)) { pair(it) }
                        CardDivider()
                        ValueRow("Stop Scanning", accent = true, onClick = { scanning = false }) {}
                    } else {
                        Column(
                            Modifier.fillMaxWidth().height(200.dp), verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
                            horizontalAlignment = Alignment.CenterHorizontally,
                        ) {
                            Icon(Icons.Default.QrCodeScanner, null, Modifier.size(48.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                            Button(onClick = {
                                if (ContextCompat.checkSelfPermission(ctx, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) scanning = true
                                else permission.launch(Manifest.permission.CAMERA)
                            }, enabled = !pairing) { Text("Scan Pairing Code") }
                        }
                    }
                }

                SectionHeader("Or paste the link")
                GroupedCard {
                    BasicTextField(
                        code, { code = it.trim() },
                        textStyle = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurface),
                        cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                        modifier = Modifier.fillMaxWidth().padding(16.dp),
                        decorationBox = { inner ->
                            if (code.isEmpty()) Text("hammock://pair/…", style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace,
                                color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f))
                            inner()
                        },
                    )
                    CardDivider()
                    val canPair = code.isNotEmpty() && !pairing
                    ValueRow("Pair", accent = canPair, onClick = if (canPair) ({ pair(code) }) else null) {}
                }
                if (pairing) {
                    Spacer(Modifier.size(20.dp))
                    GroupedCard {
                        Row(Modifier.padding(start = 16.dp, end = 8.dp, top = 4.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                            Text("Pairing…", Modifier.weight(1f), color = MaterialTheme.colorScheme.onSurfaceVariant)
                            TextButton(onClick = session::cancelPairing) { Text("Cancel") }
                        }
                    }
                }
                error?.let {
                    Spacer(Modifier.size(20.dp))
                    GroupedCard { Text(it, Modifier.padding(16.dp), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium) }
                }

                if (firstRun && onDemo != null) {
                    // A side door, not the way in: a footnote, under everything.
                    Column(Modifier.fillMaxWidth().padding(top = 24.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text("No Mac to hand?", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            TextButton(onClick = onDemo) { Text("See a demo") }
                        }
                        TextButton(onClick = {
                            ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://homegrownsw.com/hammock/")))
                        }) { Text("Hammock for Mac is free at homegrownsw.com/hammock", style = MaterialTheme.typography.bodySmall) }
                    }
                }
            }
        }
    }
}

/**
 * The camera, reading QR codes with ML Kit's model bundled in the app (no
 * Google Play services needed), until one is a Hammock pairing link.
 */
@androidx.annotation.OptIn(androidx.camera.core.ExperimentalGetImage::class)
@Composable
fun QrScanner(modifier: Modifier, onCode: (String) -> Unit) {
    val ctx = LocalContext.current
    val owner = LocalLifecycleOwner.current
    val executor = remember { Executors.newSingleThreadExecutor() }
    var done by remember { mutableStateOf(false) }
    val scanner = remember {
        BarcodeScanning.getClient(BarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_QR_CODE).build())
    }
    DisposableEffect(Unit) { onDispose { scanner.close(); executor.shutdown() } }
    AndroidView(
        modifier = modifier,
        factory = { c ->
            val view = PreviewView(c)
            val future = ProcessCameraProvider.getInstance(c)
            future.addListener({
                val provider = future.get()
                val preview = Preview.Builder().build().also { it.surfaceProvider = view.surfaceProvider }
                val analysis = ImageAnalysis.Builder().setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST).build()
                analysis.setAnalyzer(executor) { proxy ->
                    val media = proxy.image
                    if (media == null || done) { proxy.close(); return@setAnalyzer }
                    scanner.process(InputImage.fromMediaImage(media, proxy.imageInfo.rotationDegrees))
                        .addOnSuccessListener { codes ->
                            val link = codes.mapNotNull { it.rawValue }.firstOrNull { it.startsWith("hammock://pair/") }
                            if (link != null && !done) {
                                done = true
                                view.performHapticFeedback(android.view.HapticFeedbackConstants.CONFIRM)
                                onCode(link)
                            }
                        }
                        .addOnCompleteListener { proxy.close() }
                }
                runCatching {
                    provider.unbindAll()
                    provider.bindToLifecycle(owner, CameraSelector.DEFAULT_BACK_CAMERA, preview, analysis)
                }
            }, ContextCompat.getMainExecutor(c))
            view
        },
    )
}
