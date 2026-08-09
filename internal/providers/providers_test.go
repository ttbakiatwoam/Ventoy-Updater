package providers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"testing"
	"time"

	"ventoy-update/internal/config"
)

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

func TestFindChecksumOfType(t *testing.T) {
	text := `
### MD5SUMS:
a4c701431cd41f24b6557622d922918e  gparted-live-1.8.1-3-amd64.iso

### SHA1SUMS:
a75e2a042d4e94f7820aebc758b2e4a6b059f812  gparted-live-1.8.1-3-amd64.iso

### SHA256SUMS:
3f66b2e10b8bb2c573ed6cdd3a9b54fd0a8e7690634ab6b15c3c8f517992d1a1  gparted-live-1.8.1-3-amd64.iso
`
	checksum, ok := findChecksumOfType(text, "gparted-live-1.8.1-3-amd64.iso", "sha256")
	if !ok {
		t.Fatal("expected sha256 checksum")
	}
	if checksum != "3f66b2e10b8bb2c573ed6cdd3a9b54fd0a8e7690634ab6b15c3c8f517992d1a1" {
		t.Fatalf("checksum = %q", checksum)
	}
}

func TestSelectChecksumMatchOfType(t *testing.T) {
	text := `
### MD5SUMS:
5953b6c1b7b9d5948f8cfcf689c6598a  clonezilla-live-3.3.3-15-amd64.iso

### SHA256SUMS:
482518ea32af3b82ed15d09e2e7714806775deb62aeed81491e534f6cc6bbc47  clonezilla-live-3.3.3-15-amd64.iso
`
	pattern := regexp.MustCompile(`^clonezilla-live-([0-9][A-Za-z0-9_.-]+)-amd64\.iso$`)
	filename, version, checksum, ok := selectChecksumMatchOfType(text, pattern, "sha256")
	if !ok {
		t.Fatal("expected clonezilla sha256 match")
	}
	if filename != "clonezilla-live-3.3.3-15-amd64.iso" || version != "3.3.3-15" {
		t.Fatalf("filename/version = %q/%q", filename, version)
	}
	if checksum != "482518ea32af3b82ed15d09e2e7714806775deb62aeed81491e534f6cc6bbc47" {
		t.Fatalf("checksum = %q", checksum)
	}
}

func TestKaliDownloadCandidatesUseOfficialMirrors(t *testing.T) {
	got := kaliDownloadCandidates("2026.2", "kali-linux-2026.2-installer-amd64.iso")
	wantFirst := "https://cdimage.kali.org/kali-2026.2/kali-linux-2026.2-installer-amd64.iso"
	if len(got) == 0 || got[0] != wantFirst {
		t.Fatalf("candidates = %#v", got)
	}
	wantCurrent := "https://cdimage.kali.org/current/kali-linux-2026.2-installer-amd64.iso"
	foundCurrent := false
	for _, candidate := range got {
		if candidate == wantCurrent {
			foundCurrent = true
		}
	}
	if !foundCurrent {
		t.Fatalf("current mirror candidate missing from %#v", got)
	}
}

func TestKaliLiveTorrentOnlyIsSkipped(t *testing.T) {
	client := NewHTTPClient(5*time.Second, 0, nil)
	client.Client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == "https://kali.download/base-images/current/SHA256SUMS" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body: io.NopCloser(bytes.NewReader([]byte(
					"49e90e694d1b3dedd47f94afbe99dfdd5afb41c8462b638bbd332929769c773a  kali-linux-2026.2-live-amd64.iso\n",
				))),
				Request: req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Request:    req,
		}, nil
	})

	_, err := (Kali{}).Latest(context.Background(), client, config.Image{
		ID:       "kali-live",
		Provider: "kali",
		Track:    "current",
		Arch:     "amd64",
		Flavor:   "live",
	})
	if !IsSkipped(err) {
		t.Fatalf("expected skip error, got %v", err)
	}
}

func TestClonezillaStableMirrorChecksum(t *testing.T) {
	text := `482518ea32af3b82ed15d09e2e7714806775deb62aeed81491e534f6cc6bbc47 clonezilla-live-3.3.3-15-amd64.iso
00cee7700433e63017e2ea9eb40519108829710132364a8028a6c039a6046304 clonezilla-live-3.3.3-15-amd64.zip`
	pattern := regexp.MustCompile(`^clonezilla-live-([0-9][A-Za-z0-9_.-]+)-amd64\.iso$`)
	filename, version, checksum, ok := selectChecksumMatchOfType(text, pattern, "sha256")
	if !ok {
		t.Fatal("expected Clonezilla mirror checksum match")
	}
	if filename != "clonezilla-live-3.3.3-15-amd64.iso" || version != "3.3.3-15" {
		t.Fatalf("filename/version = %q/%q", filename, version)
	}
	if checksum != "482518ea32af3b82ed15d09e2e7714806775deb62aeed81491e534f6cc6bbc47" {
		t.Fatalf("checksum = %q", checksum)
	}
}

func TestHirensHTMLToTextExposesSHA256(t *testing.T) {
	html := `<tr><td>Filename</td><td><a href="/files/HBCD_PE_x64.iso">HBCD_PE_x64.iso</a></td></tr>
<tr><td>ISO SHA-256</td><td>8c4c670c9c84d6c4b5a9c32e0aa5a55d8c23de851d259207d54679ea774c2498</td></tr>`
	text := htmlToText(html)
	checksum := firstMatch(text, `(?i)ISO SHA-?256\s*(?:\||:)?\s*([a-f0-9]{64})`, `(?i)SHA-?256[^a-f0-9]+([a-f0-9]{64})`)
	if checksum != "8c4c670c9c84d6c4b5a9c32e0aa5a55d8c23de851d259207d54679ea774c2498" {
		t.Fatalf("checksum = %q", checksum)
	}
}

func TestFindGPartedChecksumInSourceForgeText(t *testing.T) {
	text := `<html><body>
<h3>SHA256SUMS:</h3>
3f66b2e10b8bb2c573ed6cdd3a9b54fd0a8e7690634ab6b15c3c8f517992d1a1 gparted-live-1.8.1-3-amd64.iso
0ff36216b1cbdc33575556eec574f018e034976128ee2229b5fc9c358cbf8cae gparted-live-1.8.1-3-amd64.zip
</body></html>`
	checksum, ok := findGPartedChecksumInSourceForgeText(text, "gparted-live-1.8.1-3-amd64.iso")
	if !ok {
		t.Fatal("expected checksum")
	}
	if checksum != "3f66b2e10b8bb2c573ed6cdd3a9b54fd0a8e7690634ab6b15c3c8f517992d1a1" {
		t.Fatalf("checksum = %q", checksum)
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
		"pop-os_24.04_amd64_nvidia_27.iso",
		"HBCD_PE_x64.iso",
	}
	for _, filename := range cases {
		if _, ok := Detect(filename); !ok {
			t.Fatalf("expected %s to be detected", filename)
		}
	}
}

func TestDetectPopOSNVIDIA(t *testing.T) {
	image, ok := Detect("pop-os_24.04_amd64_nvidia_27.iso")
	if !ok {
		t.Fatal("expected Pop!_OS NVIDIA image to be detected")
	}
	if image.ID != "popos-nvidia" || image.Flavor != "nvidia" {
		t.Fatalf("image = %#v", image)
	}
}
