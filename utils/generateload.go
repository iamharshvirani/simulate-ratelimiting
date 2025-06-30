package utils

import (
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
