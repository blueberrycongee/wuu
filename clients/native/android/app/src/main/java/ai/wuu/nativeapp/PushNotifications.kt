package ai.wuu.nativeapp

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.app.NotificationManagerCompat
import com.google.android.gms.common.ConnectionResult
import com.google.android.gms.common.GoogleApiAvailability
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions
import com.google.firebase.messaging.FirebaseMessaging
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

object PushPlatform {
    val configured get() = listOf(BuildConfig.WUU_FIREBASE_APP_ID, BuildConfig.WUU_FIREBASE_API_KEY,
        BuildConfig.WUU_FIREBASE_PROJECT_ID, BuildConfig.WUU_FIREBASE_SENDER_ID).all { it.isNotBlank() }
    fun initialize(context: Context): FirebaseMessaging {
        check(configured) { "此构建未配置 Firebase 推送" }
        if (FirebaseApp.getApps(context).isEmpty()) {
            FirebaseApp.initializeApp(context, FirebaseOptions.Builder()
                .setApplicationId(BuildConfig.WUU_FIREBASE_APP_ID).setApiKey(BuildConfig.WUU_FIREBASE_API_KEY)
                .setProjectId(BuildConfig.WUU_FIREBASE_PROJECT_ID).setGcmSenderId(BuildConfig.WUU_FIREBASE_SENDER_ID).build())
        }
        context.getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel("tasks", "任务提醒", NotificationManager.IMPORTANCE_DEFAULT))
        // Foreground explicitly retrieves the current token. Never start registration before consent.
        return FirebaseMessaging.getInstance().also { it.isAutoInitEnabled = false }
    }
}

class WuuApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        if (!PushPlatform.configured) return
        // A background FCM delivery may start a fresh process without an Activity.
        runCatching {
            val vault = Vault(this)
            val session = vault.read("session")?.let(AccountSession::decode) ?: return
            val consent = vault.read("push-consent") ?: return
            if (PushConsent.matches(session, consent) && consent.optBoolean("enabled")) PushPlatform.initialize(this)
        }
    }
}

class PushNotifications(private val context: Context) {
    var enabled by mutableStateOf<Boolean?>(null); private set
    var supported by mutableStateOf(false); private set
    var busy by mutableStateOf(false); private set
    var error by mutableStateOf<String?>(null)
    private var session: AccountSession? = null
    private val vault = Vault(context)
    val loginKey get() = session?.let(PushConsent::key)
    fun bind(value: AccountSession?) {
        if (session?.token == value?.token && session?.server == value?.server) { session = value; return }
        session = value; enabled = null; supported = false; error = null
        if (value == null) {
            context.getSystemService(NotificationManager::class.java).cancelAll()
            vault.delete("push-consent")
        }
    }
    fun mayOpenNotification(): Boolean {
        val session = session ?: return false
        val consent = vault.read("push-consent") ?: return false
        return PushConsent.matches(session, consent) && consent.optBoolean("enabled")
    }
    suspend fun refresh() {
        if (busy) return
        val session = session ?: return
        busy = true; error = null
        try {
            val api = AccountAPI(session.server)
            val config = api.request("/config"); val status = api.pushStatus(session.token)
            if (this.session != session) return
            supported = config.optJSONArray("push_platforms")?.let { values -> (0 until values.length()).any { values.optString(it) == "android" } } == true
            enabled = status.getBoolean("enabled")
            vault.read("push-consent")?.takeIf { PushConsent.matches(session, it) }?.let { consent ->
                if (!consent.optBoolean("enabled")) {
                    if (status.getBoolean("enabled")) api.setPush(session.token, "android", null)
                    if (this.session == session) enabled = false
                } else if (PushPlatform.configured && supported) register(session)
            }
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { if (this.session == session) error = e.message }
        finally { busy = false }
    }
    suspend fun setEnabled(value: Boolean) {
        if (busy) return
        val session = session ?: return
        busy = true; error = null
        try {
            if (value) {
                check(PushPlatform.configured && supported) { "当前 App 或服务器未配置通知" }
                vault.write("push-consent", PushConsent.encode(session, true))
                register(session)
            } else {
                vault.write("push-consent", PushConsent.encode(session, false))
                context.getSystemService(NotificationManager::class.java).cancelAll()
                AccountAPI(session.server).setPush(session.token, "android", null)
                if (this.session == session) enabled = false
            }
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { if (this.session == session) error = e.message }
        finally { busy = false }
    }
    private suspend fun register(session: AccountSession) {
        check(NotificationManagerCompat.from(context).areNotificationsEnabled()) { "系统通知未允许，请在系统设置中开启" }
        check(GoogleApiAvailability.getInstance().isGooglePlayServicesAvailable(context) == ConnectionResult.SUCCESS) { "此设备没有可用的 Google Play 服务" }
        val messaging = PushPlatform.initialize(context)
        val token = withTimeoutOrNull(20_000) { suspendCancellableCoroutine<String> { continuation ->
            messaging.token.addOnCompleteListener { task ->
                if (continuation.isActive) {
                    if (task.isSuccessful) continuation.resume(task.result)
                    else continuation.resumeWithException(task.exception ?: IllegalStateException("Firebase 通知注册失败"))
                }
            }
        } } ?: throw IllegalStateException("Firebase 通知注册超时，请重试")
        if (this.session != session) return
        AccountAPI(session.server).setPush(session.token, "android", token)
        if (this.session == session) enabled = true
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable fun PushSettingsSheet(push: PushNotifications, close: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var requestedFor by remember { mutableStateOf<String?>(null) }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (requestedFor == push.loginKey) {
            if (granted) scope.launch { push.setEnabled(true) }
            else push.error = "系统通知未允许，请在系统设置中开启"
        }
        requestedFor = null
    }
    LaunchedEffect(push) { push.refresh() }
    ModalBottomSheet(onDismissRequest = close) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("通知", style = MaterialTheme.typography.titleLarge)
            Text(when (push.enabled) { true -> "服务器通知已开启"; false -> "服务器通知未开启"; null -> "通知状态尚未确认" })
            if (!PushPlatform.configured) Text("此构建未配置 Firebase 推送。")
            else if (!push.supported) Text("此账号服务器未配置 Firebase 推送。")
            Text("任务完成或需要输入时提醒。通知只包含通用提示和电脑标识，不包含对话内容。关闭失败时会在下次打开 App 重试。")
            TextButton(enabled = !push.busy && requestedFor == null && PushPlatform.configured && push.supported, onClick = {
                if (Build.VERSION.SDK_INT >= 33 && !NotificationManagerCompat.from(context).areNotificationsEnabled()) {
                    requestedFor = push.loginKey; permission.launch(android.Manifest.permission.POST_NOTIFICATIONS)
                } else scope.launch { push.setEnabled(true) }
            }) { Text("开启或重新注册") }
            TextButton(enabled = !push.busy, onClick = { scope.launch { push.setEnabled(false) } }) { Text("关闭通知") }
            if (push.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            push.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            TextButton(onClick = { context.startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName)) }) { Text("系统通知设置") }
        }
    }
}
