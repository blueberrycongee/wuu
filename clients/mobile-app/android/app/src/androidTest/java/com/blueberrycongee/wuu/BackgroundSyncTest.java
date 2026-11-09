package com.blueberrycongee.wuu;

import android.app.ActivityManager;
import android.content.Context;
import android.content.Intent;
import androidx.lifecycle.Lifecycle;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.junit.Test;
import org.junit.runner.RunWith;
import static org.junit.Assert.*;

@RunWith(AndroidJUnit4.class)
public class BackgroundSyncTest {
    @Test public void keepsReplacementLeaseAliveAndStopsAfterLastDisconnect() throws Exception {
        Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();
        try (ActivityScenario<MainActivity> activity = ActivityScenario.launch(MainActivity.class)) {
            CountDownLatch ready = new CountDownLatch(1);
            activity.onActivity(app -> BackgroundSyncService.acquire(app, "first", (active, error) -> {
                if (active) ready.countDown();
            }));
            assertTrue("Foreground service did not start", ready.await(10, TimeUnit.SECONDS));
            activity.onActivity(app -> {
                assertTrue(BackgroundSyncService.isRunning());
                BackgroundSyncService.acquire(app, "replacement", (active, error) -> {});
                BackgroundSyncService.release(app, "first");
                assertTrue("Old connection stopped its replacement", BackgroundSyncService.isRunning());
            });
            activity.moveToState(Lifecycle.State.CREATED);
            InstrumentationRegistry.getInstrumentation().runOnMainSync(() -> {
                assertTrue("Backgrounding stopped the service", BackgroundSyncService.isRunning());
                // Notification permission may be denied; promotion must still work.
                assertTrue(context.getSystemService(ActivityManager.class).getRunningServices(100).stream()
                    .anyMatch(service -> service.service.getClassName().equals(BackgroundSyncService.class.getName()) && service.foreground));
                BackgroundSyncService.release(context, "replacement");
                assertFalse(BackgroundSyncService.isRunning());
            });
            activity.moveToState(Lifecycle.State.RESUMED);
        } finally {
            InstrumentationRegistry.getInstrumentation().runOnMainSync(() -> {
                BackgroundSyncService.release(context, "first");
                BackgroundSyncService.release(context, "replacement");
            });
        }
    }

    @Test public void cancelBeforeStartupDoesNotLeaveAnOwner() {
        try (ActivityScenario<MainActivity> activity = ActivityScenario.launch(MainActivity.class)) {
            activity.onActivity(app -> {
                BackgroundSyncService.acquire(app, "cancelled", (active, error) -> {});
                BackgroundSyncService.release(app, "cancelled");
                assertFalse(BackgroundSyncService.isRunning());
            });
            InstrumentationRegistry.getInstrumentation().waitForIdleSync();
            activity.onActivity(app -> assertFalse(BackgroundSyncService.isRunning()));
        }
    }

    @Test public void reportsUnexpectedServiceLossToItsConnection() throws Exception {
        Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();
        try (ActivityScenario<MainActivity> activity = ActivityScenario.launch(MainActivity.class)) {
            CountDownLatch ready = new CountDownLatch(1);
            CountDownLatch stopped = new CountDownLatch(1);
            activity.onActivity(app -> BackgroundSyncService.acquire(app, "interrupted", (active, error) -> {
                if (active) ready.countDown();
                else stopped.countDown();
            }));
            assertTrue(ready.await(10, TimeUnit.SECONDS));
            context.stopService(new Intent(context, BackgroundSyncService.class));
            assertTrue("Service loss was not reported", stopped.await(10, TimeUnit.SECONDS));
            activity.onActivity(app -> assertFalse(BackgroundSyncService.isRunning()));
        } finally {
            InstrumentationRegistry.getInstrumentation().runOnMainSync(() -> BackgroundSyncService.release(context, "interrupted"));
        }
    }
}
