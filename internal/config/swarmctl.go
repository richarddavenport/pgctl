package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// readSwarmctlEnvironments extracts the environments from a swarmctl config.
//
// This reads a deliberately small, stable subset of a schema pgctl does not
// own — name, domain, guarded, ssh, secrets — and ignores everything else, so
// that swarmctl growing a key cannot stop pgctl starting. The alternative,
// importing swarmctl's config package, would tie a database tool's release to
// a deploy tool's. See design/decisions.md #8.
func readSwarmctlEnvironments(path string) ([]Environment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc struct {
		Environments []struct {
			Name    string `yaml:"name"`
			Domain  string `yaml:"domain"`
			Guarded bool   `yaml:"guarded"`
			SSH     struct {
				Host string `yaml:"host"`
				Port int    `yaml:"port"`
			} `yaml:"ssh"`
			Secrets struct {
				File string `yaml:"file"`
			} `yaml:"secrets"`
		} `yaml:"environments"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse swarmctl config %s: %w", path, err)
	}

	envs := make([]Environment, 0, len(doc.Environments))
	for _, e := range doc.Environments {
		if e.Name == "" {
			continue
		}
		envs = append(envs, Environment{
			Name:    e.Name,
			Domain:  e.Domain,
			Guarded: e.Guarded,
			SSH:     SSH{Host: e.SSH.Host, Port: e.SSH.Port},
			Secrets: Secrets{File: e.Secrets.File},
		})
	}
	return envs, nil
}
