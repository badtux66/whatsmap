package live

import "testing"

// TestEncodeQR proves the QR-encoding half of the live pairing flow works
// offline: a real pairing-code string produces a non-trivial square module
// matrix that the console's SVG renderer can draw. (The network handshake that
// supplies the code cannot be exercised without reaching web.whatsapp.com.)
func TestEncodeQR(t *testing.T) {
	// A representative WhatsApp linked-device code payload (comma-separated
	// ref,keys...). The exact contents don't matter; only that it encodes.
	m := encodeQR("2@abcDEF123/456+ghiJKL==,7x8y9z==,foobarbaz==,1")
	if m == nil {
		t.Fatal("encodeQR returned nil for a valid code")
	}
	if len(m) < 21 {
		t.Fatalf("QR matrix too small: %d rows", len(m))
	}
	for i, row := range m {
		if len(row) != len(m) {
			t.Fatalf("row %d not square: %d cols vs %d rows", i, len(row), len(m))
		}
	}
	dark := 0
	for _, row := range m {
		for _, c := range row {
			if c {
				dark++
			}
		}
	}
	if dark == 0 {
		t.Fatal("QR matrix has no dark modules")
	}
}

func TestMaskJIDNil(t *testing.T) {
	if got := maskJID(nil); got != "" {
		t.Errorf("maskJID(nil) = %q, want empty", got)
	}
}
