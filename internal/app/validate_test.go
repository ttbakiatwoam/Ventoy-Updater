package app

import (
	"testing"

	"ventoy-update/internal/providers"
)

func TestValidateReleaseMetadataRequiresChecksum(t *testing.T) {
	err := validateReleaseMetadata(providers.Release{
		Filename:     "ubuntu.iso",
		URL:          "https://example.com/ubuntu.iso",
		ChecksumType: "sha256",
	})
	if err == nil {
		t.Fatal("expected missing checksum error")
	}
}

func TestValidateReleaseMetadataAcceptsISO(t *testing.T) {
	err := validateReleaseMetadata(providers.Release{
		Filename:     "ubuntu.iso",
		URL:          "https://example.com/ubuntu.iso",
		Checksum:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ChecksumType: "sha256",
	})
	if err != nil {
		t.Fatal(err)
	}
}
