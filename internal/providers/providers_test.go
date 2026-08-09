package providers

import "testing"

func TestParseChecksumManifest(t *testing.T) {
	text := `
abc123 ignored
49e90e694d1b3dedd47f94afbe99dfdd5afb41c8462b638bbd332929769c773a  kali-linux-2026.2-live-amd64.iso
SHA256 (gparted-live-1.8.1-3-amd64.iso) = 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`
	if checksum, ok := findChecksum(text, "kali-linux-2026.2-live-amd64.iso"); !ok || checksum == "" {
		t.Fatalf("expected Kali checksum, got %q %v", checksum, ok)
	}
	if checksum, ok := findChecksum(text, "gparted-live-1.8.1-3-amd64.iso"); !ok || checksum == "" {
		t.Fatalf("expected GParted checksum, got %q %v", checksum, ok)
	}
}

func TestDetectManualProviders(t *testing.T) {
	cases := []string{
		"ubuntu-24.04.2-desktop-amd64.iso",
		"debian-12.9.0-amd64-netinst.iso",
		"kali-linux-2026.2-live-amd64.iso",
		"linuxmint-22.3-cinnamon-64bit.iso",
		"clonezilla-live-3.3.3-15-amd64.iso",
		"gparted-live-1.8.1-3-amd64.iso",
		"tails-amd64-7.10.1.iso",
		"HBCD_PE_x64.iso",
	}
	for _, filename := range cases {
		if _, ok := Detect(filename); !ok {
			t.Fatalf("expected %s to be detected", filename)
		}
	}
}
