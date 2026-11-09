package com.blueberrycongee.wuu;

import android.graphics.Bitmap;
import android.graphics.Color;
import android.os.SystemClock;
import android.content.Context;
import android.view.inputmethod.InputMethodManager;
import androidx.core.view.ViewCompat;
import androidx.core.view.WindowInsetsCompat;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.espresso.web.model.Atoms;
import androidx.test.espresso.web.webdriver.Locator;
import java.io.File;
import java.io.FileOutputStream;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import static androidx.test.espresso.web.sugar.Web.onWebView;
import static androidx.test.espresso.web.webdriver.DriverAtoms.*;
import static androidx.test.espresso.Espresso.closeSoftKeyboard;
import static org.junit.Assert.*;

@RunWith(AndroidJUnit4.class)
public class LoginSurfaceTest {
    private JSONObject geometry() throws Exception {
        return new JSONObject(onWebView().perform(Atoms.script("function() { var h=document.querySelector('.account-welcome .account-page-header'),a=document.querySelector('.account-brand-art'),i=document.querySelector('input[type=password]'); if(!h||!a||!i) return '{}'; return JSON.stringify({height:h.getBoundingClientRect().height,font:getComputedStyle(h.querySelector('h2')).fontSize,art:a.getBoundingClientRect().width,color:getComputedStyle(h).backgroundColor,viewport:visualViewport.height,fieldHeight:i.getBoundingClientRect().height,fieldTop:i.getBoundingClientRect().top,fieldBottom:i.getBoundingClientRect().bottom}); }", Atoms.castOrDie(String.class))).get());
    }
    private void awaitReady() throws Exception {
        long deadline = SystemClock.uptimeMillis() + 20000;
        while (SystemClock.uptimeMillis() < deadline) {
            if (geometry().has("height")) return;
            SystemClock.sleep(100);
        }
        fail("Login screen did not load");
    }
    private Bitmap capture(String name) throws Exception {
        InstrumentationRegistry.getInstrumentation().waitForIdleSync();
        Bitmap image = InstrumentationRegistry.getInstrumentation().getUiAutomation().takeScreenshot();
        String output = InstrumentationRegistry.getArguments().getString("additionalTestOutputDir");
        File dir = output != null ? new File(output) : InstrumentationRegistry.getInstrumentation().getTargetContext().getExternalFilesDir("validation");
        assertTrue(dir.isDirectory() || dir.mkdirs());
        try (FileOutputStream stream = new FileOutputStream(new File(dir, name + ".png"))) { image.compress(Bitmap.CompressFormat.PNG,100,stream); }
        return image;
    }
    @Test public void statusBackgroundAndLoginGeometrySurviveKeyboard() throws Exception {
        try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
            awaitReady();
            closeSoftKeyboard();
            JSONObject before = geometry();
            int[] top = {0};
            scenario.onActivity(app -> top[0] = ViewCompat.getRootWindowInsets(app.getWindow().getDecorView()).getInsets(WindowInsetsCompat.Type.statusBars()).top);
            assertTrue("Missing native status bar", top[0] > 0);
            Bitmap image = capture("login-before-keyboard");
            int pixel = image.getPixel(image.getWidth()/3, top[0]/2);
            image.recycle();
            assertTrue("Status bar is still light: " + Integer.toHexString(pixel), Color.red(pixel) < 100 && Color.green(pixel) < 100 && Color.blue(pixel) < 100);
            onWebView().withElement(findElement(Locator.CSS_SELECTOR,"input[type=password]")).perform(webClick());
            scenario.onActivity(app -> ((InputMethodManager)app.getSystemService(Context.INPUT_METHOD_SERVICE)).showSoftInput(app.getBridge().getWebView(), InputMethodManager.SHOW_IMPLICIT));
            long deadline = SystemClock.uptimeMillis() + 10000;
            boolean[] keyboard = {false};
            do {
                scenario.onActivity(app -> keyboard[0] = ViewCompat.getRootWindowInsets(app.getWindow().getDecorView()).isVisible(WindowInsetsCompat.Type.ime()));
                if (!keyboard[0]) SystemClock.sleep(100);
            } while (!keyboard[0] && SystemClock.uptimeMillis() < deadline);
            assertTrue("Native keyboard did not open", keyboard[0]);
            JSONObject after = geometry();
            deadline = SystemClock.uptimeMillis() + 5000;
            while ((after.getDouble("fieldBottom") > after.getDouble("viewport") || after.getDouble("fieldTop") < 0) && SystemClock.uptimeMillis() < deadline) {
                SystemClock.sleep(100);
                after = geometry();
            }
            assertEquals("Keyboard compressed the input", before.getDouble("fieldHeight"), after.getDouble("fieldHeight"), 1);
            assertTrue("Input is covered by the keyboard", after.getDouble("fieldBottom") <= after.getDouble("viewport"));
            assertTrue("Input scrolled above the screen", after.getDouble("fieldTop") >= 0);
            scenario.onActivity(app -> {
                assertFalse(app.getBridge().getWebView().isVerticalScrollBarEnabled());
                assertFalse(app.getBridge().getWebView().isHorizontalScrollBarEnabled());
            });
            capture("login-with-keyboard").recycle();
            assertEquals("Keyboard compressed the hero", before.getDouble("height"), after.getDouble("height"), 1);
            assertEquals("Keyboard changed the title size", before.getString("font"), after.getString("font"));
            assertEquals("Keyboard changed the artwork size", before.getDouble("art"), after.getDouble("art"), 1);
            closeSoftKeyboard();
        }
    }
}
