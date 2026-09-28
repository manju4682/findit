// Package config centralizes FindIt's tunable defaults.
//
// Only genuinely adjustable knobs live here (heuristic weights, buffer sizes,
// thumbnail dimensions, poll intervals). Values fixed by on-disk formats —
// partition-table offsets, filesystem magic bytes, sector layout — remain as
// consts in the packages that use them, since changing those would be incorrect
// rather than a tuning decision.
//
// The values are sourced from an embedded YAML template so the shipped binary
// stays self-contained; there is no external file to install or lose.
package config

import (
	_ "embed"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed templates/configmap.yml
var defaultsYAML []byte

// Diagnosis holds the diagnosis engine's scoring and scanning knobs.
type Diagnosis struct {
	ScoreBootSector  int `yaml:"score_boot_sector"`
	ScoreTypeString  int `yaml:"score_type_string"`
	ScoreMFTRecord   int `yaml:"score_mft_record"`
	ScoreMediaBonus  int `yaml:"score_media_bonus"`
	ConfHighCutoff   int `yaml:"conf_high_cutoff"`
	ConfMediumCutoff int `yaml:"conf_medium_cutoff"`
	ScanOverlapBytes int `yaml:"scan_overlap_bytes"`
}

// Scan holds knobs for a running recovery scan.
type Scan struct {
	EventBufferSize    int `yaml:"event_buffer_size"`
	RawContentComplete int `yaml:"raw_content_complete"`
	RawContentFragment int `yaml:"raw_content_fragment"`
}

// Preview holds thumbnail-generation knobs.
type Preview struct {
	MaxDimension int `yaml:"max_dimension"`
}

// Carve holds raw file-carving knobs.
type Carve struct {
	MaxVerifyBytes int64 `yaml:"max_verify_bytes"`
}

// Clone holds drive-clone knobs.
type Clone struct {
	PollIntervalMS int `yaml:"poll_interval_ms"`
}

// PollInterval returns the clone progress sampling interval as a Duration.
func (c Clone) PollInterval() time.Duration {
	return time.Duration(c.PollIntervalMS) * time.Millisecond
}

// Config is the full set of tunable defaults.
type Config struct {
	Diagnosis Diagnosis `yaml:"diagnosis"`
	Scan      Scan      `yaml:"scan"`
	Preview   Preview   `yaml:"preview"`
	Carve     Carve     `yaml:"carve"`
	Clone     Clone     `yaml:"clone"`
}

var (
	once   sync.Once
	loaded Config
)

// Get returns the tunable configuration, parsed once from the embedded template.
// If the template ever fails to parse, the built-in fallback values are used, so
// callers can always rely on a usable config.
func Get() Config {
	once.Do(func() {
		loaded = fallback()
		_ = yaml.Unmarshal(defaultsYAML, &loaded)
	})
	return loaded
}

// fallback mirrors templates/configmap.yml so the package remains usable even if
// the embedded document can't be parsed.
func fallback() Config {
	return Config{
		Diagnosis: Diagnosis{
			ScoreBootSector:  40,
			ScoreTypeString:  25,
			ScoreMFTRecord:   20,
			ScoreMediaBonus:  15,
			ConfHighCutoff:   75,
			ConfMediumCutoff: 40,
			ScanOverlapBytes: 8192,
		},
		Scan: Scan{
			EventBufferSize:    128,
			RawContentComplete: 100,
			RawContentFragment: 40,
		},
		Preview: Preview{MaxDimension: 256},
		Carve:   Carve{MaxVerifyBytes: 64 << 20},
		Clone:   Clone{PollIntervalMS: 500},
	}
}
