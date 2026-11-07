package plugin

import (
	"encoding/json"
	"strings"
	"testing"
)

func normalizeSkinsJSON(t *testing.T, raw string) ([]SkinSpec, error) {
	t.Helper()
	return normalizeSkins(t.TempDir(), json.RawMessage(raw))
}

func TestNormalizeSkinsRejectsInvalidPlacementAndVariantOptions(t *testing.T) {
	skin := func(extra string) string {
		return `[{"id":"demo","name":"Demo",` + extra + `"light":{"background":"#112233","foreground":"#f5f5f5"},"dark":{"background":"#112233","foreground":"#f5f5f5"}}]`
	}
	variant := func(body string) string {
		return `[{"id":"demo","name":"Demo","placement":"canvas","light":{"background":"#112233","foreground":"#f5f5f5",` + body + `},"dark":{"background":"#112233","foreground":"#f5f5f5"}}]`
	}
	frameVariant := func(body string) string {
		return `[{"id":"demo","name":"Demo","light":{"background":"#112233","foreground":"#f5f5f5",` + body + `},"dark":{"background":"#112233","foreground":"#f5f5f5"}}]`
	}
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "placement outside enum", raw: skin(`"placement":"middle",`), wantErr: "placement"},
		{name: "placement non-string", raw: skin(`"placement":1,`), wantErr: "placement"},
		{name: "veil below range", raw: variant(`"veil":-0.1`), wantErr: "veil"},
		{name: "veil above range", raw: variant(`"veil":1.5`), wantErr: "veil"},
		{name: "veil non-number", raw: variant(`"veil":"high"`), wantErr: "veil"},
		{name: "dots missing size", raw: variant(`"dots":{"pitch":4}`), wantErr: "dots"},
		{name: "dots missing pitch", raw: variant(`"dots":{"size":2}`), wantErr: "dots"},
		{name: "dots pitch below range", raw: variant(`"dots":{"pitch":1,"size":1}`), wantErr: "dots"},
		{name: "dots pitch above range", raw: variant(`"dots":{"pitch":33,"size":2}`), wantErr: "dots"},
		{name: "dots size below range", raw: variant(`"dots":{"pitch":4,"size":0.4}`), wantErr: "dots"},
		{name: "dots size above pitch", raw: variant(`"dots":{"pitch":4,"size":4.5}`), wantErr: "dots"},
		{name: "dots unknown field", raw: variant(`"dots":{"pitch":4,"size":2,"angle":45}`), wantErr: "dots"},
		{name: "dots non-object", raw: variant(`"dots":4`), wantErr: "dots"},
		{name: "dots shape outside enum", raw: variant(`"dots":{"pitch":4,"size":2,"shape":"star"}`), wantErr: "shape"},
		{name: "veil without canvas placement", raw: frameVariant(`"veil":0.5`), wantErr: "canvas"},
		{name: "dots without canvas placement", raw: frameVariant(`"dots":{"pitch":4,"size":2}`), wantErr: "canvas"},
		{name: "userImage non-boolean", raw: skin(`"userImage":"yes",`), wantErr: "userImage"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeSkinsJSON(t, test.raw); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("normalizeSkins error = %v, want failure mentioning %q", err, test.wantErr)
			}
		})
	}
}

func TestNormalizeSkinsAcceptsCanvasPlacementAndDefaults(t *testing.T) {
	skins, err := normalizeSkinsJSON(t, `[{
		"id": "deep-field", "name": "Deep field", "placement": "canvas", "userImage": true,
		"light": {"background": "#112233", "foreground": "#f5f5f5", "veil": 0.8, "dots": {"pitch": 4, "size": 2.6, "shape": "square"}},
		"dark": {"background": "#0a1520", "foreground": "#e3f1f6"}
	}]`)
	if err != nil {
		t.Fatalf("normalizeSkins: %v", err)
	}
	if len(skins) != 1 || skins[0].Placement != "canvas" {
		t.Fatalf("placement did not round-trip: %+v", skins)
	}
	if skins[0].Light.Veil == nil || *skins[0].Light.Veil != 0.8 {
		t.Fatalf("veil did not round-trip: %+v", skins[0].Light.Veil)
	}
	if !skins[0].UserImage {
		t.Fatalf("userImage did not round-trip: %+v", skins[0])
	}
	if skins[0].Light.Dots == nil || skins[0].Light.Dots.Pitch != 4 || skins[0].Light.Dots.Size != 2.6 || skins[0].Light.Dots.Shape != "square" {
		t.Fatalf("dots did not round-trip: %+v", skins[0].Light.Dots)
	}
	if skins[0].Dark.Veil != nil || skins[0].Dark.Dots != nil {
		t.Fatalf("omitted variant options must stay omitted: %+v", skins[0].Dark)
	}

	skins, err = normalizeSkinsJSON(t, `[{"id":"plain","name":"Plain","light":{"background":"#112233","foreground":"#f5f5f5"},"dark":{"background":"#112233","foreground":"#f5f5f5"}}]`)
	if err != nil {
		t.Fatalf("normalizeSkins without new fields: %v", err)
	}
	if skins[0].Placement != "" || skins[0].UserImage || skins[0].Light.Veil != nil || skins[0].Light.Dots != nil {
		t.Fatalf("a skin without canvas fields must keep defaults: %+v", skins[0])
	}
}
