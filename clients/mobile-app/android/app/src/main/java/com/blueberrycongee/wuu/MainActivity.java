package com.blueberrycongee.wuu;

import com.getcapacitor.BridgeActivity;

public class MainActivity extends BridgeActivity {
    @Override public void onCreate(android.os.Bundle savedInstanceState) {
        registerPlugin(WuuSecureStoragePlugin.class);
        super.onCreate(savedInstanceState);
    }
}
