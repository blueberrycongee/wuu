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
        Bitmap bitmap = InstrumentationRegistry.getInstrumentation().getUiAutomation().takeScreenshot();
        assertNotNull(bitmap);
        try (FileOutputStream output = new FileOutputStream(new File(directory, name + ".png"))) {
            assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output));
        } catch (java.io.IOException error) { throw new AssertionError(error); }
        finally { bitmap.recycle(); }
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
            androidx.test.espresso.Espresso.pressBackUnconditionally();
            waitFor(()->onWebView().withElement(findElement(Locator.CSS_SELECTOR,".account-devices")).check(webMatches(getText(),containsString("在线"))));
            screenshot("system-back-devices");
        }
    }
}
