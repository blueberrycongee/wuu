package com.blueberrycongee.wuu;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.Map;
import java.util.function.BiConsumer;

public class BackgroundSyncService extends Service {
    private static final String CHANNEL = "background_sync";
    private static final int NOTIFICATION = 4101;
    // All access is on the main thread. Separate leases prevent an old bridge
    // disconnecting from stopping a replacement connection's service.
    private static final Map<String, BiConsumer<Boolean, String>> owners = new HashMap<>();
    private static BackgroundSyncService running;

    static boolean isRunning() { return running != null; }

    static void acquire(Context context, String id, BiConsumer<Boolean, String> callback) {
        owners.put(id, callback);
        if (running != null) {
            callback.accept(true, null);
            return;
        }
        try {
            Intent intent = new Intent(context, BackgroundSyncService.class);
            if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(intent);
            else context.startService(intent);
        } catch (RuntimeException error) {
            owners.remove(id);
            callback.accept(false, error.toString());
        }
    }

    static void release(Context context, String id) {
        BiConsumer<Boolean, String> callback = owners.remove(id);
        if (callback != null) callback.accept(false, "Background sync stopped");
        if (owners.isEmpty() && running != null) {
            running.stopForeground(STOP_FOREGROUND_REMOVE);
            context.stopService(new Intent(context, BackgroundSyncService.class));
            running = null;
        }
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        try {
            NotificationManager manager = getSystemService(NotificationManager.class);
            if (Build.VERSION.SDK_INT >= 26) {
                manager.createNotificationChannel(new NotificationChannel(CHANNEL,
                    getString(R.string.background_sync_channel), NotificationManager.IMPORTANCE_LOW));
            }
            Intent open = new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
            PendingIntent pending = PendingIntent.getActivity(this, 0, open,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
            Notification.Builder builder = Build.VERSION.SDK_INT >= 26
                ? new Notification.Builder(this, CHANNEL) : new Notification.Builder(this);
            Notification notification = builder.setSmallIcon(R.drawable.ic_background_sync)
                .setContentTitle(getString(R.string.background_sync_title))
                .setContentText(getString(R.string.background_sync_body))
                .setContentIntent(pending).setOngoing(true).setShowWhen(false)
                .setCategory(Notification.CATEGORY_SERVICE).build();
            if (Build.VERSION.SDK_INT >= 34) {
                startForeground(NOTIFICATION, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_REMOTE_MESSAGING);
            } else startForeground(NOTIFICATION, notification);
            // Even a cancelled startForegroundService request must first be
            // promoted. Stopping before promotion can crash the whole app.
            if (owners.isEmpty()) {
                stopForeground(STOP_FOREGROUND_REMOVE);
                stopSelf();
                return START_NOT_STICKY;
            }
            running = this;
            for (BiConsumer<Boolean, String> callback : new ArrayList<>(owners.values())) callback.accept(true, null);
        } catch (RuntimeException error) {
            failOwners(error.toString());
            stopSelf();
        }
        // A killed WebView cannot own a connection; never restart an empty service.
        return START_NOT_STICKY;
    }

    private static void failOwners(String reason) {
        ArrayList<BiConsumer<Boolean, String>> callbacks = new ArrayList<>(owners.values());
        owners.clear();
        for (BiConsumer<Boolean, String> callback : callbacks) callback.accept(false, reason);
    }

    @Override public void onDestroy() {
        if (running == this) {
            running = null;
            failOwners("Background sync service was stopped");
        }
        super.onDestroy();
    }

    @Override public IBinder onBind(Intent intent) { return null; }
}
