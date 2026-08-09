package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ventoy-update/internal/config"
	"ventoy-update/internal/lockfile"
	"ventoy-update/internal/logging"
	"ventoy-update/internal/providers"
	"ventoy-update/internal/storage"
)

type options struct {
	target       string
	manifestPath string
	dryRun       bool
	only         string
	keepOld      bool
	noDelete     bool
	cleanup      bool
	jsonLog      bool
	allowSkips   bool
	summaryPath  string
	timeout      time.Duration
	retries      int
}

type result struct {
	Image   config.Image
	Release providers.Release
	Status  string
	Error   error
	Manual  bool
}

func Main(args []string) int {
	if err := run(args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func run(args []string, stdout, stderr io.Writer) error {
	command, flagArgs, err := splitCommand(args)
	if err != nil {
		printUsage(stdout)
		return err
	}
	if command == "help" {
		printUsage(stdout)
		return nil
	}
	if command == "version" {
		fmt.Fprintln(stdout, "ventoy-update 0.1.0")
		return nil
	}

	opts, err := parseOptions(flagArgs, stderr)
	if err != nil {
		return err
	}
	logger := logging.New(stderr, opts.jsonLog)

	ctx := context.Background()
	if command == "validate" {
		if opts.manifestPath == "" {
			opts.manifestPath = filepath.Join("examples", "ventoy-update.yaml")
		}
		return runValidate(ctx, opts, stdout, logger)
	}

	target, err := storage.ResolveTarget(opts.target)
	if err != nil {
		return err
	}
	if err := storage.ValidateVentoyTarget(target); err != nil {
		return err
	}
	if opts.manifestPath == "" {
		opts.manifestPath = storage.DefaultManifestPath(target)
	}

	switch command {
	case "scan":
		return runScan(ctx, opts, target, stdout, logger)
	case "check":
		return runCheck(ctx, opts, target, stdout, logger)
	case "update":
		return runUpdate(ctx, opts, target, stdout, logger)
	default:
		printUsage(stdout)
		return fmt.Errorf("unknown command %q", command)
	}
}

func splitCommand(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, errors.New("missing command")
	}
	commands := map[string]struct{}{
		"scan":     {},
		"check":    {},
		"update":   {},
		"validate": {},
		"version":  {},
		"help":     {},
	}
	for i, arg := range args {
		if _, ok := commands[arg]; ok {
			flagArgs := make([]string, 0, len(args)-1)
			flagArgs = append(flagArgs, args[:i]...)
			flagArgs = append(flagArgs, args[i+1:]...)
			return arg, flagArgs, nil
		}
	}
	return "", nil, errors.New("missing command")
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var opts options
	var timeout string
	fs := flag.NewFlagSet("ventoy-update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.target, "target", "", "Ventoy image volume path")
	fs.StringVar(&opts.manifestPath, "manifest", "", "manifest path")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "show changes without writing")
	fs.StringVar(&opts.only, "only", "", "comma-separated image IDs to process")
	fs.BoolVar(&opts.keepOld, "keep-old", false, "keep superseded images")
	fs.BoolVar(&opts.noDelete, "no-delete", false, "never delete files")
	fs.BoolVar(&opts.cleanup, "cleanup", false, "remove macOS metadata and stale staging files")
	fs.BoolVar(&opts.jsonLog, "json-log", false, "emit JSON logs")
	fs.BoolVar(&opts.allowSkips, "allow-skips", false, "allow provider skips for unavailable metadata or direct ISO links")
	fs.StringVar(&opts.summaryPath, "summary", "", "append a Markdown validation summary to this path")
	fs.StringVar(&timeout, "timeout", "90s", "per-request timeout")
	fs.IntVar(&opts.retries, "retries", 2, "network retry count")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() > 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	parsedTimeout, err := time.ParseDuration(timeout)
	if err != nil {
		return options{}, fmt.Errorf("invalid --timeout: %w", err)
	}
	opts.timeout = parsedTimeout
	return opts, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: ventoy-update [flags] <scan|check|update|validate>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "common flags:")
	fmt.Fprintln(w, "  --target PATH       Ventoy image volume path; defaults to /Volumes/Ventoy on macOS")
	fmt.Fprintln(w, "  --manifest PATH     Manifest path; defaults to TARGET/.ventoy-update.yaml")
	fmt.Fprintln(w, "  --dry-run           Show changes without writing")
	fmt.Fprintln(w, "  --only IDS          Comma-separated image IDs to process")
	fmt.Fprintln(w, "  --keep-old          Keep superseded images")
	fmt.Fprintln(w, "  --no-delete         Never delete files")
	fmt.Fprintln(w, "  --cleanup           Remove macOS metadata and stale staging files")
	fmt.Fprintln(w, "  --json-log          Emit structured JSON logs to stderr")
	fmt.Fprintln(w, "  --allow-skips       Allow provider skips for unavailable metadata or direct ISO links")
	fmt.Fprintln(w, "  --summary PATH      Append a Markdown validation summary")
}

func runScan(ctx context.Context, opts options, target string, stdout io.Writer, logger *logging.Logger) error {
	_ = ctx
	if opts.cleanup {
		if opts.dryRun {
			logger.Info("dry run cleanup", logging.Fields{"target": target})
		} else if err := storage.Cleanup(target, !opts.noDelete, logger); err != nil {
			return err
		}
	}
	files, err := storage.ListImages(target)
	if err != nil {
		return err
	}
	sort.Strings(files)

	manifest := config.NewManifest()
	seenIDs := map[string]struct{}{}
	seenManual := map[string]struct{}{}
	for _, filename := range files {
		if image, ok := providers.Detect(filename); ok {
			if _, exists := seenIDs[image.ID]; exists {
				logger.Warn("duplicate managed image detected; leaving duplicate manual", logging.Fields{
					"id":       image.ID,
					"filename": filename,
				})
				addManual(&manifest, seenManual, filename)
				continue
			}
			seenIDs[image.ID] = struct{}{}
			manifest.Images = append(manifest.Images, image)
			continue
		}
		addManual(&manifest, seenManual, filename)
	}

	if opts.dryRun {
		if err := config.Write(stdout, manifest); err != nil {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(opts.manifestPath), 0o755); err != nil {
		return err
	}
	if err := config.Save(opts.manifestPath, manifest); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s\n", opts.manifestPath)
	fmt.Fprintf(stdout, "managed images: %d\n", len(manifest.Images))
	fmt.Fprintf(stdout, "manual images: %d\n", len(manifest.Manual))
	return nil
}

func addManual(manifest *config.Manifest, seen map[string]struct{}, filename string) {
	if _, exists := seen[filename]; exists {
		return
	}
	seen[filename] = struct{}{}
	manifest.Manual = append(manifest.Manual, config.ManualImage{Filename: filename})
}

func runCheck(ctx context.Context, opts options, target string, stdout io.Writer, logger *logging.Logger) error {
	manifest, err := config.Load(opts.manifestPath)
	if err != nil {
		return err
	}
	results := resolveManifest(ctx, opts, target, manifest, logger)
	printResults(stdout, results)
	return resultError(results)
}

func runUpdate(ctx context.Context, opts options, target string, stdout io.Writer, logger *logging.Logger) error {
	manifest, err := config.Load(opts.manifestPath)
	if err != nil {
		return err
	}
	if opts.cleanup {
		if opts.dryRun {
			logger.Info("dry run cleanup", logging.Fields{"target": target})
		} else if err := storage.Cleanup(target, !opts.noDelete, logger); err != nil {
			return err
		}
	}

	var lock *lockfile.Lock
	if !opts.dryRun {
		if _, err := storage.EnsureToolDir(target); err != nil {
			return err
		}
		acquired, err := lockfile.Acquire(filepath.Join(target, storage.ToolDirName, "lock"))
		if err != nil {
			return err
		}
		lock = acquired
		defer lock.Release()
	}

	results := resolveManifest(ctx, opts, target, manifest, logger)
	printResults(stdout, results)
	if err := resultError(results); err != nil {
		return err
	}

	client := providers.NewHTTPClient(opts.timeout, opts.retries, logger)
	changed := false
	for i := range manifest.Images {
		image := manifest.Images[i]
		if !included(image.ID, opts.only) {
			continue
		}
		matched := findResult(results, image.ID)
		if matched == nil {
			continue
		}
		if matched.Status != "UPDATE" && matched.Status != "MISSING" {
			continue
		}
		err := storage.DownloadRelease(ctx, client, target, matched.Release, image.Filename, storage.DownloadOptions{
			DryRun:   opts.dryRun,
			KeepOld:  opts.keepOld,
			NoDelete: opts.noDelete,
		}, logger)
		if err != nil {
			matched.Error = err
			matched.Status = "ERROR"
			continue
		}
		manifest.Images[i].Filename = matched.Release.Filename
		changed = true
	}
	if err := resultError(results); err != nil {
		return err
	}
	if changed && !opts.dryRun {
		if err := config.Save(opts.manifestPath, manifest); err != nil {
			return err
		}
	}
	if opts.dryRun {
		fmt.Fprintln(stdout, "dry run: no files changed")
	} else if changed {
		fmt.Fprintf(stdout, "updated %s\n", opts.manifestPath)
	} else {
		fmt.Fprintln(stdout, "nothing to update")
	}
	return nil
}

func resolveManifest(ctx context.Context, opts options, target string, manifest config.Manifest, logger *logging.Logger) []*result {
	client := providers.NewHTTPClient(opts.timeout, opts.retries, logger)
	registry := providers.Registry()
	var results []*result
	for _, image := range manifest.Images {
		if !included(image.ID, opts.only) {
			continue
		}
		provider, ok := registry[image.Provider]
		if !ok {
			results = append(results, &result{Image: image, Status: "ERROR", Error: fmt.Errorf("unknown provider %q", image.Provider)})
			continue
		}
		release, err := provider.Latest(ctx, client, image)
		if err != nil {
			status := "ERROR"
			if providers.IsSkipped(err) {
				status = "SKIP"
			}
			results = append(results, &result{Image: image, Status: status, Error: err})
			continue
		}
		status := "CURRENT"
		if image.Filename == "" || !fileExists(filepath.Join(target, image.Filename)) {
			status = "MISSING"
		} else if release.Filename != image.Filename {
			status = "UPDATE"
		}
		results = append(results, &result{Image: image, Release: release, Status: status})
	}
	for _, manual := range manifest.Manual {
		if opts.only != "" {
			continue
		}
		image := config.Image{
			ID:       manual.Filename,
			Name:     manual.Filename,
			Provider: "manual",
			Filename: manual.Filename,
		}
		status := "MANUAL"
		if !fileExists(filepath.Join(target, manual.Filename)) {
			status = "MISSING"
		}
		results = append(results, &result{Image: image, Status: status, Manual: true})
	}
	return results
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func included(id, only string) bool {
	if only == "" {
		return true
	}
	for _, item := range strings.Split(only, ",") {
		if strings.TrimSpace(item) == id {
			return true
		}
	}
	return false
}

func findResult(results []*result, id string) *result {
	for _, result := range results {
		if result.Image.ID == id {
			return result
		}
	}
	return nil
}

func printResults(w io.Writer, results []*result) {
	fmt.Fprintf(w, "%-28s %-34s %-34s %-10s\n", "IMAGE", "INSTALLED", "AVAILABLE", "STATUS")
	fmt.Fprintf(w, "%-28s %-34s %-34s %-10s\n", strings.Repeat("-", 28), strings.Repeat("-", 34), strings.Repeat("-", 34), strings.Repeat("-", 10))
	for _, result := range results {
		available := result.Release.Filename
		if result.Manual {
			available = "manual"
		}
		if result.Error != nil {
			available = result.Error.Error()
		}
		fmt.Fprintf(w, "%-28s %-34s %-34s %-10s\n", truncate(result.Image.Name, 28), truncate(result.Image.Filename, 34), truncate(available, 34), result.Status)
	}
}

func truncate(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	return value[:width-3] + "..."
}

func resultError(results []*result) error {
	var messages []string
	for _, result := range results {
		if result.Error != nil && !providers.IsSkipped(result.Error) {
			messages = append(messages, fmt.Sprintf("%s: %v", result.Image.ID, result.Error))
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return errors.New(strings.Join(messages, "; "))
}
