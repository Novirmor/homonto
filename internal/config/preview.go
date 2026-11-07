package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Preview is one [preview.<name>] declaration: a local Docker container that
// serves a swarm candidate's checkout of Repo over HTTP, under fixed resource
// budgets (ADR 0115). Exactly one of Dockerfile (an image built from the
// checkout) or Image (a stock image with the checkout mounted read-only at
// /workspace) is set.
type Preview struct {
	Repo       string   `toml:"repo" json:"repo"`
	Dockerfile string   `toml:"dockerfile" json:"dockerfile,omitempty"`
	Context    string   `toml:"context" json:"context,omitempty"`
	Image      string   `toml:"image" json:"image,omitempty"`
	Command    []string `toml:"command" json:"command,omitempty"`
	Port       int      `toml:"port" json:"port"`
	Path       string   `toml:"path" json:"path,omitempty"`
	CPUs       float64  `toml:"cpus" json:"cpus,omitempty"`
	Memory     string   `toml:"memory" json:"memory,omitempty"`
	Pids       int      `toml:"pids" json:"pids,omitempty"`
	TTL        string   `toml:"ttl" json:"ttl,omitempty"`
	Startup    string   `toml:"startup" json:"startup,omitempty"`
}

// Preview budget defaults and ceilings. The ceilings keep a typo from
// claiming the whole machine; three candidates run at once.
const (
	DefaultPreviewCPUs    = 1.0
	DefaultPreviewMemory  = "512m"
	DefaultPreviewPids    = 256
	DefaultPreviewTTL     = 30 * time.Minute
	DefaultPreviewStartup = 5 * time.Minute
	maxPreviewCPUs        = 8.0
	minPreviewMemory      = 32 << 20
	maxPreviewMemory      = 8 << 30
	maxPreviewPids        = 4096
	maxPreviewTTL         = 24 * time.Hour
	maxPreviewStartup     = 30 * time.Minute
)

// PreviewBudget is a preview's resolved resource limits.
type PreviewBudget struct {
	CPUs        float64       `json:"cpus"`
	MemoryBytes int64         `json:"memory_bytes"`
	Pids        int           `json:"pids"`
	TTL         time.Duration `json:"ttl"`
	Startup     time.Duration `json:"startup"`
}

// Budget returns the preview's limits with defaults applied. Load validated
// them, so parse errors cannot occur for a loaded config.
func (p Preview) Budget() PreviewBudget {
	b := PreviewBudget{CPUs: p.CPUs, Pids: p.Pids, TTL: DefaultPreviewTTL, Startup: DefaultPreviewStartup}
	if b.CPUs == 0 {
		b.CPUs = DefaultPreviewCPUs
	}
	if b.Pids == 0 {
		b.Pids = DefaultPreviewPids
	}
	mem := p.Memory
	if mem == "" {
		mem = DefaultPreviewMemory
	}
	b.MemoryBytes, _ = ParseMemory(mem)
	if p.TTL != "" {
		b.TTL, _ = time.ParseDuration(p.TTL)
	}
	if p.Startup != "" {
		b.Startup, _ = time.ParseDuration(p.Startup)
	}
	return b
}

// PathOrDefault is the URL path a preview is probed and linked at.
func (p Preview) PathOrDefault() string {
	if p.Path == "" {
		return "/"
	}
	return p.Path
}

// ContextOrDefault is the build context relative to the checkout.
func (p Preview) ContextOrDefault() string {
	if p.Context == "" {
		return "."
	}
	return p.Context
}

// ParseMemory reads a Docker-style size: a positive integer with an optional
// b, k, m or g suffix (binary units).
func ParseMemory(value string) (int64, error) {
	s := strings.ToLower(strings.TrimSpace(value))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "g"):
		mult, s = 1<<30, strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "m"):
		mult, s = 1<<20, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "k"):
		mult, s = 1<<10, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "b"):
		s = strings.TrimSuffix(s, "b")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n > (1<<62)/mult {
		return 0, fmt.Errorf("memory %q is not a positive size such as 512m or 1g", value)
	}
	return n * mult, nil
}

func relativeInside(p string) bool {
	clean := filepath.Clean(p)
	return p != "" && !filepath.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) && !strings.ContainsAny(p, "\\\x00\r\n")
}

var previewName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func validatePreviews(c *Config) error {
	names := make([]string, 0, len(c.Preview))
	for name := range c.Preview {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		p := c.Preview[name]
		label := "preview." + name
		// The name becomes part of Docker container and image names.
		if !previewName.MatchString(name) {
			return fmt.Errorf("%s: name must be lowercase letters, digits and '-', starting with a letter or digit (at most 32)", label)
		}
		if c.SchemaVersion != 2 {
			return fmt.Errorf("%s requires schema_version = 2 and explicit [repos]", label)
		}
		if _, ok := c.Repos[p.Repo]; !ok {
			return fmt.Errorf("%s: repo %q is not a declared repository", label, p.Repo)
		}
		if (p.Dockerfile == "") == (p.Image == "") {
			return fmt.Errorf("%s: set exactly one of dockerfile (build from the checkout) or image (mount the checkout)", label)
		}
		if p.Dockerfile != "" {
			if !relativeInside(p.Dockerfile) {
				return fmt.Errorf("%s: dockerfile must be relative to the checkout", label)
			}
			if p.Context != "" && !relativeInside(p.Context) {
				return fmt.Errorf("%s: context must be relative to the checkout (use . for its root)", label)
			}
			if len(p.Command) > 0 {
				return fmt.Errorf("%s: command applies only with image; a built image runs its own CMD", label)
			}
		} else {
			if p.Context != "" {
				return fmt.Errorf("%s: context applies only with dockerfile", label)
			}
			if strings.ContainsAny(p.Image, " \t\r\n\x00") || strings.HasPrefix(p.Image, "-") {
				return fmt.Errorf("%s: image %q is not an image reference", label, p.Image)
			}
			for _, arg := range p.Command {
				if strings.ContainsRune(arg, 0) {
					return fmt.Errorf("%s: command contains NUL", label)
				}
			}
		}
		if p.Port < 1 || p.Port > 65535 {
			return fmt.Errorf("%s: port must be the container port the preview serves HTTP on (1-65535)", label)
		}
		if p.Path != "" && (!strings.HasPrefix(p.Path, "/") || strings.ContainsAny(p.Path, " \t\r\n\x00")) {
			return fmt.Errorf("%s: path must start with /", label)
		}
		if p.CPUs < 0 || p.CPUs > maxPreviewCPUs {
			return fmt.Errorf("%s: cpus must be between 0 and %g", label, maxPreviewCPUs)
		}
		if p.Memory != "" {
			n, err := ParseMemory(p.Memory)
			if err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			if n < minPreviewMemory || n > maxPreviewMemory {
				return fmt.Errorf("%s: memory must be between 32m and 8g", label)
			}
		}
		if p.Pids < 0 || p.Pids > maxPreviewPids {
			return fmt.Errorf("%s: pids must be between 1 and %d", label, maxPreviewPids)
		}
		for _, d := range []struct {
			key, value string
			max        time.Duration
		}{{"ttl", p.TTL, maxPreviewTTL}, {"startup", p.Startup, maxPreviewStartup}} {
			if d.value == "" {
				continue
			}
			v, err := time.ParseDuration(d.value)
			if err != nil || v <= 0 || v > d.max {
				return fmt.Errorf("%s: %s must be a positive duration up to %s", label, d.key, d.max)
			}
		}
	}
	return nil
}

// PreviewsFor selects previews whose repository changed, in stable order.
func (c *Config) PreviewsFor(changed []string) []string {
	var names []string
	for name, p := range c.Preview {
		if slices.Contains(changed, p.Repo) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
