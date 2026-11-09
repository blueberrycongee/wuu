package com.blueberrycongee.wuu;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import java.util.HashSet;
import java.util.Set;

@CapacitorPlugin(name = "WuuBackgroundSync")
public class WuuBackgroundSyncPlugin extends Plugin {
    private final Set<String> leases = new HashSet<>();

    @PluginMethod public void start(PluginCall call) {
        String id = call.getString("id");
        if (id == null || id.isEmpty() || id.length() > 128) {
            call.reject("Invalid background sync lease");
            return;
        }
        getActivity().runOnUiThread(() -> {
            if (!BackgroundSyncService.isRunning() && !getBridge().getApp().isActive()) {
                call.reject("Open Wuu to start background sync");
                return;
            }
            leases.add(id);
            boolean[] settled = {false};
            BackgroundSyncService.acquire(getContext(), id, (active, error) -> {
                if (!active) leases.remove(id);
                JSObject result = new JSObject().put("id", id).put("active", active);
                if (error != null) result.put("error", error);
                if (!settled[0]) {
                    settled[0] = true;
                    if (active) call.resolve(result);
                    else call.reject(error);
                } else notifyListeners("stateChange", result);
            });
        });
    }

    @PluginMethod public void stop(PluginCall call) {
        String id = call.getString("id");
        getActivity().runOnUiThread(() -> {
            if (leases.remove(id)) BackgroundSyncService.release(getContext(), id);
            call.resolve();
        });
    }

    @Override protected void handleOnDestroy() {
        getActivity().runOnUiThread(() -> {
            for (String id : new HashSet<>(leases)) BackgroundSyncService.release(getContext(), id);
            leases.clear();
        });
    }
}
