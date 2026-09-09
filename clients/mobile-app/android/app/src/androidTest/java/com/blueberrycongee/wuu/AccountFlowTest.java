package com.blueberrycongee.wuu;

import android.os.Bundle;
import android.graphics.Bitmap;
import android.content.pm.ActivityInfo;
import java.io.File;
import java.io.FileOutputStream;
import static androidx.test.espresso.Espresso.closeSoftKeyboard;
import android.os.SystemClock;
import androidx.lifecycle.Lifecycle;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.espresso.web.webdriver.Locator;
import org.junit.Test;
import org.junit.runner.RunWith;
import static androidx.test.espresso.web.sugar.Web.onWebView;
import static androidx.test.espresso.web.webdriver.DriverAtoms.*;
import static androidx.test.espresso.web.assertion.WebViewAssertions.webMatches;
import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.equalTo;
import static org.junit.Assert.*;

/** Runs against an operator-started real account server and desktop Agent. */
@RunWith(AndroidJUnit4.class)
public class AccountFlowTest {
    private void screenshot(String name) {
        try { InstrumentationRegistry.getInstrumentation().getUiAutomation().waitForIdle(500, 5000); }
        catch (java.util.concurrent.TimeoutException error) { throw new AssertionError("UI did not settle before screenshot", error); }
        File directory = InstrumentationRegistry.getInstrumentation().getTargetContext().getExternalFilesDir("validation");
        assertNotNull(directory);
        assertTrue(directory.isDirectory() || directory.mkdirs());
        String geometry = onWebView().perform(androidx.test.espresso.web.model.Atoms.script(
            "function() { var input = document.querySelector('textarea'), send = document.querySelector('.composer-send-button'); return JSON.stringify({width:innerWidth,height:innerHeight,visualHeight:visualViewport.height,scale:visualViewport.scale,input:input && input.getBoundingClientRect().toJSON(),send:send && send.getBoundingClientRect().toJSON(),rootHeight:document.documentElement.style.getPropertyValue('--web-viewport-height')}); }",
            androidx.test.espresso.web.model.Atoms.castOrDie(String.class))).get();
        try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".json"))) {
            output.write(geometry.getBytes(java.nio.charset.StandardCharsets.UTF_8));
        } catch (java.io.IOException error) { throw new AssertionError(error); }
        nativeScreenshot(name);
    }
    private void nativeScreenshot(String name) {
        File directory = InstrumentationRegistry.getInstrumentation().getTargetContext().getExternalFilesDir("validation");
        assertNotNull(directory);
        assertTrue(directory.isDirectory() || directory.mkdirs());
        Bitmap bitmap = InstrumentationRegistry.getInstrumentation().getUiAutomation().takeScreenshot();
        assertNotNull(bitmap);
        try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".png"))) {
            assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output));
        } catch (java.io.IOException error) { throw new AssertionError(error); }
        finally { bitmap.recycle(); }
    }
    private android.view.accessibility.AccessibilityNodeInfo nativeText(String text) {
        android.view.accessibility.AccessibilityNodeInfo root = InstrumentationRegistry.getInstrumentation().getUiAutomation().getRootInActiveWindow();
        assertNotNull(root);
        java.util.List<android.view.accessibility.AccessibilityNodeInfo> nodes = root.findAccessibilityNodeInfosByText(text);
        assertFalse("Missing native text: " + text, nodes.isEmpty());
        for (android.view.accessibility.AccessibilityNodeInfo node : nodes) if (text.contentEquals(node.getText() == null ? "" : node.getText())) return node;
        return nodes.get(0);
    }
    private void nativeClick(String text) {
        android.view.accessibility.AccessibilityNodeInfo node = nativeText(text);
        android.view.accessibility.AccessibilityNodeInfo clickable = node;
        while (clickable != null && !clickable.isClickable()) clickable = clickable.getParent();
        if (clickable != null && clickable.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_CLICK)) return;
        android.graphics.Rect bounds = new android.graphics.Rect();
        node.getBoundsInScreen(bounds);

        if (bounds.isEmpty()) {
            node.performAction(android.view.accessibility.AccessibilityNodeInfo.AccessibilityAction.ACTION_SHOW_ON_SCREEN.getId());
            while (node != null && !node.isScrollable()) node = node.getParent();
            if (node != null) node.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_SCROLL_FORWARD);
            throw new AssertionError("Scrolling native target into view: " + text);
        }
        long time = SystemClock.uptimeMillis();
        android.app.UiAutomation automation = InstrumentationRegistry.getInstrumentation().getUiAutomation();
        for (int action : new int[] { android.view.MotionEvent.ACTION_DOWN, android.view.MotionEvent.ACTION_UP }) {
            android.view.MotionEvent event = android.view.MotionEvent.obtain(time, SystemClock.uptimeMillis(), action, bounds.centerX(), bounds.centerY(), 0);
            event.setSource(android.view.InputDevice.SOURCE_TOUCHSCREEN);
            assertTrue(automation.injectInputEvent(event, true));
            event.recycle();
        }
    }
    private void touchWebElement(ActivityScenario<MainActivity> scenario, Locator locator, String selector) {
        String position = onWebView().withElement(findElement(locator, selector)).perform(androidx.test.espresso.web.model.Atoms.script(
            "function(el) { var r=el.getBoundingClientRect(); return JSON.stringify([r.x+r.width/2,r.y+r.height/2]); }",
            androidx.test.espresso.web.model.Atoms.castOrDie(String.class))).get();
        final float[] point = new float[2];
        scenario.onActivity(activity -> {
            android.webkit.WebView web = activity.getBridge().getWebView();
            int[] origin = new int[2]; web.getLocationOnScreen(origin);
            try {
                org.json.JSONArray xy = new org.json.JSONArray(position);
                point[0] = origin[0] + (float) xy.getDouble(0) * web.getScale();
                point[1] = origin[1] + (float) xy.getDouble(1) * web.getScale();
            } catch (org.json.JSONException error) { throw new AssertionError(error); }
        });
        long time = SystemClock.uptimeMillis();
        for (int action : new int[] { android.view.MotionEvent.ACTION_DOWN, android.view.MotionEvent.ACTION_UP }) {
            android.view.MotionEvent event = android.view.MotionEvent.obtain(time, SystemClock.uptimeMillis(), action, point[0], point[1], 0);
            event.setSource(android.view.InputDevice.SOURCE_TOUCHSCREEN);
            assertTrue(InstrumentationRegistry.getInstrumentation().getUiAutomation().injectInputEvent(event, true));
            event.recycle();
        }
    }
    private void phase(String value) {
        Bundle status = new Bundle(); status.putString("wuu_phase", value);
        InstrumentationRegistry.getInstrumentation().sendStatus(2, status);
    }
    private void waitFor(Runnable assertion) {
        long deadline = SystemClock.elapsedRealtime() + 120000;
        Throwable last = null;
        while (SystemClock.elapsedRealtime() < deadline) {
            try { assertion.run(); return; } catch (Throwable e) { last = e; }
            SystemClock.sleep(250);
        }
        throw new AssertionError("Remote UI did not reach the expected state", last);
    }
    private void fill(String selector, String value) {
        onWebView().withElement(findElement(Locator.CSS_SELECTOR,selector)).perform(webClick());
        // Let the controlled web input commit each native key before the next.
        // A bulk synthetic key burst can outrun the WebView/React input cycle.
        for (int index = 0; index < value.length(); index++) {
            InstrumentationRegistry.getInstrumentation().sendStringSync(value.substring(index, index + 1));
            String prefix = value.substring(0, index + 1);
            waitFor(() -> onWebView().withElement(findElement(Locator.CSS_SELECTOR, selector)).check(webMatches(androidx.test.espresso.web.model.Atoms.script("function(el) { return el.value; }", androidx.test.espresso.web.model.Atoms.castOrDie(String.class)), equalTo(prefix))));
        }
        closeSoftKeyboard();
    }
    @Test public void loginRunAgentAndRestore() {
        Bundle args = InstrumentationRegistry.getArguments();
        String server=args.getString("server"), username=args.getString("username"), password=args.getString("password");
        assertNotNull("Pass a local server instrumentation argument",server);
        assertNotNull(username);assertNotNull(password);
        try (ActivityScenario<MainActivity> scenario=ActivityScenario.launch(MainActivity.class)) {
            waitFor(()->onWebView().withElement(findElement(Locator.CSS_SELECTOR,".account-panel h2")).check(webMatches(getText(),containsString("Wuu"))));
            fill("input[type=url]",server); fill("input[autocomplete=username]",username);fill("input[type=password]",password);
            onWebView().withElement(findElement(Locator.CSS_SELECTOR,"button[type=submit]")).perform(webClick());
            waitFor(()->onWebView().withElement(findElement(Locator.CSS_SELECTOR,".account-devices")).check(webMatches(getText(),containsString("在线"))));
            String computer = args.getString("computer");
            if (computer == null) {
                onWebView().withElement(findElement(Locator.CSS_SELECTOR,".account-device button")).perform(webClick());
            } else {
                assertFalse("Computer test name must not contain a single quote", computer.contains("'"));
                onWebView().withElement(findElement(Locator.XPATH,"//div[contains(@class,'account-device')][div/strong[text()='" + computer + "']]/button[1]")).perform(webClick());
            }
            waitFor(()->onWebView().withElement(findElement(Locator.CSS_SELECTOR,"textarea")).perform(webClick()));
            fill("textarea","Create android-live-proof.txt containing only ANDROID_VERIFIED. Then reply ANDROID_VERIFIED.");
            onWebView().withElement(findElement(Locator.CSS_SELECTOR,".composer-send-button")).perform(webClick());
            waitFor(()->onWebView().withElement(findElement(Locator.XPATH,"//*[contains(concat(' ', normalize-space(@class), ' '), ' turn-answer-body ') and contains(., 'ANDROID_VERIFIED')]")).check(webMatches(getText(),containsString("ANDROID_VERIFIED"))));
            screenshot("agent-result-portrait");
            scenario.onActivity(activity -> activity.setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE));
            waitFor(() -> assertTrue(InstrumentationRegistry.getInstrumentation().getTargetContext().getResources().getConfiguration().orientation == android.content.res.Configuration.ORIENTATION_LANDSCAPE));
            waitFor(()->onWebView().withElement(findElement(Locator.CSS_SELECTOR,".app-shell")).check(webMatches(getText(),containsString("ANDROID_VERIFIED"))));
            closeSoftKeyboard();
            screenshot("agent-result-landscape");
            onWebView().withElement(findElement(Locator.CSS_SELECTOR,"textarea")).perform(webClick());
            scenario.onActivity(activity -> {
                android.view.View focus = activity.getCurrentFocus();
                assertNotNull(focus);
                ((android.view.inputmethod.InputMethodManager) activity.getSystemService(android.content.Context.INPUT_METHOD_SERVICE)).showSoftInput(focus, android.view.inputmethod.InputMethodManager.SHOW_IMPLICIT);
            });
            waitFor(() -> scenario.onActivity(activity -> assertTrue(activity.getWindow().getDecorView().getRootWindowInsets().isVisible(android.view.WindowInsets.Type.ime()))));
            waitFor(() -> onWebView().check(webMatches(androidx.test.espresso.web.model.Atoms.script(
                "function() { var input = document.querySelector('textarea').getBoundingClientRect(), send = document.querySelector('.composer-send-button').getBoundingClientRect(); return input.top >= 0 && input.bottom <= visualViewport.height && send.top >= 0 && send.bottom <= visualViewport.height; }",
                androidx.test.espresso.web.model.Atoms.castOrDie(Boolean.class)), equalTo(true))));
            screenshot("landscape-keyboard");
            closeSoftKeyboard();
            scenario.onActivity(activity -> activity.setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_PORTRAIT));
            scenario.moveToState(Lifecycle.State.CREATED); scenario.moveToState(Lifecycle.State.RESUMED);
            waitFor(()->onWebView().withElement(findElement(Locator.XPATH,"//*[contains(concat(' ', normalize-space(@class), ' '), ' turn-answer-body ') and contains(., 'ANDROID_VERIFIED')]")).check(webMatches(getText(),containsString("ANDROID_VERIFIED"))));
            scenario.recreate();
            waitFor(()->onWebView().withElement(findElement(Locator.CSS_SELECTOR,".app-shell")).check(webMatches(getText(),containsString("ANDROID_VERIFIED"))));
            screenshot("restored-session");
            onWebView().withElement(findElement(Locator.CSS_SELECTOR, ".compact-conversation-actions [aria-haspopup=menu]")).perform(webClick());
            onWebView().withElement(findElement(Locator.XPATH, "//*[@role='menuitem' and contains(., 'Open right sidebar')]")).perform(webClick());
            waitFor(() -> onWebView().withElement(findElement(Locator.XPATH, "//button[contains(., 'Files')]")).perform(webClick()));
            waitFor(() -> onWebView().check(webMatches(androidx.test.espresso.web.model.Atoms.script(
                "function() { function find(root) { var row = root.querySelector('[role=treeitem][data-item-path=\"android-live-proof.txt\"]'); if (row) { row.click(); return true; } for (var el of root.querySelectorAll('*')) if (el.shadowRoot && find(el.shadowRoot)) return true; return false; } return find(document); }",
                androidx.test.espresso.web.model.Atoms.castOrDie(Boolean.class)), equalTo(true))));
            waitFor(() -> onWebView().withElement(findElement(Locator.CSS_SELECTOR, ".workspace-file-export-actions button")).perform(webClick()));
            waitFor(() -> nativeClick("Wuu Share Validation"));
            waitFor(() -> {
                android.view.accessibility.AccessibilityNodeInfo received = nativeText("ANDROID_VERIFIED");
                assertEquals("com.blueberrycongee.wuu.test", String.valueOf(received.getPackageName()));
                assertEquals("android-live-proof.txt\nANDROID_VERIFIED", received.getText().toString().trim());
            });
            try { InstrumentationRegistry.getInstrumentation().getUiAutomation().waitForIdle(500, 5000); }
            catch (java.util.concurrent.TimeoutException error) { throw new AssertionError(error); }
            android.graphics.Bitmap shared = InstrumentationRegistry.getInstrumentation().getUiAutomation().takeScreenshot();
            try (FileOutputStream output = new FileOutputStream(new File(InstrumentationRegistry.getInstrumentation().getTargetContext().getExternalFilesDir("validation"), "system-share-received.png"))) {
                assertTrue(shared.compress(Bitmap.CompressFormat.PNG, 100, output));
            } catch (java.io.IOException error) { throw new AssertionError(error); }
            finally { shared.recycle(); }
            assertTrue(InstrumentationRegistry.getInstrumentation().getUiAutomation().performGlobalAction(android.accessibilityservice.AccessibilityService.GLOBAL_ACTION_BACK));
            waitFor(() -> onWebView().withElement(findElement(Locator.CSS_SELECTOR, ".workspace-file-export-actions button")).check(webMatches(getText(), containsString("android-live-proof.txt"))));
            // Native Back closes the tool panel and returns to the conversation.
            androidx.test.espresso.Espresso.pressBackUnconditionally();
            String imageName = args.getString("image");
            if (imageName != null) {
                onWebView().withElement(findElement(Locator.CSS_SELECTOR, ".composer-plus-button")).perform(webClick());
                touchWebElement(scenario, Locator.XPATH, "//button[contains(., 'Choose files')]");
                nativeScreenshot("system-picker-open");
                waitFor(() -> nativeClick(imageName));
                waitFor(() -> onWebView().check(webMatches(androidx.test.espresso.web.model.Atoms.script(
                    "function() { var image = document.querySelector('.composer-image-attachment img'); return !!image && image.complete && image.naturalWidth > 0; }",
                    androidx.test.espresso.web.model.Atoms.castOrDie(Boolean.class)), equalTo(true))));
                screenshot("system-image-imported");
                onWebView().withElement(findElement(Locator.CSS_SELECTOR, ".composer-attachment-remove")).perform(webClick());
            }
            if ("true".equals(args.getString("outage"))) {
                fill("textarea", "Preserve this unsent draft across computer loss");
                phase("ready-for-host-stop");
                waitFor(() -> onWebView().withElement(findElement(Locator.CSS_SELECTOR, ".web-connection-status")).check(webMatches(androidx.test.espresso.web.model.Atoms.script(
                    "function(el) { return el.getBoundingClientRect().height > 0; }", androidx.test.espresso.web.model.Atoms.castOrDie(Boolean.class)), equalTo(true))));
                screenshot("computer-disconnected");
                phase("ready-for-host-start");
                waitFor(() -> onWebView().check(webMatches(androidx.test.espresso.web.model.Atoms.script(
                    "function() { return !document.querySelector('.web-connection-status'); }",
                    androidx.test.espresso.web.model.Atoms.castOrDie(Boolean.class)), equalTo(true))));
                onWebView().withElement(findElement(Locator.CSS_SELECTOR, "textarea")).check(webMatches(androidx.test.espresso.web.model.Atoms.script(
                    "function(el) { return el.value; }", androidx.test.espresso.web.model.Atoms.castOrDie(String.class)), equalTo("Preserve this unsent draft across computer loss")));
                screenshot("computer-reconnected");
                phase("host-restored");
            }
            androidx.test.espresso.Espresso.pressBackUnconditionally();
            waitFor(()->onWebView().withElement(findElement(Locator.CSS_SELECTOR,".account-devices")).check(webMatches(getText(),containsString("在线"))));
            screenshot("system-back-devices");
        }
    }
}
