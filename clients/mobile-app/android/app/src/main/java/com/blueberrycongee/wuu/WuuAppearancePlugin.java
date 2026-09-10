package com.blueberrycongee.wuu;

import android.graphics.Color;
import android.graphics.Canvas;
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
        final int statusColor;
        try {
            color = Color.parseColor(call.getString("color", "#FFFFFF"));
            statusColor = Color.parseColor(call.getString("statusColor", call.getString("color", "#FFFFFF")));
        } catch (IllegalArgumentException error) {
            call.reject("Invalid surface color");
            return;
        }
        final boolean dark = call.getBoolean("dark", false);
        final boolean statusDark = call.getBoolean("statusDark", dark);
        getActivity().runOnUiThread(() -> {
            Window window = getActivity().getWindow();
            window.setBackgroundDrawable(new ColorDrawable(color));
            window.getDecorView().setBackgroundColor(color);
            // Older WebViews reserve native padding around the page. Paint that
            // surface too, while Capacitor keeps ownership of keyboard/insets.
            View parent = (View) getBridge().getWebView().getParent();
            parent.setBackground(new ColorDrawable(color) {
                @Override
                public void draw(Canvas canvas) {
                    super.draw(canvas);
                    // Capacitor reserves the top inset natively on older WebViews.
                    canvas.save();
                    canvas.clipRect(0, 0, parent.getWidth(), parent.getPaddingTop());
                    canvas.drawColor(statusColor);
                    canvas.restore();
                }
            });
            getBridge().getWebView().setBackgroundColor(color);
            var controller = WindowCompat.getInsetsController(window, window.getDecorView());
            controller.setAppearanceLightStatusBars(!statusDark);
            controller.setAppearanceLightNavigationBars(!dark);
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                window.setStatusBarContrastEnforced(false);
                window.setNavigationBarContrastEnforced(false);
            }
            if (Build.VERSION.SDK_INT < 35) {
                window.setStatusBarColor(statusColor);
                window.setNavigationBarColor(color);
            }
            call.resolve();
        });
    }
}
