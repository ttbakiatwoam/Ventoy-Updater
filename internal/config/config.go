package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

const DefaultManifestName = ".ventoy-update.yaml"

type Manifest struct {
	Version int           `json:"version"`
	Images  []Image       `json:"images"`
	Manual  []ManualImage `json:"manual"`
}

type Image struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider"`
	Track    string `json:"track,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Flavor   string `json:"flavor,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type ManualImage struct {
	Filename string `json:"filename"`
}

func NewManifest() Manifest {
	return Manifest{Version: 1}
}

func Load(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()

	return Parse(f)
}

func Save(path string, manifest Manifest) error {
	var out bytes.Buffer
	if err := Write(&out, manifest); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func Parse(r io.Reader) (Manifest, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Manifest{}, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return Manifest{}, fmt.Errorf("empty manifest")
	}
	if trimmed[0] == '{' {
		var manifest Manifest
		if err := json.Unmarshal(trimmed, &manifest); err != nil {
			return Manifest{}, err
		}
		normalize(&manifest)
		return manifest, nil
	}

	manifest, err := parseYAMLSubset(bytes.NewReader(trimmed))
	if err != nil {
		return Manifest{}, err
	}
	normalize(&manifest)
	return manifest, nil
}

func Write(w io.Writer, manifest Manifest) error {
	normalize(&manifest)

	bw := bufio.NewWriter(w)
	if _, err := fmt.Fprintf(bw, "version: %d\n", manifest.Version); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(bw, "images:"); err != nil {
		return err
	}
	for _, image := range manifest.Images {
		if _, err := fmt.Fprintf(bw, "  - id: %s\n", quote(image.ID)); err != nil {
			return err
		}
		fields := []struct {
			key   string
			value string
		}{
			{"name", image.Name},
			{"provider", image.Provider},
			{"track", image.Track},
			{"arch", image.Arch},
			{"flavor", image.Flavor},
			{"filename", image.Filename},
		}
		for _, field := range fields {
			if field.value == "" {
				continue
			}
			if _, err := fmt.Fprintf(bw, "    %s: %s\n", field.key, quote(field.value)); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintln(bw, "manual:"); err != nil {
		return err
	}
	for _, image := range manifest.Manual {
		if image.Filename == "" {
			continue
		}
		if _, err := fmt.Fprintf(bw, "  - filename: %s\n", quote(image.Filename)); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func normalize(manifest *Manifest) {
	if manifest.Version == 0 {
		manifest.Version = 1
	}
	for i := range manifest.Images {
		manifest.Images[i].ID = strings.TrimSpace(manifest.Images[i].ID)
		manifest.Images[i].Provider = strings.TrimSpace(manifest.Images[i].Provider)
		manifest.Images[i].Arch = defaultString(strings.TrimSpace(manifest.Images[i].Arch), "amd64")
		manifest.Images[i].Track = strings.TrimSpace(manifest.Images[i].Track)
		manifest.Images[i].Flavor = strings.TrimSpace(manifest.Images[i].Flavor)
		manifest.Images[i].Filename = strings.TrimSpace(manifest.Images[i].Filename)
		if manifest.Images[i].Name == "" {
			manifest.Images[i].Name = humanName(manifest.Images[i].ID)
		}
	}
	sort.SliceStable(manifest.Images, func(i, j int) bool {
		return manifest.Images[i].ID < manifest.Images[j].ID
	})
	sort.SliceStable(manifest.Manual, func(i, j int) bool {
		return manifest.Manual[i].Filename < manifest.Manual[j].Filename
	})
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func humanName(id string) string {
	id = strings.ReplaceAll(id, "-", " ")
	if id == "" {
		return "Image"
	}
	words := strings.Fields(id)
	for i := range words {
		words[i] = strings.ToUpper(words[i][:1]) + words[i][1:]
	}
	return strings.Join(words, " ")
}

func parseYAMLSubset(r io.Reader) (Manifest, error) {
	var manifest Manifest
	var section string
	var current *Image
	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := stripComment(scanner.Text())
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := countIndent(line)
		trimmed := strings.TrimSpace(line)

		if indent == 0 {
			key, value, ok := splitKeyValue(trimmed)
			if !ok {
				return Manifest{}, fmt.Errorf("manifest line %d: expected top-level key", lineNo)
			}
			switch key {
			case "version":
				v, err := strconv.Atoi(value)
				if err != nil {
					return Manifest{}, fmt.Errorf("manifest line %d: invalid version %q", lineNo, value)
				}
				manifest.Version = v
				section = ""
			case "images", "manual":
				section = key
				current = nil
			default:
				return Manifest{}, fmt.Errorf("manifest line %d: unsupported top-level key %q", lineNo, key)
			}
			continue
		}

		switch section {
		case "images":
			if strings.HasPrefix(trimmed, "- ") {
				manifest.Images = append(manifest.Images, Image{})
				current = &manifest.Images[len(manifest.Images)-1]
				key, value, ok := splitKeyValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
				if !ok {
					return Manifest{}, fmt.Errorf("manifest line %d: expected image field", lineNo)
				}
				setImageField(current, key, unquote(value))
				continue
			}
			if current == nil {
				return Manifest{}, fmt.Errorf("manifest line %d: image field before image item", lineNo)
			}
			key, value, ok := splitKeyValue(trimmed)
			if !ok {
				return Manifest{}, fmt.Errorf("manifest line %d: expected image field", lineNo)
			}
			setImageField(current, key, unquote(value))
		case "manual":
			if !strings.HasPrefix(trimmed, "- ") {
				return Manifest{}, fmt.Errorf("manifest line %d: expected manual item", lineNo)
			}
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			key, value, ok := splitKeyValue(item)
			if ok {
				if key != "filename" {
					return Manifest{}, fmt.Errorf("manifest line %d: unsupported manual field %q", lineNo, key)
				}
				manifest.Manual = append(manifest.Manual, ManualImage{Filename: unquote(value)})
			} else {
				manifest.Manual = append(manifest.Manual, ManualImage{Filename: unquote(item)})
			}
		default:
			return Manifest{}, fmt.Errorf("manifest line %d: nested content outside a section", lineNo)
		}
	}
	if err := scanner.Err(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func setImageField(image *Image, key, value string) {
	switch key {
	case "id":
		image.ID = value
	case "name":
		image.Name = value
	case "provider":
		image.Provider = value
	case "track":
		image.Track = value
	case "arch":
		image.Arch = value
	case "flavor":
		image.Flavor = value
	case "filename":
		image.Filename = value
	}
}

func stripComment(line string) string {
	inQuote := false
	var quoteChar rune
	for i, r := range line {
		if (r == '"' || r == '\'') && (i == 0 || line[i-1] != '\\') {
			if inQuote && r == quoteChar {
				inQuote = false
				continue
			}
			if !inQuote {
				inQuote = true
				quoteChar = r
				continue
			}
		}
		if r == '#' && !inQuote {
			return line[:i]
		}
	}
	return line
}

func countIndent(line string) int {
	count := 0
	for _, r := range line {
		if r != ' ' {
			break
		}
		count++
	}
	return count
}

func splitKeyValue(line string) (string, string, bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	if key == "" {
		return "", "", false
	}
	return key, unquote(value), true
}

func quote(value string) string {
	if value == "" {
		return `""`
	}
	needsQuote := false
	for _, r := range value {
		if r == ':' || r == '#' || r == '"' || r == '\'' || r == ' ' || r == '\t' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return value
	}
	return strconv.Quote(value)
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		if value[0] == '\'' && value[len(value)-1] == '\'' {
			return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		}
	}
	return value
}

func IDs(manifest Manifest) map[string]struct{} {
	ids := make(map[string]struct{}, len(manifest.Images))
	for _, image := range manifest.Images {
		ids[image.ID] = struct{}{}
	}
	return ids
}
