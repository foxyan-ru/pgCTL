// Package config defines the on-disk cluster description pgctl operates on.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Cluster describes a single local Postgres instance (or pair, during an
// upgrade) that pgctl knows how to create, configure, scale and decommission
// via Docker Compose. It intentionally mirrors the shape of a Kubernetes
// PostgresCluster CRD spec so the same struct could later back a real
// operator without a rewrite.
type Cluster struct {
	Name    string  `yaml:"name"`
	Image   string  `yaml:"image"`
	Port    int     `yaml:"port"`
	Replicas int    `yaml:"replicas"`
	Storage string  `yaml:"storage"` // e.g. "5GiB" — informational for the local demo
	Upgrade *Upgrade `yaml:"upgrade,omitempty"`
}

// Upgrade holds the settings for a major-version upgrade validation run:
// the source (current) cluster and the candidate (target) cluster to replay
// captured traffic against before cutover.
type Upgrade struct {
	TargetImage string `yaml:"targetImage"`
	TargetPort  int    `yaml:"targetPort"`
	// FailThreshold is the fractional latency regression (e.g. 0.20 = 20%)
	// at which `pgctl upgrade compare` reports a failure. See docs/adr/0002
	// for why this is a naive threshold rather than a statistical test.
	FailThreshold float64 `yaml:"failThreshold"`
}

// Load reads and validates a cluster definition from a YAML file.
func Load(path string) (*Cluster, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var c Cluster
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the cluster definition back to disk, e.g. after `pgctl configure`
// or `pgctl scale` mutate it in place.
func (c *Cluster) Save(path string) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	return os.WriteFile(path, b, 0o644)
}

// Validate applies sane defaults and rejects configs that would produce a
// broken Compose file.
func (c *Cluster) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Image == "" {
		c.Image = "postgres:16"
	}
	if c.Port == 0 {
		c.Port = 5432
	}
	if c.Replicas < 0 {
		return fmt.Errorf("replicas cannot be negative")
	}
	if c.Upgrade != nil && c.Upgrade.FailThreshold == 0 {
		c.Upgrade.FailThreshold = 0.20
	}
	return nil
}
