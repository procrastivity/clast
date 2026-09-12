// Package config loads the tool config: $XDG_CONFIG_HOME/clast/config.yaml,
// resolved user-override -> shipped config.default.yaml, via the same
// resolution mechanism any other asset uses. This is TOOL config only —
// anything two clones of one project repo must agree on is project config
// and belongs to the tool's own store, never here.
package config

import (
	"fmt"

	"github.com/procrastivity/clast/internal/asset"
	"gopkg.in/yaml.v3"
)

// defaultAssetName is the shipped default's relative name. It deliberately
// differs from the user override's filename (config.yaml) so the two are
// never mistaken for the same file on disk (C5.2) — unlike a plain
// shadow-by-name asset, config is loaded from both and merged, not replaced.
const defaultAssetName = "config.default.yaml"

// overrideAssetName is the user override's relative name under
// $XDG_CONFIG_HOME/clast/.
const overrideAssetName = "config.yaml"

// Config is the merged tool config. No keys are owned by the chassis
// itself; verbs add their own as they earn tool-config scope.
type Config map[string]any

// Load reads the shipped default (share tree, else the embedded fallback)
// and merges the user override on top of it, override winning per key.
func Load() (Config, error) {
	def, err := asset.ReadDefault(defaultAssetName)
	if err != nil {
		return nil, fmt.Errorf("config: loading shipped default: %w", err)
	}

	merged := Config{}
	if err := yaml.Unmarshal(def.Bytes(), &merged); err != nil {
		return nil, fmt.Errorf("config: parsing shipped default: %w", err)
	}

	override, present, err := asset.ReadOverride(overrideAssetName)
	if err != nil {
		return nil, fmt.Errorf("config: locating user override: %w", err)
	}
	if !present {
		return merged, nil
	}

	overrideValues := Config{}
	if err := yaml.Unmarshal(override.Bytes(), &overrideValues); err != nil {
		return nil, fmt.Errorf("config: parsing user override: %w", err)
	}
	for k, v := range overrideValues {
		merged[k] = v
	}

	return merged, nil
}
