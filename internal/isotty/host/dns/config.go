// Package dns changes DNS records from the VM through the host's gcloud
// login, under the names the human pinned. See docs/DNS.md.
package dns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

const ProviderCloudDNS = "clouddns"

var (
	projectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	zonePattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	labelPattern   = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?$`)
)

// Zone is one agreement: in this managed zone, the VM may change the
// records at these names and at every name under them. The record type is
// part of the ask, not of the agreement: a mistake stays under the name
// whatever the type, and the audit log carries the type.
type Zone struct {
	Project     string   `json:"project"`
	ManagedZone string   `json:"managed_zone"`
	Names       []string `json:"names"`
}

// ProjectConfig names the DNS provider the project's zones live at and the
// zones the VM may change records in. One provider per project: a project
// that spans providers is not something IsoTTY sets out to serve.
type ProjectConfig struct {
	Provider string `json:"provider"`
	Zones    []Zone `json:"zones"`
}

func ProjectConfigPath(projectPath string) string {
	return filepath.Join(projectPath, ".isotty", "dns.yaml")
}

// LoadProjectConfig reports enabled=false when the project has no DNS config.
func LoadProjectConfig(projectPath string) (ProjectConfig, bool, error) {
	path := ProjectConfigPath(projectPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectConfig{}, false, nil
		}
		return ProjectConfig{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := ParseProjectConfig(data)
	if err != nil {
		return ProjectConfig{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, nil
}

func ParseProjectConfig(data []byte) (ProjectConfig, error) {
	var cfg ProjectConfig
	if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
		return ProjectConfig{}, err
	}
	if err := cfg.validate(); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

func SaveProjectConfig(projectPath string, cfg ProjectConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal dns config: %w", err)
	}
	path := ProjectConfigPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dns config directory: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// RemoveProjectConfig is what an empty list means: no config at all.
func RemoveProjectConfig(projectPath string) error {
	if err := os.Remove(ProjectConfigPath(projectPath)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", ProjectConfigPath(projectPath), err)
	}
	return nil
}

// ValidateProjectConfig checks a config assembled on the host before it is written.
func ValidateProjectConfig(cfg ProjectConfig) error {
	return cfg.validate()
}

func (c ProjectConfig) validate() error {
	if c.Provider != ProviderCloudDNS {
		return fmt.Errorf("provider %q is not supported; only %s is", c.Provider, ProviderCloudDNS)
	}
	if len(c.Zones) == 0 {
		return errors.New("zones must name at least one zone")
	}
	seen := map[string]bool{}
	for _, zone := range c.Zones {
		if err := zone.validate(); err != nil {
			return err
		}
		for _, name := range zone.Names {
			if seen[name] {
				return fmt.Errorf("name %s is listed twice", name)
			}
			seen[name] = true
		}
	}
	return nil
}

func (z Zone) validate() error {
	if !projectPattern.MatchString(z.Project) {
		return fmt.Errorf("invalid project %q", z.Project)
	}
	if !zonePattern.MatchString(z.ManagedZone) {
		return fmt.Errorf("invalid managed zone %q", z.ManagedZone)
	}
	if len(z.Names) == 0 {
		return fmt.Errorf("managed zone %s names no record name", z.ManagedZone)
	}
	for _, name := range z.Names {
		if err := ValidateName(name); err != nil {
			return err
		}
	}
	return nil
}

// ValidateName accepts a lower-case name without a trailing dot and
// without wildcards: a pinned name covers everything under it already.
func ValidateName(name string) error {
	if name == "" || name != strings.ToLower(name) || strings.HasSuffix(name, ".") {
		return fmt.Errorf("invalid name %q: write it in lower case without a trailing dot", name)
	}
	for _, label := range strings.Split(name, ".") {
		if label == "*" {
			return fmt.Errorf("invalid name %q: a pinned name covers the names under it, so no wildcard is needed", name)
		}
		if !labelPattern.MatchString(label) {
			return fmt.Errorf("invalid name %q", name)
		}
	}
	return nil
}

// Normalize is the name as the pin and the audit log spell it: lower case,
// no trailing dot.
func Normalize(name string) string {
	return strings.TrimSuffix(strings.ToLower(name), ".")
}

// Allows returns the zone whose agreement covers a name: a pinned name or
// one under it, wildcard labels included.
func (c ProjectConfig) Allows(name string) (Zone, bool) {
	name = Normalize(name)
	for _, zone := range c.Zones {
		for _, pinned := range zone.Names {
			if name == pinned || strings.HasSuffix(name, "."+pinned) {
				return zone, true
			}
		}
	}
	return Zone{}, false
}

// Put adds the names to the zone, creating it when the project has no
// agreement for it yet.
func (c *ProjectConfig) Put(zone Zone) {
	for i, existing := range c.Zones {
		if existing.Project == zone.Project && existing.ManagedZone == zone.ManagedZone {
			for _, name := range zone.Names {
				if !slices.Contains(existing.Names, name) {
					existing.Names = append(existing.Names, name)
				}
			}
			c.Zones[i] = existing
			return
		}
	}
	c.Zones = append(c.Zones, zone)
}

// Delete removes a pinned name; a zone left without names goes with it.
func (c *ProjectConfig) Delete(name string) bool {
	for i, zone := range c.Zones {
		at := slices.Index(zone.Names, name)
		if at < 0 {
			continue
		}
		zone.Names = slices.Delete(slices.Clone(zone.Names), at, at+1)
		if len(zone.Names) == 0 {
			c.Zones = slices.Delete(c.Zones, i, i+1)
		} else {
			c.Zones[i] = zone
		}
		return true
	}
	return false
}

func (c ProjectConfig) sorted() []Zone {
	zones := make([]Zone, len(c.Zones))
	for i, zone := range c.Zones {
		zone.Names = slices.Sorted(slices.Values(zone.Names))
		zones[i] = zone
	}
	slices.SortFunc(zones, func(a, b Zone) int {
		return strings.Compare(a.Project+"/"+a.ManagedZone, b.Project+"/"+b.ManagedZone)
	})
	return zones
}

func (c ProjectConfig) equal(other ProjectConfig) bool {
	return c.Provider == other.Provider && slices.EqualFunc(c.sorted(), other.sorted(), func(a, b Zone) bool {
		return a.Project == b.Project && a.ManagedZone == b.ManagedZone && slices.Equal(a.Names, b.Names)
	})
}

func (c ProjectConfig) summary() string {
	var parts []string
	for _, zone := range c.sorted() {
		parts = append(parts, fmt.Sprintf("%s/%s: %s", zone.Project, zone.ManagedZone, strings.Join(zone.Names, ",")))
	}
	return c.Provider + " " + strings.Join(parts, "; ")
}
