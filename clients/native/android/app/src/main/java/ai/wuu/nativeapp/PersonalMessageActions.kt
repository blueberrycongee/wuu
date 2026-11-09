package ai.wuu.nativeapp

import androidx.activity.compose.BackHandler
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInWindow
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import kotlinx.coroutines.launch
import kotlin.math.roundToInt

internal data class PersonalMessageSelection(val id: Long, val text: String, val user: Boolean, val bounds: Rect)

@Stable internal class PersonalMessageActions {
    var selected by mutableStateOf<PersonalMessageSelection?>(null)
    fun open(value: PersonalMessageSelection) { if (selected == null && value.text.isNotBlank() && value.bounds.width > 0) selected = value }
}

internal val LocalPersonalMessageActions = staticCompositionLocalOf<PersonalMessageActions?> { null }

/** Owns only Personal's transient UI. Context changes and lifecycle loss invalidate the selection. */
@Composable internal fun PersonalMessageActionsHost(context: Any, reply: ((String) -> Unit)?, content: @Composable () -> Unit) {
    val actions = remember(context) { PersonalMessageActions() }
    var selectionText by remember(context) { mutableStateOf<String?>(null) }
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner, actions) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_PAUSE) { actions.selected = null; selectionText = null } }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer); actions.selected = null }
    }
    CompositionLocalProvider(LocalPersonalMessageActions provides actions) {
        Box(Modifier.fillMaxSize()) {
            Box(if (actions.selected != null) Modifier.clearAndSetSemantics {} else Modifier) { content() }
            actions.selected?.let { selected ->
                key(selected.id) {
                    PersonalMessageMenu(selected, close = { actions.selected = null },
                        select = { selectionText = selected.text }, reply = reply)
                }
            }
        }
    }
    selectionText?.let { text ->
        AlertDialog(onDismissRequest = { selectionText = null }, title = { Text("选择文本") },
            text = { SelectionContainer { Text(text, Modifier.heightIn(max = 440.dp).verticalScroll(rememberScrollState())) } },
            confirmButton = { TextButton(onClick = { selectionText = null }) { Text("完成") } })
    }
}

@Composable private fun PersonalMessageMenu(selected: PersonalMessageSelection, close: () -> Unit, select: () -> Unit, reply: ((String) -> Unit)?) {
    val progress = remember { Animatable(0f) }
    val dim = remember { Animatable(0f) }
    var closing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val clipboard = LocalClipboardManager.current
    val density = LocalDensity.current
    val overlaySurface = if (MaterialTheme.colorScheme.surface.luminance() < 0.5f) MaterialTheme.colorScheme.surfaceContainerHigh else MaterialTheme.colorScheme.surface
    var origin by remember { mutableStateOf(Offset.Zero) }
    val end: (() -> Unit) -> Unit = { action ->
        if (!closing) {
            closing = true
            scope.launch {
                launch { dim.animateTo(0f, tween(140)) }
                progress.animateTo(0f, tween(200))
                close(); action()
            }
        }
    }
    BackHandler { end {} }
    LaunchedEffect(Unit) {
        launch { dim.animateTo(1f, tween(140)) }
        progress.animateTo(1f, tween(480, easing = Easing { fraction -> val t = fraction * 0.48f; (1f - (1f + 20f * t) * kotlin.math.exp(-20f * t)) }))
    }
    // Compose Animatable honors the system animator duration scale, including zero.
    Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.51f * dim.value))
        .clickable(onClickLabel = "关闭消息操作") { end {} })
    BoxWithConstraints(Modifier.fillMaxSize().safeDrawingPadding().imePadding().padding(12.dp)
        .onGloballyPositioned { origin = it.positionInWindow() }) {
        val source = selected.bounds.translate(-origin)
        val availableHeight = with(density) { maxHeight.toPx() }
        val availableWidth = with(density) { maxWidth.toPx() }
        val row = 48.dp * density.fontScale.coerceAtLeast(1f)
        val requestedTools = with(density) { (row * (if (reply == null) 2 else 3) + 16.dp).toPx() }
        val toolsHeight = requestedTools.coerceAtMost((availableHeight - with(density) { 68.dp.toPx() }).coerceAtLeast(44f))
        val gap = with(density) { 24.dp.toPx() }
        val messageHeight = source.height.coerceAtMost((availableHeight - toolsHeight - gap).coerceAtLeast(1f))
        val width = source.width.coerceAtMost(availableWidth)
        val x = source.left.coerceIn(0f, (availableWidth - width).coerceAtLeast(0f))
        val y = source.top.coerceIn(0f, (availableHeight - messageHeight - gap - toolsHeight).coerceAtLeast(0f))
        val p = progress.value
        Surface(Modifier.offset { IntOffset(x.roundToInt(), (source.top + (y - source.top) * p).roundToInt()) }
            .width(with(density) { width.toDp() }).height(with(density) { messageHeight.toDp() }),
            shape = RoundedCornerShape(18.dp), color = if (selected.user) Color(0xFF3566F0) else overlaySurface,
            contentColor = if (selected.user) Color.White else MaterialTheme.colorScheme.onSurface) {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                PersonalMessageText(selected.text, selected.user)
            }
        }
        val menuWidth = with(density) { (row * 4.2f).coerceAtLeast(200.dp).toPx() }.coerceAtMost(availableWidth)
        val menuX = (if (selected.user) x + width - menuWidth else x).coerceIn(0f, (availableWidth - menuWidth).coerceAtLeast(0f))
        Surface(Modifier.offset { IntOffset(menuX.roundToInt(), (y + messageHeight + gap + with(density) { 30.dp.toPx() } * (1 - p)).roundToInt()) }
            .width(with(density) { menuWidth.toDp() }).height(with(density) { toolsHeight.toDp() })
            .graphicsLayer { alpha = dim.value }, shape = RoundedCornerShape(20.dp), color = overlaySurface) {
            Column(Modifier.verticalScroll(rememberScrollState()).padding(vertical = 8.dp)) {
                PersonalMenuAction("复制", PersonalMenuIcon.Copy, row, !closing) { clipboard.setText(AnnotatedString(selected.text)); end {} }
                PersonalMenuAction("选择文本", PersonalMenuIcon.Select, row, !closing) { end(select) }
                if (reply != null) PersonalMenuAction("引用文本", PersonalMenuIcon.Reply, row, !closing) { end { reply(selected.text) } }
            }
        }
        var initialSize by remember { mutableStateOf<Pair<Float, Float>?>(null) }
        LaunchedEffect(availableWidth, availableHeight) {
            val size = availableWidth to availableHeight
            if (initialSize != null && initialSize != size) close() else initialSize = size
        }
    }
}

@Composable internal fun PersonalMessageText(content: String, user: Boolean, onLongClick: (() -> Unit)? = null) {
    Column(Modifier.padding(horizontal = if (user) 14.dp else 0.dp, vertical = if (user) 10.dp else 0.dp)) {
        MessageText(content, !user, selectable = false, onLongClick = onLongClick)
    }
}

private enum class PersonalMenuIcon { Copy, Select, Reply }

@Composable private fun PersonalMenuAction(title: String, icon: PersonalMenuIcon, row: Dp, enabled: Boolean, action: () -> Unit) {
    val color = MaterialTheme.colorScheme.onSurface
    Row(Modifier.fillMaxWidth().heightIn(min = row).clickable(enabled = enabled, onClick = action).padding(horizontal = 18.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(14.dp)) {
        Canvas(Modifier.size(18.dp)) {
            val path = Path().apply {
                when (icon) {
                    PersonalMenuIcon.Copy -> {
                        addRoundRect(androidx.compose.ui.geometry.RoundRect(3f, 7f, 13f, 18f, androidx.compose.ui.geometry.CornerRadius(2f)))
                        moveTo(7f, 7f); lineTo(7f, 4f); quadraticBezierTo(7f, 2f, 9f, 2f)
                        lineTo(16f, 2f); quadraticBezierTo(18f, 2f, 18f, 4f)
                        lineTo(18f, 12f); quadraticBezierTo(18f, 14f, 16f, 14f); lineTo(13f, 14f)
                    }
                    PersonalMenuIcon.Select -> {
                        addRoundRect(androidx.compose.ui.geometry.RoundRect(4f, 2f, 16f, 18f, androidx.compose.ui.geometry.CornerRadius(2f)))
                        moveTo(7f, 7f); lineTo(13f, 7f); moveTo(7f, 11f); lineTo(11f, 11f)
                    }
                    PersonalMenuIcon.Reply -> {
                        moveTo(3f, 5f); lineTo(3f, 10f); quadraticBezierTo(3f, 13f, 6f, 13f)
                        lineTo(17f, 13f); moveTo(13f, 9f); lineTo(17f, 13f); lineTo(13f, 17f)
                    }
                }
            }
            scale(size.width / 20, size.height / 20, pivot = Offset.Zero) { drawPath(path, color, style = Stroke(width = 1.5f, cap = StrokeCap.Round, join = StrokeJoin.Round)) }
        }
        Text(title, color = color, fontSize = 15.sp)
    }
}
