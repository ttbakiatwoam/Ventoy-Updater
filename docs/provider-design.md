# Provider Design

Each provider implements:

```go
type Provider interface {
	ID() string
	Name() string
	Detect(filename string) (config.Image, bool)
	Latest(ctx context.Context, client *HTTPClient, image config.Image) (Release, error)
}
```

The app never downloads an image unless the provider returns a release URL and
a checksum. Providers should prefer upstream checksum manifests or structured
metadata. Official HTML pages are used only when the upstream does not publish a
stable manifest or API for ISO metadata.

## Current Sources

- Ubuntu: `https://releases.ubuntu.com/<track>/SHA256SUMS`
- Debian netinst: `https://cdimage.debian.org/debian-cd/current/<arch>/iso-cd/SHA512SUMS`
- Debian live: `https://cdimage.debian.org/debian-cd/current-live/<arch>/iso-hybrid/SHA512SUMS`
- Kali: `https://kali.download/base-images/current/SHA256SUMS`
- Linux Mint: `https://pub.linuxmint.io/stable/<track>/sha256sum.txt`
- Clonezilla: official stable page plus SourceForge `SHA256SUMS`
- GParted: official download page plus SourceForge `CHECKSUMS.TXT`
- Tails: `https://tails.net/install/v2/Tails/amd64/stable/latest.json`
- Pop!_OS: official System76 download page
- Hiren's BootCD PE: official Hiren's BootCD PE download page

## Adding A Provider

1. Add a provider type in `internal/providers`.
2. Implement filename detection for existing Ventoy images.
3. Resolve the newest release from an official source.
4. Return a checksum type and checksum with every release.
5. Add detection and checksum parsing tests.

