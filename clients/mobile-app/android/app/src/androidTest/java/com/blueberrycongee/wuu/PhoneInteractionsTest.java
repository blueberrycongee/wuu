package com.blueberrycongee.wuu;

import android.os.SystemClock;
import android.view.inputmethod.InputMethodManager;
import androidx.core.view.ViewCompat;
import androidx.core.view.WindowInsetsCompat;
import androidx.test.core.app.ActivityScenario;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.espresso.web.model.Atoms;
import androidx.test.espresso.web.webdriver.Locator;
import org.json.JSONObject;
import org.junit.Test;
import static org.junit.Assert.*;
import static androidx.test.espresso.web.sugar.Web.onWebView;
import static androidx.test.espresso.web.webdriver.DriverAtoms.*;

/** Production React components served by the phone fixture, with a real Android IME. */
public class PhoneInteractionsTest {
    private String script(String body) {
        return onWebView().perform(Atoms.script("function() { " + body + " }", Atoms.castOrDie(String.class))).get();
    }

    private void awaitScript(String condition) {
        long deadline = SystemClock.uptimeMillis() + 10000;
        while (SystemClock.uptimeMillis() < deadline) {
            if ("yes".equals(script("return (" + condition + ") ? 'yes' : 'no';"))) return;
            SystemClock.sleep(50);
        }
        fail("Timed out: " + condition);
    }

    private void keyboard(ActivityScenario<MainActivity> scenario, boolean visible) {
        long deadline = SystemClock.uptimeMillis() + 10000;
        boolean[] actual = { !visible };
        do {
            scenario.onActivity(app -> actual[0] = ViewCompat.getRootWindowInsets(app.getWindow().getDecorView()).isVisible(WindowInsetsCompat.Type.ime()));
            if (actual[0] == visible) return;
            SystemClock.sleep(50);
        } while (SystemClock.uptimeMillis() < deadline);
        assertEquals("Unexpected keyboard visibility", visible, actual[0]);
    }

    private void openKeyboard(ActivityScenario<MainActivity> scenario, String selector) {
        onWebView().withElement(findElement(Locator.CSS_SELECTOR, selector)).perform(webClick());
        scenario.onActivity(app -> ((InputMethodManager) app.getSystemService(android.content.Context.INPUT_METHOD_SERVICE))
            .showSoftInput(app.getBridge().getWebView(), InputMethodManager.SHOW_IMPLICIT));
        keyboard(scenario, true);
    }

    @Test public void pickerDismissesImeAndLoginDoesNotCompress() throws Exception {
        String url = InstrumentationRegistry.getArguments().getString("fixtureUrl");
        assertNotNull("Pass the phone-interactions.html fixture URL", url);
        try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
            scenario.onActivity(app -> app.getBridge().getWebView().loadUrl(url));
            onWebView().forceJavascriptEnabled();
            awaitScript("document.querySelector('textarea')");
            openKeyboard(scenario, "textarea");
            onWebView().withElement(findElement(Locator.CSS_SELECTOR, ".codex-runtime-trigger")).perform(webClick());
            keyboard(scenario, false);
            awaitScript("document.querySelector('dialog[data-ready=true]')");
            assertEquals("yes", script("return document.querySelector('dialog').matches(':modal') ? 'yes' : 'no';"));
            assertEquals("Unsent phone draft", script("return document.querySelector('textarea').value;"));
            onWebView().withElement(findElement(Locator.CSS_SELECTOR, ".runtime-panel-model")).perform(webClick());
            openKeyboard(scenario, "input[type=search]");
            onWebView().withElement(findElement(Locator.CSS_SELECTOR, ".composer-mobile-sheet-panel > header button")).perform(webClick());
            awaitScript("!document.querySelector('dialog')");
            keyboard(scenario, false);
            assertEquals("no", script("return document.activeElement === document.querySelector('textarea') ? 'yes' : 'no';"));

            scenario.onActivity(app -> app.getBridge().getWebView().loadUrl(url + "?login"));
            awaitScript("document.querySelector('input[type=password]')");
            double before = new JSONObject(script("return JSON.stringify({height:document.querySelector('.account-page-header').getBoundingClientRect().height});")).getDouble("height");
            // Switching field types exercises IME replacement; OEM secure keyboards
            // still require a device check because the emulator has no such IME.
            for (String selector : new String[] {"input[autocomplete=username]", "input[type=password]", "input[autocomplete=username]", "input[type=password]"}) {
                openKeyboard(scenario, selector);
                awaitScript("(() => { const r=document.querySelector('" + selector + "').getBoundingClientRect(); return r.top>=visualViewport.offsetTop && r.bottom<=visualViewport.offsetTop+visualViewport.height; })()");
                assertEquals("Outer document moved during keyboard switch", "yes", script("return Math.abs(document.querySelector('#root').getBoundingClientRect().top) < 1 && window.scrollY === 0 ? 'yes' : 'no';"));
            }
            double after = new JSONObject(script("return JSON.stringify({height:document.querySelector('.account-page-header').getBoundingClientRect().height});")).getDouble("height");
            assertEquals("Login artwork was compressed by the IME", before, after, 1);
            scenario.onActivity(app -> ((InputMethodManager) app.getSystemService(android.content.Context.INPUT_METHOD_SERVICE))
                .hideSoftInputFromWindow(app.getBridge().getWebView().getWindowToken(), 0));
            keyboard(scenario, false);
            awaitScript("Math.abs(document.querySelector('#root').getBoundingClientRect().top) < 1 && visualViewport.offsetTop === 0");
        }
    }
}
