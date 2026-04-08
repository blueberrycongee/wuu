package tui

import "testing"

func TestDetectImageMediaType(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "png",
			data: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00},
			want: "image/png",
		},
		{
			name: "jpeg",
			data: []byte{0xFF, 0xD8, 0xFF, 0x00},
			want: "image/jpeg",
		},
		{
			name: "gif",
			data: []byte("GIF89a...."),
			want: "image/gif",
		},
		{
			name: "webp",
			data: []byte("RIFFxxxxWEBP"),
			want: "image/webp",
		},
		{
			name: "bmp",
			data: []byte("BMxxxx"),
			want: "image/bmp",
		},
		{
			name: "unknown",
			data: []byte("not-an-image"),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectImageMediaType(tt.data)
			if got != tt.want {
				t.Fatalf("detectImageMediaType() = %q, want %q", got, tt.want)
			}
		})
	}
}
