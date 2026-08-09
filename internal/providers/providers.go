package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ventoy-update/internal/config"
)

const manifestLimit = 16 << 20

type Release struct {
	ImageID      string
	Provider     string
	Name         string
	Version      string
	Filename     string
	URL          string
	Checksum     string
	ChecksumType string
	Size         int64
}

type Provider interface {
	ID() string
	Name() string
	Detect(filename string) (config.Image, bool)
	Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error)
}

func Registry() map[string]Provider {
	providers := []Provider{
		Ubuntu{},
		Debian{},
		Kali{},
		LinuxMint{},
		Clonezilla{},
		GParted{},
		Tails{},
		PopOS{},
		Hirens{},
	}
	registry := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		registry[provider.ID()] = provider
	}
	return registry
}

func Ordered() []Provider {
	registry := Registry()
	keys := make([]string, 0, len(registry))
	for key := range registry {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Provider, 0, len(keys))
	for _, key := range keys {
		out = append(out, registry[key])
	}
	return out
}

func Detect(filename string) (config.Image, bool) {
	for _, provider := range Ordered() {
		image, ok := provider.Detect(filename)
		if ok {
			return image, true
		}
	}
	return config.Image{}, false
}

type checksumEntry struct {
	Filename     string
	Checksum     string
	ChecksumType string
}

func parseChecksumManifest(text string) []checksumEntry {
	var entries []checksumEntry
	currentType := ""
	reSection := regexp.MustCompile(`(?i)^#+\s*(MD5|SHA1|SHA256|SHA512)SUMS?:`)
	reBSD := regexp.MustCompile(`(?i)^(SHA(?:1|256|512)|MD5)\s*\(([^)]+)\)\s*=\s*([a-f0-9]+)$`)
	reLabeled := regexp.MustCompile(`(?i)^([a-f0-9]{32,128})\s+[* ]?(.+)$`)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			if match := reSection.FindStringSubmatch(line); match != nil {
				currentType = normalizeChecksumType(match[1])
			}
			continue
		}
		if match := reBSD.FindStringSubmatch(line); match != nil {
			entries = append(entries, checksumEntry{
				Filename:     cleanManifestFilename(match[2]),
				Checksum:     strings.ToLower(match[3]),
				ChecksumType: normalizeChecksumType(match[1]),
			})
			continue
		}
		if match := reLabeled.FindStringSubmatch(line); match != nil {
			checksum := strings.ToLower(match[1])
			entries = append(entries, checksumEntry{
				Filename:     cleanManifestFilename(match[2]),
				Checksum:     checksum,
				ChecksumType: defaultString(currentType, inferChecksumType(checksum)),
			})
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && looksLikeHash(fields[0]) {
			checksum := strings.ToLower(fields[0])
			entries = append(entries, checksumEntry{
				Filename:     cleanManifestFilename(fields[len(fields)-1]),
				Checksum:     checksum,
				ChecksumType: defaultString(currentType, inferChecksumType(checksum)),
			})
		}
	}
	return entries
}

func findChecksum(text, filename string) (string, bool) {
	for _, entry := range parseChecksumManifest(text) {
		if entry.Filename == filename || path.Base(entry.Filename) == filename {
			return entry.Checksum, true
		}
	}
	return "", false
}

func findChecksumOfType(text, filename, checksumType string) (string, bool) {
	checksumType = normalizeChecksumType(checksumType)
	for _, entry := range parseChecksumManifest(text) {
		if entry.ChecksumType != checksumType {
			continue
		}
		if entry.Filename == filename || path.Base(entry.Filename) == filename {
			return entry.Checksum, true
		}
	}
	return "", false
}

func cleanManifestFilename(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "*")
	value = strings.TrimPrefix(value, "./")
	value = strings.Trim(value, `"'`)
	return value
}

func looksLikeHash(value string) bool {
	if len(value) < 32 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func normalizeChecksumType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, "sums")
	value = strings.TrimSuffix(value, "sum")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func inferChecksumType(checksum string) string {
	switch len(checksum) {
	case 32:
		return "md5"
	case 40:
		return "sha1"
	case 64:
		return "sha256"
	case 128:
		return "sha512"
	default:
		return ""
	}
}

func selectChecksumMatch(text string, pattern *regexp.Regexp) (filename, version, checksum string, ok bool) {
	for _, entry := range parseChecksumManifest(text) {
		base := path.Base(entry.Filename)
		match := pattern.FindStringSubmatch(base)
		if match == nil {
			continue
		}
		candidateVersion := ""
		if len(match) > 1 {
			candidateVersion = match[1]
		}
		if !ok || compareVersions(candidateVersion, version) > 0 {
			filename = base
			version = candidateVersion
			checksum = entry.Checksum
			ok = true
		}
	}
	return filename, version, checksum, ok
}

func selectChecksumMatchOfType(text string, pattern *regexp.Regexp, checksumType string) (filename, version, checksum string, ok bool) {
	checksumType = normalizeChecksumType(checksumType)
	for _, entry := range parseChecksumManifest(text) {
		if entry.ChecksumType != checksumType {
			continue
		}
		base := path.Base(entry.Filename)
		match := pattern.FindStringSubmatch(base)
		if match == nil {
			continue
		}
		candidateVersion := ""
		if len(match) > 1 {
			candidateVersion = match[1]
		}
		if !ok || compareVersions(candidateVersion, version) > 0 {
			filename = base
			version = candidateVersion
			checksum = entry.Checksum
			ok = true
		}
	}
	return filename, version, checksum, ok
}

func compareVersions(a, b string) int {
	ta := versionTokens(a)
	tb := versionTokens(b)
	for i := 0; i < len(ta) || i < len(tb); i++ {
		if i >= len(ta) {
			return -1
		}
		if i >= len(tb) {
			return 1
		}
		if ta[i].num && tb[i].num {
			if ta[i].n < tb[i].n {
				return -1
			}
			if ta[i].n > tb[i].n {
				return 1
			}
			continue
		}
		if ta[i].s < tb[i].s {
			return -1
		}
		if ta[i].s > tb[i].s {
			return 1
		}
	}
	return 0
}

type versionToken struct {
	num bool
	n   int
	s   string
}

func versionTokens(value string) []versionToken {
	re := regexp.MustCompile(`[0-9]+|[A-Za-z]+`)
	var tokens []versionToken
	for _, raw := range re.FindAllString(value, -1) {
		if n, err := strconv.Atoi(raw); err == nil {
			tokens = append(tokens, versionToken{num: true, n: n})
		} else {
			tokens = append(tokens, versionToken{s: strings.ToLower(raw)})
		}
	}
	return tokens
}

func firstMatch(text string, patterns ...string) string {
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(text)
		if match != nil && len(match) > 1 {
			return html.UnescapeString(match[1])
		}
	}
	return ""
}

func htmlToText(text string) string {
	text = regexp.MustCompile(`(?is)<(br|p|div|tr|td|th|li|h[1-6])\b[^>]*>`).ReplaceAllString(text, "\n")
	text = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = regexp.MustCompile(`[ \t\r\f\v]+`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(` *\n *`).ReplaceAllString(text, "\n")
	return strings.TrimSpace(text)
}

func defaultArch(arch string) string {
	if arch == "" {
		return "amd64"
	}
	return arch
}

func defaultTrack(track, fallback string) string {
	if track == "" {
		return fallback
	}
	return track
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func releaseURL(base, filename string) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return strings.TrimRight(base, "/") + "/" + url.PathEscape(filename)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + filename
	return parsed.String()
}

func getFirstText(ctx context.Context, client *HTTPClient, bases []string, filename string) (base, text string, err error) {
	var messages []string
	for _, candidate := range bases {
		candidate = strings.TrimRight(candidate, "/") + "/"
		rawURL := candidate + filename
		text, err := client.GetText(ctx, rawURL, manifestLimit)
		if err == nil {
			return candidate, text, nil
		}
		messages = append(messages, fmt.Sprintf("%s: %v", rawURL, err))
	}
	return "", "", fmt.Errorf("all release metadata sources failed: %s", strings.Join(messages, "; "))
}

func headSize(ctx context.Context, client *HTTPClient, rawURL string) int64 {
	resp, err := client.Head(ctx, rawURL)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.ContentLength
}

func imageName(id string) string {
	switch id {
	case "ubuntu-desktop":
		return "Ubuntu Desktop"
	case "ubuntu-server":
		return "Ubuntu Server"
	case "debian-netinst":
		return "Debian Netinst"
	case "debian-live-standard":
		return "Debian Live Standard"
	case "kali-live":
		return "Kali Live"
	case "linux-mint-cinnamon":
		return "Linux Mint Cinnamon"
	case "linux-mint-mate":
		return "Linux Mint MATE"
	case "linux-mint-xfce":
		return "Linux Mint Xfce"
	case "clonezilla":
		return "Clonezilla Live"
	case "gparted":
		return "GParted Live"
	case "tails":
		return "Tails"
	case "popos":
		return "Pop!_OS"
	case "popos-nvidia":
		return "Pop!_OS NVIDIA"
	case "hirens":
		return "Hiren's BootCD PE"
	default:
		return id
	}
}

type Ubuntu struct{}

func (Ubuntu) ID() string   { return "ubuntu" }
func (Ubuntu) Name() string { return "Ubuntu" }

func (Ubuntu) Detect(filename string) (config.Image, bool) {
	re := regexp.MustCompile(`^ubuntu-([0-9]+\.[0-9]+)(?:\.[0-9]+)?-(desktop|live-server)-([A-Za-z0-9_]+)\.iso$`)
	match := re.FindStringSubmatch(filename)
	if match == nil {
		return config.Image{}, false
	}
	flavor := "desktop"
	id := "ubuntu-desktop"
	if match[2] == "live-server" {
		flavor = "server"
		id = "ubuntu-server"
	}
	return config.Image{
		ID:       id,
		Name:     imageName(id),
		Provider: "ubuntu",
		Track:    match[1],
		Arch:     match[3],
		Flavor:   flavor,
		Filename: filename,
	}, true
}

func (Ubuntu) Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error) {
	track := defaultTrack(image.Track, "24.04")
	arch := defaultArch(image.Arch)
	kind := "desktop"
	if image.Flavor == "server" || image.ID == "ubuntu-server" {
		kind = "live-server"
	}
	manifestURL := fmt.Sprintf("https://releases.ubuntu.com/%s/SHA256SUMS", url.PathEscape(track))
	text, err := client.GetText(ctx, manifestURL, manifestLimit)
	if err != nil {
		return Release{}, err
	}
	pattern := regexp.MustCompile(fmt.Sprintf(`^ubuntu-([0-9.]+)-%s-%s\.iso$`, regexp.QuoteMeta(kind), regexp.QuoteMeta(arch)))
	filename, version, checksum, ok := selectChecksumMatch(text, pattern)
	if !ok {
		return Release{}, fmt.Errorf("no Ubuntu %s %s image found in %s", kind, arch, manifestURL)
	}
	rawURL := releaseURL("https://releases.ubuntu.com/"+track+"/", filename)
	return Release{
		ImageID:      image.ID,
		Provider:     "ubuntu",
		Name:         imageName(image.ID),
		Version:      version,
		Filename:     filename,
		URL:          rawURL,
		Checksum:     checksum,
		ChecksumType: "sha256",
		Size:         headSize(ctx, client, rawURL),
	}, nil
}

type Debian struct{}

func (Debian) ID() string   { return "debian" }
func (Debian) Name() string { return "Debian" }

func (Debian) Detect(filename string) (config.Image, bool) {
	if match := regexp.MustCompile(`^debian-([0-9.]+)-([A-Za-z0-9_]+)-netinst\.iso$`).FindStringSubmatch(filename); match != nil {
		return config.Image{
			ID:       "debian-netinst",
			Name:     imageName("debian-netinst"),
			Provider: "debian",
			Track:    "stable",
			Arch:     match[2],
			Flavor:   "netinst",
			Filename: filename,
		}, true
	}
	if match := regexp.MustCompile(`^debian-live-([0-9.]+)-([A-Za-z0-9_]+)-standard\.iso$`).FindStringSubmatch(filename); match != nil {
		return config.Image{
			ID:       "debian-live-standard",
			Name:     imageName("debian-live-standard"),
			Provider: "debian",
			Track:    "stable",
			Arch:     match[2],
			Flavor:   "live-standard",
			Filename: filename,
		}, true
	}
	return config.Image{}, false
}

func (Debian) Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error) {
	arch := defaultArch(image.Arch)
	flavor := defaultTrack(image.Flavor, "netinst")
	var bases []string
	var pattern *regexp.Regexp
	if flavor == "live-standard" || image.ID == "debian-live-standard" {
		bases = []string{
			fmt.Sprintf("https://ftp.us.debian.org/debian-cdimage/current-live/%s/iso-hybrid/", url.PathEscape(arch)),
			fmt.Sprintf("https://cdimage.debian.org/debian-cd/current-live/%s/iso-hybrid/", url.PathEscape(arch)),
			fmt.Sprintf("https://cloudfront.debian.net/cdimage/release/current-live/%s/iso-hybrid/", url.PathEscape(arch)),
		}
		pattern = regexp.MustCompile(fmt.Sprintf(`^debian-live-([0-9.]+)-%s-standard\.iso$`, regexp.QuoteMeta(arch)))
	} else {
		bases = []string{
			fmt.Sprintf("https://ftp.us.debian.org/debian-cdimage/current/%s/iso-cd/", url.PathEscape(arch)),
			fmt.Sprintf("https://cdimage.debian.org/debian-cd/current/%s/iso-cd/", url.PathEscape(arch)),
			fmt.Sprintf("https://cloudfront.debian.net/cdimage/release/current/%s/iso-cd/", url.PathEscape(arch)),
		}
		pattern = regexp.MustCompile(fmt.Sprintf(`^debian-([0-9.]+)-%s-netinst\.iso$`, regexp.QuoteMeta(arch)))
	}
	base, text, err := getFirstText(ctx, client, bases, "SHA512SUMS")
	if err != nil {
		return Release{}, err
	}
	filename, version, checksum, ok := selectChecksumMatchOfType(text, pattern, "sha512")
	if !ok {
		return Release{}, fmt.Errorf("no Debian %s %s image found in %s", flavor, arch, base+"SHA512SUMS")
	}
	rawURL := releaseURL(base, filename)
	return Release{
		ImageID:      image.ID,
		Provider:     "debian",
		Name:         imageName(image.ID),
		Version:      version,
		Filename:     filename,
		URL:          rawURL,
		Checksum:     checksum,
		ChecksumType: "sha512",
		Size:         headSize(ctx, client, rawURL),
	}, nil
}

type Kali struct{}

func (Kali) ID() string   { return "kali" }
func (Kali) Name() string { return "Kali" }

func (Kali) Detect(filename string) (config.Image, bool) {
	re := regexp.MustCompile(`^kali-linux-([0-9.]+)-(live|installer|installer-netinst)-([A-Za-z0-9_]+)\.iso$`)
	match := re.FindStringSubmatch(filename)
	if match == nil {
		return config.Image{}, false
	}
	id := "kali-" + match[2]
	if match[2] == "live" {
		id = "kali-live"
	}
	return config.Image{
		ID:       id,
		Name:     imageName(id),
		Provider: "kali",
		Track:    "current",
		Arch:     match[3],
		Flavor:   match[2],
		Filename: filename,
	}, true
}

func (Kali) Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error) {
	arch := defaultArch(image.Arch)
	flavor := defaultTrack(image.Flavor, "live")
	base := "https://kali.download/base-images/current/"
	text, err := client.GetText(ctx, base+"SHA256SUMS", manifestLimit)
	if err != nil {
		return Release{}, err
	}
	pattern := regexp.MustCompile(fmt.Sprintf(`^kali-linux-([0-9.]+)-%s-%s\.iso$`, regexp.QuoteMeta(flavor), regexp.QuoteMeta(arch)))
	filename, version, checksum, ok := selectChecksumMatch(text, pattern)
	if !ok {
		return Release{}, fmt.Errorf("no Kali %s %s image found in %s", flavor, arch, base+"SHA256SUMS")
	}
	rawURL, err := kaliDownloadURL(ctx, client, version, filename)
	if err != nil {
		return Release{}, err
	}
	return Release{
		ImageID:      image.ID,
		Provider:     "kali",
		Name:         imageName(image.ID),
		Version:      version,
		Filename:     filename,
		URL:          rawURL,
		Checksum:     checksum,
		ChecksumType: "sha256",
		Size:         headSize(ctx, client, rawURL),
	}, nil
}

func kaliDownloadURL(ctx context.Context, client *HTTPClient, version, filename string) (string, error) {
	var lastErr error
	for _, candidate := range kaliDownloadCandidates(version, filename) {
		if _, err := client.ProbeDownload(ctx, candidate); err == nil {
			return candidate, nil
		} else {
			lastErr = err
		}
	}
	if strings.Contains(filename, "-live-") {
		return "", Skipf("Kali lists %s in SHA256SUMS, but official mirrors currently expose the live image as torrent-only", filename)
	}
	return "", fmt.Errorf("Kali lists %s in SHA256SUMS, but no official direct ISO URL responded: %v", filename, lastErr)
}

func kaliDownloadCandidates(version, filename string) []string {
	escapedVersion := url.PathEscape(version)
	escapedFilename := url.PathEscape(filename)
	return []string{
		fmt.Sprintf("https://cdimage.kali.org/kali-%s/%s", escapedVersion, escapedFilename),
		fmt.Sprintf("https://cdimage.kali.org/current/%s", escapedFilename),
		fmt.Sprintf("https://archive.kali.org/kali-images/kali-%s/%s", escapedVersion, escapedFilename),
		fmt.Sprintf("https://archive.kali.org/kali-images/current/%s", escapedFilename),
		fmt.Sprintf("https://kali.download/base-images/kali-%s/%s", escapedVersion, escapedFilename),
		fmt.Sprintf("https://kali.download/base-images/current/%s", escapedFilename),
	}
}

type LinuxMint struct{}

func (LinuxMint) ID() string   { return "linuxmint" }
func (LinuxMint) Name() string { return "Linux Mint" }

func (LinuxMint) Detect(filename string) (config.Image, bool) {
	re := regexp.MustCompile(`^linuxmint-([0-9.]+)-(cinnamon|mate|xfce)-64bit\.iso$`)
	match := re.FindStringSubmatch(filename)
	if match == nil {
		return config.Image{}, false
	}
	id := "linux-mint-" + match[2]
	return config.Image{
		ID:       id,
		Name:     imageName(id),
		Provider: "linuxmint",
		Track:    "stable",
		Arch:     "amd64",
		Flavor:   match[2],
		Filename: filename,
	}, true
}

func (LinuxMint) Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error) {
	track := image.Track
	if track == "" || track == "stable" {
		latest, err := latestLinuxMintTrack(ctx, client)
		if err != nil {
			return Release{}, err
		}
		track = latest
	}
	flavor := defaultTrack(image.Flavor, "cinnamon")
	base := fmt.Sprintf("https://pub.linuxmint.io/stable/%s/", url.PathEscape(track))
	text, err := client.GetText(ctx, base+"sha256sum.txt", manifestLimit)
	if err != nil {
		return Release{}, err
	}
	pattern := regexp.MustCompile(fmt.Sprintf(`^linuxmint-([0-9.]+)-%s-64bit\.iso$`, regexp.QuoteMeta(flavor)))
	filename, version, checksum, ok := selectChecksumMatch(text, pattern)
	if !ok {
		return Release{}, fmt.Errorf("no Linux Mint %s image found in %s", flavor, base+"sha256sum.txt")
	}
	rawURL := releaseURL(base, filename)
	return Release{
		ImageID:      image.ID,
		Provider:     "linuxmint",
		Name:         imageName(image.ID),
		Version:      version,
		Filename:     filename,
		URL:          rawURL,
		Checksum:     checksum,
		ChecksumType: "sha256",
		Size:         headSize(ctx, client, rawURL),
	}, nil
}

func latestLinuxMintTrack(ctx context.Context, client *HTTPClient) (string, error) {
	text, err := client.GetText(ctx, "https://pub.linuxmint.io/stable/", manifestLimit)
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`href=["']([0-9]+(?:\.[0-9]+)+)/["']`)
	matches := re.FindAllStringSubmatch(text, -1)
	latest := ""
	for _, match := range matches {
		if latest == "" || compareVersions(match[1], latest) > 0 {
			latest = match[1]
		}
	}
	if latest == "" {
		return "", fmt.Errorf("could not discover latest Linux Mint stable track")
	}
	return latest, nil
}

type Clonezilla struct{}

func (Clonezilla) ID() string   { return "clonezilla" }
func (Clonezilla) Name() string { return "Clonezilla" }

func (Clonezilla) Detect(filename string) (config.Image, bool) {
	match := regexp.MustCompile(`^clonezilla-live-(.+)-([A-Za-z0-9_]+)\.iso$`).FindStringSubmatch(filename)
	if match == nil {
		return config.Image{}, false
	}
	return config.Image{
		ID:       "clonezilla",
		Name:     imageName("clonezilla"),
		Provider: "clonezilla",
		Track:    "stable",
		Arch:     match[2],
		Flavor:   "live",
		Filename: filename,
	}, true
}

func (Clonezilla) Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error) {
	arch := defaultArch(image.Arch)
	version := image.Track
	checksum := ""
	filename := ""
	baseURL := ""
	if version == "" || version == "stable" {
		latestFilename, latestVersion, latestChecksum, latestBaseURL, err := latestClonezillaRelease(ctx, client, arch)
		if err != nil {
			return Release{}, err
		}
		filename = latestFilename
		version = latestVersion
		checksum = latestChecksum
		baseURL = latestBaseURL
	}
	if filename == "" {
		filename = fmt.Sprintf("clonezilla-live-%s-%s.iso", version, arch)
	}
	if checksum == "" {
		var err error
		checksum, err = clonezillaChecksum(ctx, client, filename)
		if err != nil {
			return Release{}, err
		}
	}
	rawURL := releaseURL(baseURL, filename)
	if baseURL == "" {
		rawURL = fmt.Sprintf("https://sourceforge.net/projects/clonezilla/files/clonezilla_live_stable/%s/%s/download", url.PathEscape(version), url.PathEscape(filename))
	}
	return Release{
		ImageID:      image.ID,
		Provider:     "clonezilla",
		Name:         imageName("clonezilla"),
		Version:      version,
		Filename:     filename,
		URL:          rawURL,
		Checksum:     checksum,
		ChecksumType: "sha256",
		Size:         headSize(ctx, client, rawURL),
	}, nil
}

func latestClonezillaRelease(ctx context.Context, client *HTTPClient, arch string) (filename, version, checksum, baseURL string, err error) {
	bases := []string{
		"https://free.nchc.org.tw/clonezilla-live/stable/",
		"https://clonezilla.nchc.org.tw/clonezilla-live/stable/",
		"https://clonezilla.org/downloads/stable/",
	}
	base, text, err := getFirstText(ctx, client, bases, "SHA256SUMS")
	if err != nil {
		return "", "", "", "", err
	}
	pattern := regexp.MustCompile(fmt.Sprintf(`^clonezilla-live-([0-9][A-Za-z0-9_.-]+)-%s\.iso$`, regexp.QuoteMeta(arch)))
	filename, version, checksum, ok := selectChecksumMatchOfType(text, pattern, "sha256")
	if !ok {
		return "", "", "", "", fmt.Errorf("no Clonezilla stable %s ISO found in SHA256SUMS", arch)
	}
	return filename, version, checksum, base, nil
}

func latestClonezillaVersion(ctx context.Context, client *HTTPClient, arch string) (string, error) {
	text, err := client.GetText(ctx, "https://clonezilla.org/downloads/download.php?branch=stable", manifestLimit)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "alternative stable") {
			continue
		}
		if version := firstMatch(line, `\bstable\s*-\s*([0-9][A-Za-z0-9_.-]+)`); version != "" {
			return version, nil
		}
	}
	re := regexp.MustCompile(fmt.Sprintf(`clonezilla-live-([0-9][A-Za-z0-9_.-]+)-%s\.iso`, regexp.QuoteMeta(arch)))
	match := re.FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("could not discover Clonezilla stable version")
	}
	return match[1], nil
}

func clonezillaChecksum(ctx context.Context, client *HTTPClient, filename string) (string, error) {
	urls := []string{
		"https://free.nchc.org.tw/clonezilla-live/stable/SHA256SUMS",
		"https://clonezilla.nchc.org.tw/clonezilla-live/stable/SHA256SUMS",
		"https://clonezilla.org/downloads/stable/checksums.php",
		"https://clonezilla.org/downloads/stable/CHECKSUMS.TXT",
	}
	var lastErr error
	for _, rawURL := range urls {
		text, err := client.GetText(ctx, rawURL, manifestLimit)
		if err != nil {
			lastErr = err
			continue
		}
		if checksum, ok := findChecksumOfType(text, filename, "sha256"); ok {
			return checksum, nil
		}
		lastErr = fmt.Errorf("checksum for %s not found in %s", filename, rawURL)
	}
	return "", lastErr
}

type GParted struct{}

func (GParted) ID() string   { return "gparted" }
func (GParted) Name() string { return "GParted" }

func (GParted) Detect(filename string) (config.Image, bool) {
	match := regexp.MustCompile(`^gparted-live-(.+)-([A-Za-z0-9_]+)\.iso$`).FindStringSubmatch(filename)
	if match == nil {
		return config.Image{}, false
	}
	return config.Image{
		ID:       "gparted",
		Name:     imageName("gparted"),
		Provider: "gparted",
		Track:    "stable",
		Arch:     match[2],
		Flavor:   "live",
		Filename: filename,
	}, true
}

func (GParted) Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error) {
	arch := defaultArch(image.Arch)
	version := image.Track
	checksum := ""
	filename := ""
	if version == "" || version == "stable" {
		latestFilename, latestVersion, latestChecksum, err := latestGPartedRelease(ctx, client, arch)
		if err != nil {
			return Release{}, err
		}
		filename = latestFilename
		version = latestVersion
		checksum = latestChecksum
	}
	if filename == "" {
		filename = fmt.Sprintf("gparted-live-%s-%s.iso", version, arch)
	}
	if checksum == "" {
		var err error
		checksum, err = gpartedChecksum(ctx, client, filename)
		if err != nil {
			return Release{}, err
		}
	}
	rawURL := fmt.Sprintf("https://sourceforge.net/projects/gparted/files/gparted-live-stable/%s/%s/download", url.PathEscape(version), url.PathEscape(filename))
	return Release{
		ImageID:      image.ID,
		Provider:     "gparted",
		Name:         imageName("gparted"),
		Version:      version,
		Filename:     filename,
		URL:          rawURL,
		Checksum:     checksum,
		ChecksumType: "sha256",
		Size:         headSize(ctx, client, rawURL),
	}, nil
}

func latestGPartedRelease(ctx context.Context, client *HTTPClient, arch string) (filename, version, checksum string, err error) {
	text, err := client.GetText(ctx, "https://gparted.org/gparted-live/stable/CHECKSUMS.TXT", manifestLimit)
	if err != nil {
		return latestGPartedReleaseFromSourceForge(ctx, client, arch, err)
	}
	pattern := regexp.MustCompile(fmt.Sprintf(`^gparted-live-([0-9][A-Za-z0-9_.-]+)-%s\.iso$`, regexp.QuoteMeta(arch)))
	filename, version, checksum, ok := selectChecksumMatchOfType(text, pattern, "sha256")
	if !ok {
		return latestGPartedReleaseFromSourceForge(ctx, client, arch, fmt.Errorf("no GParted stable %s ISO found in CHECKSUMS.TXT", arch))
	}
	return filename, version, checksum, nil
}

func latestGPartedVersion(ctx context.Context, client *HTTPClient, arch string) (string, error) {
	text, err := client.GetText(ctx, "https://gparted.org/download.php", manifestLimit)
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(fmt.Sprintf(`gparted-live-([0-9][A-Za-z0-9_.-]+)-%s\.iso`, regexp.QuoteMeta(arch)))
	match := re.FindStringSubmatch(text)
	if match == nil {
		return "", fmt.Errorf("could not discover GParted stable version")
	}
	return match[1], nil
}

func gpartedChecksum(ctx context.Context, client *HTTPClient, filename string) (string, error) {
	urls := []string{
		"https://gparted.org/gparted-live/stable/CHECKSUMS.TXT",
	}
	var lastErr error
	for _, rawURL := range urls {
		text, err := client.GetText(ctx, rawURL, manifestLimit)
		if err != nil {
			lastErr = err
			continue
		}
		if checksum, ok := findChecksumOfType(text, filename, "sha256"); ok {
			return checksum, nil
		}
		lastErr = fmt.Errorf("checksum for %s not found in %s", filename, rawURL)
	}
	match := regexp.MustCompile(`^gparted-live-(.+)-[A-Za-z0-9_]+\.iso$`).FindStringSubmatch(filename)
	if match != nil {
		if checksum, err := gpartedChecksumFromSourceForge(ctx, client, match[1], filename); err == nil {
			return checksum, nil
		} else {
			lastErr = err
		}
	}
	return "", Skipf("GParted checksum metadata unavailable for %s: %v", filename, lastErr)
}

func latestGPartedReleaseFromSourceForge(ctx context.Context, client *HTTPClient, arch string, priorErr error) (filename, version, checksum string, err error) {
	const sourceForgeStable = "https://sourceforge.net/projects/gparted/files/gparted-live-stable/"
	text, err := client.GetText(ctx, sourceForgeStable, manifestLimit)
	if err != nil {
		return "", "", "", Skipf("GParted stable metadata unavailable: %v; fallback failed: %v", priorErr, err)
	}
	pattern := regexp.MustCompile(fmt.Sprintf(`gparted-live-([0-9][A-Za-z0-9_.-]+)-%s\.iso`, regexp.QuoteMeta(arch)))
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		candidateVersion := match[1]
		if version == "" || compareVersions(candidateVersion, version) > 0 {
			version = candidateVersion
			filename = fmt.Sprintf("gparted-live-%s-%s.iso", version, arch)
		}
	}
	if filename == "" {
		return "", "", "", Skipf("GParted stable %s ISO was not found in SourceForge fallback after CHECKSUMS.TXT failed: %v", arch, priorErr)
	}
	checksum, err = gpartedChecksumFromSourceForge(ctx, client, version, filename)
	if err != nil {
		return "", "", "", Skipf("GParted checksum metadata unavailable for %s: %v", filename, err)
	}
	return filename, version, checksum, nil
}

func gpartedChecksumFromSourceForge(ctx context.Context, client *HTTPClient, version, filename string) (string, error) {
	rawURL := fmt.Sprintf("https://sourceforge.net/projects/gparted/files/gparted-live-stable/%s/", url.PathEscape(version))
	text, err := client.GetText(ctx, rawURL, manifestLimit)
	if err != nil {
		return "", err
	}
	if checksum, ok := findChecksumOfType(text, filename, "sha256"); ok {
		return checksum, nil
	}
	if checksum, ok := findGPartedChecksumInSourceForgeText(text, filename); ok {
		return checksum, nil
	}
	return "", fmt.Errorf("sha256 checksum for %s not found in %s", filename, rawURL)
}

func findGPartedChecksumInSourceForgeText(text, filename string) (string, bool) {
	text = htmlToText(text)
	pattern := regexp.MustCompile(`(?i)([a-f0-9]{64})\s+` + regexp.QuoteMeta(filename))
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		return "", false
	}
	return strings.ToLower(match[1]), true
}

type Tails struct{}

func (Tails) ID() string   { return "tails" }
func (Tails) Name() string { return "Tails" }

func (Tails) Detect(filename string) (config.Image, bool) {
	match := regexp.MustCompile(`^tails-amd64-([0-9.]+)\.(iso|img)$`).FindStringSubmatch(filename)
	if match == nil {
		return config.Image{}, false
	}
	return config.Image{
		ID:       "tails",
		Name:     imageName("tails"),
		Provider: "tails",
		Track:    "stable",
		Arch:     "amd64",
		Flavor:   match[2],
		Filename: filename,
	}, true
}

func (Tails) Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error) {
	flavor := defaultTrack(image.Flavor, "iso")
	data, err := client.GetBytes(ctx, "https://tails.net/install/v2/Tails/amd64/stable/latest.json", manifestLimit)
	if err != nil {
		return Release{}, err
	}
	var latest tailsLatest
	if err := json.Unmarshal(data, &latest); err != nil {
		return Release{}, err
	}
	for _, installation := range latest.Installations {
		for _, installPath := range installation.InstallationPaths {
			if installPath.Type != flavor {
				continue
			}
			if len(installPath.TargetFiles) == 0 {
				continue
			}
			target := installPath.TargetFiles[0]
			filename := path.Base(target.URL)
			return Release{
				ImageID:      image.ID,
				Provider:     "tails",
				Name:         imageName("tails"),
				Version:      installation.Version,
				Filename:     filename,
				URL:          target.URL,
				Checksum:     strings.ToLower(target.SHA256),
				ChecksumType: "sha256",
				Size:         target.Size,
			}, nil
		}
	}
	return Release{}, fmt.Errorf("no Tails %s image found in latest.json", flavor)
}

type tailsLatest struct {
	Installations []struct {
		Version           string `json:"version"`
		InstallationPaths []struct {
			Type        string `json:"type"`
			TargetFiles []struct {
				SHA256 string `json:"sha256"`
				Size   int64  `json:"size"`
				URL    string `json:"url"`
			} `json:"target-files"`
		} `json:"installation-paths"`
	} `json:"installations"`
}

type PopOS struct{}

func (PopOS) ID() string   { return "popos" }
func (PopOS) Name() string { return "Pop!_OS" }

func (PopOS) Detect(filename string) (config.Image, bool) {
	lower := strings.ToLower(filename)
	if !strings.HasSuffix(lower, ".iso") || !strings.HasPrefix(lower, "pop-os_") && !strings.HasPrefix(lower, "pop_os_") && !strings.HasPrefix(lower, "pop-os-") && !strings.HasPrefix(lower, "pop_os-") {
		return config.Image{}, false
	}
	if !strings.Contains(lower, "amd64") && !strings.Contains(lower, "x86_64") {
		return config.Image{}, false
	}
	match := regexp.MustCompile(`(?i)^pop[-_]os[_-]([0-9]+(?:\.[0-9]+)*)`).FindStringSubmatch(filename)
	if match == nil {
		return config.Image{}, false
	}
	flavor := popOSFlavorFromFilename(filename, "generic")
	id := "popos"
	if flavor == "nvidia" {
		id = "popos-nvidia"
	}
	return config.Image{
		ID:       id,
		Name:     imageName(id),
		Provider: "popos",
		Track:    match[1],
		Arch:     "amd64",
		Flavor:   flavor,
		Filename: filename,
	}, true
}

func (PopOS) Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error) {
	text, err := client.GetText(ctx, "https://system76.com/download-pop/", manifestLimit)
	if err != nil {
		return Release{}, Skipf("Pop!_OS official download page unavailable: %v", err)
	}
	flavor := popOSFlavorFromFilename(image.Filename, defaultTrack(image.Flavor, "generic"))
	isoURL, pos := selectPopOSURL(text, flavor, image.Track)
	if isoURL == "" {
		return Release{}, Skipf("Pop!_OS %s ISO URL is not exposed in the official page HTML", flavor)
	}
	checksum := nearestSHA256(text, pos)
	if checksum == "" {
		return Release{}, Skipf("Pop!_OS %s checksum is not exposed near the ISO URL on the official page", flavor)
	}
	filename := path.Base(mustURLPath(isoURL))
	version := firstMatch(filename, `(?i)pop[-_]os[_-]([0-9.]+)`)
	return Release{
		ImageID:      image.ID,
		Provider:     "popos",
		Name:         imageName(image.ID),
		Version:      version,
		Filename:     filename,
		URL:          isoURL,
		Checksum:     strings.ToLower(checksum),
		ChecksumType: "sha256",
		Size:         headSize(ctx, client, isoURL),
	}, nil
}

func popOSFlavorFromFilename(filename, fallback string) string {
	lower := strings.ToLower(filename)
	if strings.Contains(lower, "nvidia") {
		return "nvidia"
	}
	if fallback == "" || fallback == "intel" {
		return "generic"
	}
	return fallback
}

func selectPopOSURL(text, flavor, track string) (string, int) {
	re := regexp.MustCompile(`https?://[^"' <]+\.iso`)
	matches := re.FindAllStringIndex(text, -1)
	for _, match := range matches {
		raw := html.UnescapeString(text[match[0]:match[1]])
		lower := strings.ToLower(raw)
		if track != "" && !strings.Contains(lower, strings.ToLower(track)) {
			continue
		}
		if flavor == "nvidia" {
			if strings.Contains(lower, "nvidia") {
				return raw, match[0]
			}
			continue
		}
		if strings.Contains(lower, "amd64") && !strings.Contains(lower, "nvidia") {
			return raw, match[0]
		}
	}
	return "", -1
}

func nearestSHA256(text string, pos int) string {
	if pos < 0 {
		pos = 0
	}
	start := pos - 2500
	if start < 0 {
		start = 0
	}
	end := pos + 2500
	if end > len(text) {
		end = len(text)
	}
	re := regexp.MustCompile(`(?i)[a-f0-9]{64}`)
	matches := re.FindAllString(text[start:end], -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.ToLower(matches[0])
}

func mustURLPath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return parsed.Path
}

type Hirens struct{}

func (Hirens) ID() string   { return "hirens" }
func (Hirens) Name() string { return "Hiren's BootCD PE" }

func (Hirens) Detect(filename string) (config.Image, bool) {
	if !regexp.MustCompile(`(?i)^HBCD_PE_x64.*\.iso$`).MatchString(filename) {
		return config.Image{}, false
	}
	return config.Image{
		ID:       "hirens",
		Name:     imageName("hirens"),
		Provider: "hirens",
		Track:    "stable",
		Arch:     "amd64",
		Flavor:   "pe",
		Filename: filename,
	}, true
}

func (Hirens) Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error) {
	text, err := client.GetText(ctx, "https://www.hirensbootcd.org/download/", manifestLimit)
	if err != nil {
		return Release{}, Skipf("Hiren's BootCD PE official download page unavailable: %v", err)
	}
	plainText := htmlToText(text)
	checksum := firstMatch(plainText, `(?i)ISO SHA-?256\s*(?:\||:)?\s*([a-f0-9]{64})`, `(?i)SHA-?256[^a-f0-9]+([a-f0-9]{64})`)
	if checksum == "" {
		return Release{}, Skipf("Hiren's BootCD PE SHA256 checksum is not exposed in the official page HTML")
	}
	version := firstMatch(plainText, `(?i)Hiren'?s BootCD PE x64 \(v([^)]+)\)`, `(?i)\(v([0-9.]+)\)`)
	isoURL := firstMatch(text, `href=["']([^"']*HBCD_PE_x64[^"']*\.iso[^"']*)["']`)
	if isoURL == "" {
		isoURL = "https://www.hirensbootcd.org/files/HBCD_PE_x64.iso"
	} else if parsed, err := url.Parse(isoURL); err == nil && !parsed.IsAbs() {
		base, _ := url.Parse("https://www.hirensbootcd.org/download/")
		isoURL = base.ResolveReference(parsed).String()
	}
	downloadURL, err := hirensDownloadURL(ctx, client, isoURL)
	if err != nil {
		return Release{}, err
	}
	filename := path.Base(mustURLPath(downloadURL))
	if filename == "" || filename == "." || filename == "/" {
		filename = "HBCD_PE_x64.iso"
	}
	return Release{
		ImageID:      image.ID,
		Provider:     "hirens",
		Name:         imageName("hirens"),
		Version:      version,
		Filename:     filename,
		URL:          downloadURL,
		Checksum:     strings.ToLower(checksum),
		ChecksumType: "sha256",
		Size:         headSize(ctx, client, downloadURL),
	}, nil
}

func hirensDownloadURL(ctx context.Context, client *HTTPClient, primaryURL string) (string, error) {
	var lastErr error
	for _, candidate := range hirensDownloadCandidates(primaryURL) {
		if _, err := client.ProbeDownload(ctx, candidate); err == nil {
			return candidate, nil
		} else {
			lastErr = err
		}
	}
	return "", Skipf("Hiren's BootCD PE mirrors are not currently reachable without downloading the ISO: %v", lastErr)
}

func hirensDownloadCandidates(primaryURL string) []string {
	candidates := []string{primaryURL}
	for _, candidate := range []string{
		"https://hirensbootcd.mirror.wearetriple.com/HBCD_PE_x64.iso",
		"https://hbcd.mirror.garr.it/mirrors/hbcd/HBCD_PE_x64.iso",
		"https://mirrors.uni-ruse.bg/hirens-bootcd/HBCD_PE_x64.iso",
		"https://mirror.internet.asn.au/pub/hbcd/HBCD_PE_x64.iso",
		"https://www2.frugalware.org/mirror/hirensbootcd.org/HBCD_PE_x64.iso",
		"https://mirror.lstn.net/hirensbootcd/HBCD_PE_x64.iso",
	} {
		if candidate != primaryURL {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}
