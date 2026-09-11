package com.blueberrycongee.wuu;

import android.Manifest;
import android.net.Uri;
import android.webkit.PermissionRequest;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.Test;
import org.junit.runner.RunWith;
import static org.junit.Assert.*;

@RunWith(AndroidJUnit4.class)
public class CameraPermissionTest {
    @Test public void cameraAuthorizationReachesWebView() throws Exception {
        var instrumentation = InstrumentationRegistry.getInstrumentation();
        // A missing manifest declaration makes the OS reject this grant, even
        // though Capacitor already handles the WebView runtime permission request.
        instrumentation.getUiAutomation().grantRuntimePermission(
            instrumentation.getTargetContext().getPackageName(), Manifest.permission.CAMERA);

        CountDownLatch resolved = new CountDownLatch(1);
        AtomicReference<String[]> granted = new AtomicReference<>();
        try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
            scenario.onActivity(app -> {
                var webView = app.getBridge().getWebView();
                webView.getWebChromeClient().onPermissionRequest(new PermissionRequest() {
                    @Override public Uri getOrigin() { return Uri.parse(app.getBridge().getLocalUrl()); }
                    @Override public String[] getResources() { return new String[] { RESOURCE_VIDEO_CAPTURE }; }
                    @Override public void grant(String[] resources) {
                        granted.set(resources);
                        resolved.countDown();
                    }
                    @Override public void deny() { resolved.countDown(); }
                });
            });
            assertTrue("WebView camera authorization did not resolve", resolved.await(10, TimeUnit.SECONDS));
            assertArrayEquals("Authorized camera was denied by the WebView",
                new String[] { PermissionRequest.RESOURCE_VIDEO_CAPTURE }, granted.get());
        }
    }
}
