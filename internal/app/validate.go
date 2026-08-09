package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ventoy-update/internal/config"
	"ventoy-update/internal/logging"
	"ventoy-update/internal/providers"
)

type validationResult struct {
	Image   config.Image
	Release providers.Release
	Probe   providers.ProbeResult
	Status  string
	Error   error
	Manual  bool
}

func runValidate(ctx context.Context, opts options, stdout io.Writer, logger *logging.Logger) error {
	manifest, err := config.Load(opts.manifestPath)
	if err != nil {
		return err
	}

	client := providers.NewHTTPClient(opts.timeout, opts.retries, logger)
	registry := providers.Registry()
	results := make([]validationResult, 0, len(manifest.Images)+len(manifest.Manual))

	for _, image := range manifest.Images {
		if !included(image.ID, opts.only) {
			continue
		}
		provider, ok := registry[image.Provider]
		if !ok {
			results = append(results, validationResult{
				Image:  image,
				Status: "ERROR",
				Error:  fmt.Errorf("unknown provider %q", image.Provider),
			})
			continue
		}

		release, err := provider.Latest(ctx, client, image)
		if err != nil {
			status := "ERROR"
			if providers.IsSkipped(err) {
				status = "SKIP"
			}
			results = append(results, validationResult{
				Image:  image,
				Status: status,
				Error:  err,
			})
			continue
		}

		if err := validateReleaseMetadata(release); err != nil {
			results = append(results, validationResult{
				Image:   image,
				Release: release,
				Status:  "ERROR",
				Error:   err,
			})
			continue
		}

		probe, err := client.ProbeDownload(ctx, release.URL)
		if err != nil {
			results = append(results, validationResult{
				Image:   image,
				Release: release,
				Status:  "ERROR",
				Error:   err,
			})
			continue
		}

		results = append(results, validationResult{
			Image:   image,
			Release: release,
			Probe:   probe,
			Status:  "OK",
		})
	}

	if opts.only == "" {
		for _, manual := range manifest.Manual {
			results = append(results, validationResult{
				Image: config.Image{
					ID:       manual.Filename,
					Name:     manual.Filename,
					Provider: "manual",
					Filename: manual.Filename,
				},
				Status: "MANUAL",
				Manual: true,
			})
		}
	}

	printValidationResults(stdout, results)
	if opts.summaryPath != "" {
		if err := appendValidationSummary(opts.summaryPath, results, opts); err != nil {
			return err
		}
	}
	return validationError(results, opts.allowSkips)
}

func validateReleaseMetadata(release providers.Release) error {
	var missing []string
	if release.URL == "" {
		missing = append(missing, "url")
	}
	if release.Filename == "" {
		missing = append(missing, "filename")
	}
	if release.Checksum == "" {
		missing = append(missing, "checksum")
	}
	if release.ChecksumType == "" {
		missing = append(missing, "checksum_type")
	}
	if len(missing) > 0 {
		return fmt.Errorf("release metadata missing %s", strings.Join(missing, ", "))
	}

	lower := strings.ToLower(release.Filename)
	if !strings.HasSuffix(lower, ".iso") && !strings.HasSuffix(lower, ".img") {
		return fmt.Errorf("release filename is not an ISO or IMG: %s", release.Filename)
	}

	expectedLength := checksumLength(release.ChecksumType)
	if expectedLength == 0 {
		return fmt.Errorf("unsupported checksum type %q", release.ChecksumType)
	}
	if len(release.Checksum) != expectedLength {
		return fmt.Errorf("%s checksum has %d hex characters, expected %d", release.ChecksumType, len(release.Checksum), expectedLength)
	}
	for _, r := range release.Checksum {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return fmt.Errorf("%s checksum contains non-hex character %q", release.ChecksumType, r)
	}
	return nil
}

func checksumLength(checksumType string) int {
	switch strings.ToLower(checksumType) {
	case "md5":
		return 32
	case "sha1":
		return 40
	case "sha256":
		return 64
	case "sha512":
		return 128
	default:
		return 0
	}
}

func printValidationResults(w io.Writer, results []validationResult) {
	fmt.Fprintf(w, "%-28s %-34s %-11s %-6s %-9s %s\n", "IMAGE", "AVAILABLE", "HTTP", "PROBE", "STATUS", "DETAIL")
	fmt.Fprintf(w, "%-28s %-34s %-11s %-6s %-9s %s\n", strings.Repeat("-", 28), strings.Repeat("-", 34), strings.Repeat("-", 11), strings.Repeat("-", 6), strings.Repeat("-", 9), strings.Repeat("-", 32))
	for _, result := range results {
		httpStatus := ""
		probeMethod := ""
		detail := ""
		available := result.Release.Filename
		if result.Manual {
			available = "manual-only"
			detail = "not validated by design"
		}
		if result.Error != nil {
			detail = result.Error.Error()
		}
		if result.Probe.Status != "" {
			httpStatus = result.Probe.Status
			probeMethod = result.Probe.Method
		}
		if result.Probe.ContentLength > 0 {
			detail = fmt.Sprintf("%d bytes", result.Probe.ContentLength)
		}
		if result.Probe.ContentRange != "" {
			detail = result.Probe.ContentRange
		}
		fmt.Fprintf(w, "%-28s %-34s %-11s %-6s %-9s %s\n",
			truncate(result.Image.Name, 28),
			truncate(available, 34),
			truncate(httpStatus, 11),
			truncate(probeMethod, 6),
			result.Status,
			truncate(detail, 80),
		)
	}
}

func appendValidationSummary(path string, results []validationResult, opts options) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintln(file, "## Ventoy ISO Availability Validation")
	fmt.Fprintln(file)
	fmt.Fprintln(file, "This check resolves provider metadata and probes ISO URLs with `HEAD` first, falling back to a one-byte range request when a server rejects `HEAD`. It does not download ISO or IMG bodies.")
	fmt.Fprintln(file)
	fmt.Fprintln(file, "| Image | Available | HTTP | Probe | Status | Detail |")
	fmt.Fprintln(file, "| --- | --- | --- | --- | --- | --- |")
	for _, result := range results {
		available := result.Release.Filename
		if result.Manual {
			available = "manual-only"
		}
		httpStatus := result.Probe.Status
		detail := ""
		if result.Error != nil {
			detail = result.Error.Error()
		}
		if result.Manual {
			detail = "not validated by design"
		}
		if result.Probe.ContentLength > 0 {
			detail = fmt.Sprintf("%d bytes", result.Probe.ContentLength)
		}
		if result.Probe.ContentRange != "" {
			detail = result.Probe.ContentRange
		}
		fmt.Fprintf(file, "| %s | %s | %s | %s | %s | %s |\n",
			escapeMarkdown(result.Image.Name),
			escapeMarkdown(available),
			escapeMarkdown(httpStatus),
			escapeMarkdown(result.Probe.Method),
			escapeMarkdown(result.Status),
			escapeMarkdown(detail),
		)
	}
	if opts.allowSkips {
		fmt.Fprintln(file)
		fmt.Fprintln(file, "Validation was run with `--allow-skips`; skipped providers did not fail the job.")
	}
	fmt.Fprintln(file)
	return nil
}

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "|", `\|`)
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func validationError(results []validationResult, allowSkips bool) error {
	var messages []string
	for _, result := range results {
		if result.Error == nil {
			continue
		}
		if providers.IsSkipped(result.Error) && allowSkips {
			continue
		}
		messages = append(messages, fmt.Sprintf("%s: %v", result.Image.ID, result.Error))
	}
	if len(messages) == 0 {
		return nil
	}
	return errors.New(strings.Join(messages, "; "))
}
