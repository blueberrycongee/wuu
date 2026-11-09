package com.blueberrycongee.wuu;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;
import com.getcapacitor.*;
import com.getcapacitor.annotation.CapacitorPlugin;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

@CapacitorPlugin(name = "WuuSecureStorage")
public class WuuSecureStoragePlugin extends Plugin {
    private static final String ALIAS = "wuu.credentials.v1";
    private SecretKey key() throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore"); store.load(null);
        if (!store.containsAlias(ALIAS)) {
            KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
            generator.init(new KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build());
            generator.generateKey();
        }
        return (SecretKey) store.getKey(ALIAS, null);
    }
    private SharedPreferences prefs() { return getContext().getSharedPreferences(ALIAS, Context.MODE_PRIVATE); }
    private String name(PluginCall call) throws Exception {
        String value = call.getString("key");
        if (value == null || !value.startsWith("wuu.") || value.length() > 128) throw new Exception("Invalid key");
        return value;
    }
    @PluginMethod public synchronized void get(PluginCall call) {
        try {
            String name = name(call), stored = prefs().getString(name, null);
            if (stored == null) { call.resolve(); return; }
            String[] parts = stored.split(":", 2);
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(Cipher.DECRYPT_MODE, key(), new GCMParameterSpec(128, Base64.decode(parts[0], Base64.NO_WRAP)));
            cipher.updateAAD(name.getBytes(StandardCharsets.UTF_8));
            String value = new String(cipher.doFinal(Base64.decode(parts[1], Base64.NO_WRAP)), StandardCharsets.UTF_8);
            JSObject result = new JSObject(); result.put("value", value); call.resolve(result);
        } catch (Exception e) { call.reject("Credential storage unavailable"); }
    }
    @PluginMethod public synchronized void set(PluginCall call) {
        try {
            String name = name(call), value = call.getString("value");
            if (value == null) throw new Exception("Missing value");
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.ENCRYPT_MODE, key());
            cipher.updateAAD(name.getBytes(StandardCharsets.UTF_8));
            String encrypted = Base64.encodeToString(cipher.getIV(), Base64.NO_WRAP) + ":" + Base64.encodeToString(cipher.doFinal(value.getBytes(StandardCharsets.UTF_8)), Base64.NO_WRAP);
            if (!prefs().edit().putString(name, encrypted).commit()) throw new Exception("Persistence failed");
            call.resolve();
        } catch (Exception e) { call.reject("Could not save credential"); }
    }
    @PluginMethod public synchronized void remove(PluginCall call) {
        try { if (!prefs().edit().remove(name(call)).commit()) throw new Exception("Persistence failed"); call.resolve(); }
        catch (Exception e) { call.reject("Could not remove credential"); }
    }
}
