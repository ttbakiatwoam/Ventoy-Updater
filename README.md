# ventoy-update

**License:** [Creative Commons Attribution-ShareAlike 4.0 International](LICENSE.txt)  
**Last validation result:** [![PR Validation](https://github.com/ttbakiatwoam/Ventoy-Updater/actions/workflows/pr-validation.yml/badge.svg)](https://github.com/ttbakiatwoam/Ventoy-Updater/actions/workflows/pr-validation.yml)

`ventoy-update` is a manifest-driven updater for a Ventoy USB image partition.
It scans the existing image set, checks upstream releases, and downloads updates
transactionally with checksum verification before replacing anything.

The project is intentionally dependency-free Go. It uses official checksum
manifests and APIs where upstreams publish them, and falls back to official
download pages only for projects that do not expose a stable machine-readable
release feed.

## Supported Images

- Ubuntu Desktop and Ubuntu Server
- Debian netinst and Debian live standard
- Kali live
- Linux Mint Cinnamon, MATE, and Xfce
- Clonezilla Live
- GParted Live
- Tails ISO or IMG
- Pop!_OS generic and NVIDIA
- Hiren's BootCD PE

Windows installer ISOs and custom WinPE images are treated as manual-only. They
are scanned into the manifest but never updated automatically.

## Provider Contributions

Pull requests targeting the `master` branch are welcome when adding a new
provider and supported ISO file. A provider PR should include filename
detection, release metadata resolution from an official upstream source,
checksum verification, manifest/example updates, and focused provider tests.

## Build

```bash
go build ./cmd/ventoy-update
```

## Basic Use

On macOS, the tool automatically checks `/Volumes/Ventoy` when `--target` is not
provided.

```bash
./ventoy-update scan
./ventoy-update check
./ventoy-update update
./ventoy-update validate --manifest examples/ventoy-update.yaml
```

With an explicit target:

```bash
./ventoy-update --target /Volumes/Ventoy scan
./ventoy-update --target /Volumes/Ventoy check
./ventoy-update --target /Volumes/Ventoy update
```

## Useful Flags

- `--dry-run`: show intended changes without writing or downloading.
- `--only id1,id2`: limit `check` or `update` to specific manifest image IDs.
- `--keep-old`: keep superseded image files after a successful update.
- `--no-delete`: never delete old image files or stale tool files.
- `--cleanup`: remove macOS metadata files and old staging files.
- `--json-log`: emit structured JSON logs to stderr.
- `--timeout 90s`: per-request timeout.
- `--retries 3`: retry count for transient network failures.
- `--manifest path`: override the manifest path.
- `--summary path`: append a Markdown validation summary.
- `--allow-skips`: allow validation skips for providers without machine-readable
  metadata.

## Safety Model

`ventoy-update` refuses to operate unless the target looks like a Ventoy image
volume. A target is accepted when one of these is true:

- Its volume/directory name is `Ventoy`, case-insensitive.
- It already contains `.ventoy-update.yaml`.
- It contains a `ventoy` directory.

Downloads are staged under `.ventoy-update/downloads`, resumed when possible,
verified against upstream checksums, then atomically renamed into place on the
same filesystem. If replacement of an existing file is needed, the old file is
renamed aside first so it can be restored if the final rename fails.

## Manifest

The scanner writes `.ventoy-update.yaml` to the target volume by default:

```yaml
version: 1
images:
  - id: ubuntu-desktop
    name: Ubuntu Desktop
    provider: ubuntu
    track: "24.04"
    arch: amd64
    flavor: desktop
    filename: ubuntu-24.04.2-desktop-amd64.iso
manual:
  - filename: Win11_24H2_English_x64.iso
```

The parser supports the YAML subset written by this tool and also accepts JSON
manifests with the same field names.

## Provider Notes

- Ubuntu uses `SHA256SUMS` from `releases.ubuntu.com`.
- Debian uses `SHA512SUMS` from `cdimage.debian.org`.
- Kali uses `SHA256SUMS` from `kali.download/base-images/current`.
- Linux Mint uses `sha256sum.txt` from `pub.linuxmint.io`.
- Tails uses the official `latest.json` release metadata.
- GParted and Clonezilla use project checksum files linked from official pages.
- Pop!_OS and Hiren's BootCD PE use their official download pages for release
  metadata because no stable checksum manifest endpoint is currently published.

## PR Validation

Pull requests run `.github/workflows/pr-validation.yml`. The workflow builds the
CLI, runs the Go test suite, and runs:

```bash
./ventoy-update validate --manifest examples/ventoy-update.yaml --timeout 90s
```

The validation command resolves each managed provider from the manifest,
confirms release filenames and checksums are present, and probes each ISO or IMG
URL with `HEAD` only. It does not download image bodies. Windows installer ISOs
and custom WinPE images stay manual-only and are reported as `MANUAL`.

## Local Verification

Run:

```bash
go test ./...
go build ./cmd/ventoy-update
```
