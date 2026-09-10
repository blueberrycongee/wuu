package com.blueberrycongee.wuu;

import android.graphics.Color;
import android.graphics.drawable.ColorDrawable;
import android.os.Build;
import android.view.View;
import android.view.Window;
import androidx.core.view.WindowCompat;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

@CapacitorPlugin(name = "WuuAppearance")
public class WuuAppearancePlugin extends Plugin {
    @PluginMethod
    public void setSurface(PluginCall call) {
        final int color;
        try {
            color = Color.parseColor(call.getString("color", "#FFFFFF"));
        } catch (IllegalArgumentException error) {
            call.reject("Invalid surface color");
            return;
        }
        final boolean dark = call.getBoolean("dark", false);
        getActivity().runOnUiThread(() -> {
            Window window = getActivity().getWindow();
            window.setBackgroundDrawable(new ColorDrawable(color));
            window.getDecorView().setBackgroundColor(color);
            // Older WebViews reserve native padding around the page. Paint that
            // surface too, while Capacitor keeps ownership of keyboard/insets.
            ((View) getBridge().getWebView().getParent()).setBackgroundColor(color);
            getBridge().getWebView().setBackgroundColor(color);
            var controller = WindowCompat.getInsetsController(window, window.getDecorView());
            controller.setAppearanceLightStatusBars(!dark);
            controller.setAppearanceLightNavigationBars(!dark);
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                window.setStatusBarContrastEnforced(false);
                window.setNavigationBarContrastEnforced(false);
            }
            if (Build.VERSION.SDK_INT < 35) {
                window.setStatusBarColor(color);
                window.setNavigationBarColor(color);
            }
            call.resolve();
        });
    }
}
