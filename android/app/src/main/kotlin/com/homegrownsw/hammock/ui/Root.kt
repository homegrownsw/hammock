package com.homegrownsw.hammock.ui

import androidx.activity.compose.BackHandler
import com.homegrownsw.hammock.LocalAppLocked
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.filled.Devices
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.ViewAgenda
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.VerticalDivider
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffold
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffoldDefaults
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteType
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.OpenRequest
import com.homegrownsw.hammock.Session
import com.homegrownsw.hammock.rememberBoolPref
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.PairingCodes
import com.homegrownsw.hammock.media.ScreenShare
import com.homegrownsw.hammock.media.ScreenShareBar
import com.homegrownsw.hammock.media.ScreenViewer
import androidx.compose.ui.semantics.clearAndSetSemantics
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.launch

/** The four tabs, as on the iPhone. */
enum class Dest(val label: String, val icon: ImageVector) {
    Sessions("Sessions", Icons.AutoMirrored.Filled.Chat),
    Groups("Groups", Icons.Default.ViewAgenda),
    Devices("Devices", Icons.Default.Devices),
    Settings("Settings", Icons.Default.Settings),
}

/**
 * Where the app is: the tab shown, and what's open over its list (a pane,
 * a tab, a schedule), as a stack of routes: "pane:ID", "tab:ID",
 * "schedules", "schedule:ID". Back pops it.
 */
class Nav {
    var dest by mutableStateOf(Dest.Sessions)
    val stack = mutableStateListOf<String>()
    fun open(route: String) {
        stack.clear()
        stack.add(route)
    }
    fun push(route: String) { stack.add(route) }
    fun pop() { if (stack.isNotEmpty()) stack.removeAt(stack.lastIndex) }
}

/** The app: pairing until a computer is paired, then its workspace. */
@Composable
fun Root(session: Session, opening: OpenRequest?, opened: () -> Unit) {
    val model by session.model.collectAsStateWithLifecycle()
    val offer by session.offered.collectAsStateWithLifecycle()
    val m = model
    if (m == null) {
        PairingScreen(session, firstRun = true, onDemo = { DemoMac.start(session) })
    } else {
        val link by m.link.collectAsStateWithLifecycle()
        if ((link as? com.homegrownsw.hammock.client.Link.Failed)?.unpaired == true) Removed(session)
        else Workspace(session, m, opening, opened)
    }
    offer?.let { o ->
        if (!LocalAppLocked.current) AlertDialog(
            onDismissRequest = session::dismissOffer,
            title = { Text("Pair with ${o.pairing.hostName.removeSuffix(".local")}?") },
            text = {
                Text("Only pair with a computer of your own. Check its certificate matches what it shows: ${PairingCodes.shortFingerprint(o.pairing.certSha256)}")
            },
            confirmButton = { TextButton(onClick = { session.pair(o.code, switching = model == null) }) { Text("Pair") } },
            dismissButton = { TextButton(onClick = session::dismissOffer) { Text("Cancel") } },
        )
    }
    UpdatePrompt()
}

/** The computer shown removed this phone (Settings ▸ Devices there): pair again with a new code, or forget it. */
@Composable
private fun Removed(session: Session) {
    val host by session.host.collectAsStateWithLifecycle()
    val name = host?.shortName?.ifEmpty { null } ?: "Your computer"
    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.surface) {
        Column(Modifier.fillMaxSize().statusBarsPadding()) {
            Column(Modifier.padding(horizontal = 20.dp).padding(top = 32.dp)) {
                Text("$name removed this phone", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
                Text("Pair it again with a new pairing code to connect.", style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 8.dp))
                host?.let { h -> TextButton(onClick = { session.forget(h) }, Modifier.padding(top = 4.dp)) { Text("Forget $name") } }
            }
            Box(Modifier.weight(1f)) { PairingScreen(session, firstRun = false) }
        }
    }
}

/** A newer release (UpdateCheck), offered once: Download, What's New, or Later (it stays in Settings ▸ About). */
@Composable
private fun UpdatePrompt() {
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val update by com.homegrownsw.hammock.UpdateCheck.available.collectAsStateWithLifecycle()
    var answered by remember { mutableStateOf(0) }
    val u = update ?: return
    if (answered == u.build || !com.homegrownsw.hammock.UpdateCheck.shouldPrompt(ctx, u) || LocalAppLocked.current) return
    fun done() {
        com.homegrownsw.hammock.UpdateCheck.answered(ctx, u)
        answered = u.build
    }
    AlertDialog(
        onDismissRequest = ::done,
        title = { Text("Hammock ${u.version} is available") },
        text = { Text("You have ${com.homegrownsw.hammock.BuildConfig.VERSION_NAME}. Download the new version, then open it to install it over this one: your computers and settings stay.") },
        confirmButton = { TextButton(onClick = { done(); com.homegrownsw.hammock.UpdateCheck.download(ctx, u) }) { Text("Download") } },
        dismissButton = {
            Row {
                if (u.notes.isNotEmpty()) TextButton(onClick = { com.homegrownsw.hammock.UpdateCheck.whatsNew(ctx, u) }) { Text("What's New") }
                TextButton(onClick = ::done) { Text("Later") }
            }
        },
    )
}

@Composable
private fun Workspace(session: Session, model: AppModel, opening: OpenRequest?, opened: () -> Unit) {
    val nav = remember(model) { Nav() }
    val ws by model.workspace.collectAsStateWithLifecycle()
    // Messages as snackbars: TalkBack reads them, and they swipe away. A new
    // one replaces the one showing.
    val snackbars = remember { SnackbarHostState() }
    LaunchedEffect(model) {
        model.toast.filterNotNull().collectLatest { t ->
            model.toastShown(t)
            snackbars.showSnackbar(t.text)
        }
    }
    val wide = LocalConfiguration.current.screenWidthDp >= 840
    val showingDetail = nav.stack.isNotEmpty() && (nav.dest == Dest.Sessions || nav.dest == Dest.Groups)
    var lastDest by rememberSaveable { mutableStateOf(Dest.Sessions.name) }
    LaunchedEffect(lastDest) { nav.dest = Dest.valueOf(lastDest) }

    BackHandler(nav.stack.isNotEmpty()) { nav.pop() }

    // The Hammock Key: this phone's approval key, sent on every connect so the
    // computer can let it approve; a request shown here when it may.
    val link by model.link.collectAsStateWithLifecycle()
    val host by session.host.collectAsStateWithLifecycle()
    LaunchedEffect(link) {
        if (link == com.homegrownsw.hammock.client.Link.Connected) model.reportPresence(emptyList(), true)
        if (link == com.homegrownsw.hammock.client.Link.Connected && !model.isDemo) {
            val key = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) { com.homegrownsw.hammock.ApprovalKey.publicKey() }
            if (key != null) model.send {
                setSetApprovalKey(hammock.v1.Hammock.SetApprovalKey.newBuilder().setPublicKey(com.google.protobuf.ByteString.copyFrom(key)).build())
            }
        }
    }
    // Notifications need asking for (Android 13 and later): once, when a computer's paired.
    val ctx = androidx.compose.ui.platform.LocalContext.current
    val askNotify = androidx.activity.compose.rememberLauncherForActivityResult(
        androidx.activity.result.contract.ActivityResultContracts.RequestPermission(),
    ) { }
    LaunchedEffect(link, host?.deviceID) {
        if (link != com.homegrownsw.hammock.client.Link.Connected || model.isDemo || host?.deviceID == null) return@LaunchedEffect
        val prefs = com.homegrownsw.hammock.Prefs.of(ctx)
        val granted = androidx.core.content.ContextCompat.checkSelfPermission(ctx, android.Manifest.permission.POST_NOTIFICATIONS) ==
            android.content.pm.PackageManager.PERMISSION_GRANTED
        if (!granted && !prefs.getBoolean("askedNotify", false)) {
            prefs.edit().putBoolean("askedNotify", true).apply()
            askNotify.launch(android.Manifest.permission.POST_NOTIFICATIONS)
        }
    }
    var dismissedKeys by remember { mutableStateOf(setOf<String>()) }
    val me = host?.deviceID
    val keyRequest = if (me != null && ws.hammockKey.approversList.any { it.deviceId == me })
        ws.hammockKeyRequests.firstOrNull { it.id !in dismissedKeys } else null
    keyRequest?.let { r ->
        HammockKeySheet(model, ws, r, host?.shortName ?: "your computer") { dismissedKeys = dismissedKeys + r.id }
    }

    var answer by remember { mutableStateOf<com.homegrownsw.hammock.NotificationAnswer?>(null) }
    // A notification opened: its pane once the computer's state is here; Needs You at the top of Sessions.
    LaunchedEffect(opening, ws.panes.size) {
        val o = opening ?: return@LaunchedEffect
        if (o.hostID != null && session.host.value?.id != o.hostID) {
            session.hosts.value.firstOrNull { it.id == o.hostID }?.let { session.show(it) }
            return@LaunchedEffect
        }
        if (o.paneID != null) {
            if (ws.panes[o.paneID] == null) return@LaunchedEffect
            nav.dest = Dest.Sessions
            nav.open("pane:${o.paneID}")
        } else {
            nav.dest = Dest.Sessions
            nav.stack.clear()
        }
        o.answer?.let { answer = it }
        opened()
    }
    // A notification's answer (with Require Unlock on, it opened the app):
    // sent once the lock's open and the computer's connected.
    val locked = LocalAppLocked.current
    LaunchedEffect(answer, locked, link) {
        val a = answer ?: return@LaunchedEffect
        if (locked || link != com.homegrownsw.hammock.client.Link.Connected || session.host.value?.id != a.host) return@LaunchedEffect
        answer = null
        a.answered(ctx)
        model.scope.launch { // (not this effect's: clearing answer ends it)
            if (a.send(model) != com.homegrownsw.hammock.client.AppModel.PromptAnswer.ANSWERED) model.showToast("Couldn't answer: choose here instead")
        }
    }

    val needsYou = ws.badgeCount
    // A computer's screen: over everything while it's open, a bar on every
    // screen while it's minimised (above the navigation bar, or under a
    // screen's top bar where that's hidden). Another computer shown ends it.
    val share = remember(model) { ScreenShare() }
    val fullScreen = share.state.isOpen
    val barAtTop = showingDetail && !wide
    LaunchedEffect(opening) { if (opening?.paneID != null) share.minimise() }
    val layout = if (fullScreen || (showingDetail && !wide)) NavigationSuiteType.None
    else NavigationSuiteScaffoldDefaults.calculateFromAdaptiveInfo(androidx.compose.material3.adaptive.currentWindowAdaptiveInfo())

    val closer = remember(model) { Closer() }
    val warnBusy by rememberBoolPref(com.homegrownsw.hammock.Prefs.WARN_CLOSE_BUSY, true)
    androidx.compose.runtime.SideEffect { closer.warnBusy = warnBusy }
    androidx.compose.runtime.CompositionLocalProvider(LocalModel provides model, LocalCloser provides closer,
        LocalScreenBar provides ScreenBar(share, barAtTop)) {
    // The link to the computer, for the banner on every screen.
    LinkWatcher(model, host?.shortName?.ifEmpty { null } ?: "your computer") {
    Box(Modifier.fillMaxSize()) {
    NavigationSuiteScaffold(
        // Under the open screen: not read out, nor touched.
        modifier = if (fullScreen) Modifier.clearAndSetSemantics {} else Modifier,
        layoutType = layout,
        navigationSuiteItems = {
            Dest.entries.forEach { d ->
                item(
                    selected = nav.dest == d,
                    onClick = {
                        if (nav.dest == d) nav.stack.clear()
                        nav.dest = d
                        lastDest = d.name
                    },
                    icon = {
                        if (d == Dest.Sessions && needsYou > 0) BadgedBox(badge = { Badge { Text("$needsYou") } }) { Icon(d.icon, null) }
                        else Icon(d.icon, null)
                    },
                    label = { Text(d.label) },
                )
            }
        },
    ) {
        Column(Modifier.fillMaxSize()) {
        Box(Modifier.fillMaxWidth().weight(1f)) {
            when (nav.dest) {
                Dest.Sessions, Dest.Groups -> ListDetail(wide, nav, model) {
                    if (nav.dest == Dest.Sessions) SessionsScreen(session, model, nav) else GroupsScreen(session, model, nav)
                }
                Dest.Devices -> DevicesScreen(session, model, share)
                Dest.Settings -> SettingsScreen(session, model)
            }
            SnackbarHost(snackbars, Modifier.align(Alignment.BottomCenter)) { data ->
                key(data) {
                    val swipe = rememberSwipeToDismissBoxState(confirmValueChange = {
                        if (it != SwipeToDismissBoxValue.Settled) data.dismiss()
                        true
                    })
                    SwipeToDismissBox(swipe, backgroundContent = {}) {
                        Snackbar(data, Modifier.padding(16.dp))
                    }
                }
            }
        }
        // Above the navigation bar.
        if (!barAtTop) ScreenShareBar(share, top = false)
        }
    }
    if (share.state.isShowing) key(share.state.session) { ScreenViewer(model, share) }
    }
    }
    closer.Dialog()
    }
}

/**
 * A list and what it opens: side by side on a wide screen (a tablet, an
 * open foldable), the list kept beside; on a phone, what's open covers the
 * list, and Back returns to it.
 */
@Composable
private fun ListDetail(wide: Boolean, nav: Nav, model: AppModel, list: @Composable () -> Unit) {
    val route = nav.stack.lastOrNull()
    if (wide) {
        Row(Modifier.fillMaxSize()) {
            Box(Modifier.width(380.dp).fillMaxHeight()) { list() }
            VerticalDivider()
            // The list beside has the connection banner.
            Box(Modifier.weight(1f).fillMaxHeight()) { androidx.compose.runtime.CompositionLocalProvider(LocalShowsLinkBanner provides false) {
                if (route == null) Placeholder(if (nav.dest == Dest.Sessions) "No Session Selected" else "No Tab Selected")
                else Detail(route, model, nav, showBack = nav.stack.size > 1)
            } }
        }
    } else {
        AnimatedContent(
            targetState = route,
            transitionSpec = {
                val forward = targetState != null && (initialState == null || nav.stack.size > 1)
                if (forward) (slideInHorizontally { it } + fadeIn()) togetherWith (slideOutHorizontally { -it / 4 } + fadeOut())
                else (slideInHorizontally { -it / 4 } + fadeIn()) togetherWith (slideOutHorizontally { it } + fadeOut())
            },
            label = "detail",
        ) { r ->
            if (r == null) list() else Detail(r, model, nav, showBack = true)
        }
    }
}

@Composable
private fun Detail(route: String, model: AppModel, nav: Nav, showBack: Boolean) {
    val (kind, id) = route.split(':', limit = 2).let { it[0] to it.getOrElse(1) { "" } }
    when (kind) {
        "pane" -> PaneScreen(model, id, nav, showBack)
        "tab" -> TabScreen(model, id, nav, showBack)
        "schedules" -> SchedulesScreen(model, nav, showBack)
        "schedule" -> ScheduleScreen(model, id, nav, showBack)
        else -> Placeholder("")
    }
}

@Composable
fun Placeholder(text: String) {
    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.surface) {
        Box(contentAlignment = Alignment.Center) {
            Text(text, color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.titleMedium)
        }
    }
}
