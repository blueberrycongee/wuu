package com.blueberrycongee.wuu;

import android.os.Bundle;
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
import static org.junit.Assert.*;

/** Runs against an operator-started real account server and desktop Agent. */
@RunWith(AndroidJUnit4.class)
public class AccountFlowTest {
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
        InstrumentationRegistry.getInstrumentation().sendStringSync(value);
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
            onWebView().withElement(findElement(Locator.CSS_SELECTOR,".account-device button")).perform(webClick());
            waitFor(()->onWebView().withElement(findElement(Locator.CSS_SELECTOR,"textarea")).perform(webClick()));
            fill("textarea","Create android-live-proof.txt containing only ANDROID_VERIFIED. Then reply ANDROID_VERIFIED.");
            onWebView().withElement(findElement(Locator.CSS_SELECTOR,".composer-send-button")).perform(webClick());
            waitFor(()->onWebView().withElement(findElement(Locator.XPATH,"//*[contains(concat(' ', normalize-space(@class), ' '), ' turn-answer-body ') and contains(., 'ANDROID_VERIFIED')]")).check(webMatches(getText(),containsString("ANDROID_VERIFIED"))));
            scenario.moveToState(Lifecycle.State.CREATED); scenario.moveToState(Lifecycle.State.RESUMED);
            waitFor(()->onWebView().withElement(findElement(Locator.XPATH,"//*[contains(concat(' ', normalize-space(@class), ' '), ' turn-answer-body ') and contains(., 'ANDROID_VERIFIED')]")).check(webMatches(getText(),containsString("ANDROID_VERIFIED"))));
            scenario.recreate();
            waitFor(()->onWebView().withElement(findElement(Locator.CSS_SELECTOR,".app-shell")).check(webMatches(getText(),containsString("ANDROID_VERIFIED"))));
        }
    }
}
