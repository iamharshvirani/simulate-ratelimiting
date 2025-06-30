package utils

import (
	"fmt"
	"math"
	"math/rand/v2"
)

type GenerateLoadConfig struct {
	Pattern        string
	Baseline       float64
	JitterPercent  int
	PeakSecond     int
	PeakMultiplier float64

	// Sine wave parameters
	Amplitude float64 // amplitude of sine wave
	Period    float64 // period of sine wave in seconds

	// Step pattern parameters
	StepDuration int
}

// Predefined load configurations
var predefinedConfigs = map[string]GenerateLoadConfig{
	"sine_default": {
		Pattern:        "sine",
		Baseline:       630.0,
		Amplitude:      150.0,
		Period:         60.0,
		JitterPercent:  7,
		PeakSecond:     90,
		PeakMultiplier: 1.1,
	},
	"sine_low": {
		Pattern:        "sine",
		Baseline:       200.0,
		Amplitude:      60.0,
		Period:         90.0,
		JitterPercent:  20,
		PeakSecond:     55,
		PeakMultiplier: 2.5,
	},
	"sine_high_above_capacity": {
		Pattern:        "sine",
		Baseline:       800.0,
		Amplitude:      100.0,
		Period:         60.0,
		JitterPercent:  7,
		PeakSecond:     90,
		PeakMultiplier: 1.1,
	},
	"step_default": {
		Pattern:       "step",
		Baseline:      240.0,
		Amplitude:     5.0,
		StepDuration:  10,
		JitterPercent: 20,
	},
	"constant": {
		Pattern:       "constant",
		Baseline:      300.0,
		JitterPercent: 5,
	},
	"constant_high_above_capacity": {
		Pattern:       "constant",
		Baseline:      800.0,
		JitterPercent: 5,
	},
}

// GetLoadConfig returns a predefined load configuration by name.
// If the name doesn't exist, it returns an error.
func GetLoadConfig(name string) (GenerateLoadConfig, error) {
	config, exists := predefinedConfigs[name]
	if !exists {
		return GenerateLoadConfig{}, fmt.Errorf("load configuration '%s' not found", name)
	}
	return config, nil
}

// ListAvailableConfigs returns a list of all available configuration names
func ListAvailableConfigs() []string {
	var names []string
	for name := range predefinedConfigs {
		names = append(names, name)
	}
	return names
}

func GenerateLoad(second int, loadConfig GenerateLoadConfig) int {
	// baseLoad := loadConfig.baseline + loadConfig.amplitude*math.Sin(2*math.Pi*float64(second)/loadConfig.period)
	// jitter := (rand.Float64() - 0.5) * (float64(loadConfig.jitterPercent) / 100.0) * baseLoad
	// loadWithJitter := baseLoad + jitter
	// if second == loadConfig.peakSecond {
	// 	loadWithJitter *= loadConfig.peakMultiplier
	// }
	// return int(math.Max(loadWithJitter, 0))

	var baseLoad float64

	switch loadConfig.Pattern {
	case "sine":
		// Sine wave pattern
		baseLoad = loadConfig.Baseline + loadConfig.Amplitude*math.Sin(2*math.Pi*float64(second)/loadConfig.Period)
	case "step":
		// Step pattern - alternates between high and low load
		if (second/loadConfig.StepDuration)%2 == 0 {
			baseLoad = loadConfig.Baseline + loadConfig.Amplitude
		} else {
			baseLoad = loadConfig.Baseline - loadConfig.Amplitude
		}
	case "constant":
		// Constant load with optional peak
		baseLoad = loadConfig.Baseline
	default:
		// Default to sine wave if pattern is not recognized
		baseLoad = loadConfig.Baseline + loadConfig.Amplitude*math.Sin(2*math.Pi*float64(second)/loadConfig.Period)
	}

	// Apply jitter if enabled
	jitter := 0.0
	if loadConfig.JitterPercent > 0 {
		jitter = (rand.Float64() - 0.5) * (float64(loadConfig.JitterPercent) / 100.0) * baseLoad
	}

	// Apply peak multiplier if this is the peak second
	if second == loadConfig.PeakSecond {
		baseLoad *= loadConfig.PeakMultiplier
	}

	// Calculate final load with jitter and ensure it's not negative
	loadWithJitter := baseLoad + jitter
	return int(math.Max(loadWithJitter, 0))
}
