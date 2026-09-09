package com.blueberrycongee.wuu;

/** An independently installed receiver exercises the real system URI grant. */
public class ShareReceiverActivity extends android.app.Activity {
    @Override public void onCreate(android.os.Bundle state) {
        super.onCreate(state);
        android.widget.TextView result = new android.widget.TextView(this);
        result.setTextSize(20);
        result.setPadding(24, 80, 24, 24);
        setContentView(result);
        try {
            android.net.Uri uri = getIntent().getParcelableExtra(android.content.Intent.EXTRA_STREAM, android.net.Uri.class);
            if (uri == null) uri = getIntent().getClipData().getItemAt(0).getUri();
            String name;
            try (android.database.Cursor cursor = getContentResolver().query(uri, null, null, null, null)) {
                if (cursor == null || !cursor.moveToFirst()) throw new java.io.IOException("Missing file metadata");
                name = cursor.getString(cursor.getColumnIndexOrThrow(android.provider.OpenableColumns.DISPLAY_NAME));
            }
            try (java.io.InputStream input = getContentResolver().openInputStream(uri)) {
                if (input == null) throw new java.io.IOException("Missing shared content");
                result.setText(name + "\n" + new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8));
            }
        } catch (Exception error) { result.setText("SHARE_FAILED: " + error); }
    }
}
