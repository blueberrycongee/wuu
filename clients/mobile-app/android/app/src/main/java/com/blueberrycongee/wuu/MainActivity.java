package com.blueberrycongee.wuu;

import com.getcapacitor.BridgeActivity;

public class MainActivity extends BridgeActivity {
    @Override public void onCreate(android.os.Bundle savedInstanceState) {
        registerPlugin(WuuSecureStoragePlugin.class);
        registerPlugin(WuuAppearancePlugin.class);
        super.onCreate(savedInstanceState);
    }
}
