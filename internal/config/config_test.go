package config

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseYAMLSubset(t *testing.T) {
	input := `
version: 1
images:
  - id: ubuntu-desktop
    name: "Ubuntu Desktop"
    provider: ubuntu
    track: "24.04"
    arch: amd64
    flavor: desktop
    filename: ubuntu-24.04.2-desktop-amd64.iso
manual:
  - filename: Win11_24H2_English_x64.iso
`
	manifest, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(manifest.Images))
	}
	if manifest.Images[0].Track != "24.04" {
		t.Fatalf("track = %q", manifest.Images[0].Track)
	}
	if len(manifest.Manual) != 1 {
		t.Fatalf("expected 1 manual image, got %d", len(manifest.Manual))
	}
}

func TestWriteRoundTrip(t *testing.T) {
	manifest := Manifest{
		Version: 1,
		Images: []Image{{
			ID:       "hirens",
			Name:     "Hiren's BootCD PE",
			Provider: "hirens",
			Track:    "stable",
			Arch:     "amd64",
			Flavor:   "pe",
			Filename: "HBCD_PE_x64.iso",
		}},
		Manual: []ManualImage{{Filename: "WinPE_Win11.iso"}},
	}
	var out bytes.Buffer
	if err := Write(&out, manifest); err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Images[0].Name != "Hiren's BootCD PE" {
		t.Fatalf("name = %q", parsed.Images[0].Name)
	}
}
