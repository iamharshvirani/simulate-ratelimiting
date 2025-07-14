package traffic

import (
	"errors"
	"fmt"
	"math"
)

// CameraTrafficConfig holds the knobs that describe how cameras generate events.
//
//	TotalCameras      – total number of simulated cameras (e.g. 250)
//	NumGroups         – how many distinct rate groups to create (fixed at 10 in the requirement but kept configurable).
//	MinRate / MaxRate – base-rates (events/second) assigned to the slowest and
//	                    fastest groups respectively. Groups in-between are spaced
//	                    linearly.
//	DynamicMultiplier – if true, every base-rate is multiplied by a global
//	                    sine-wave factor oscillating between 0.5× and 2×.
//	                    When false, multiplier is always 1.
//	PeriodSec         – length of one full sine cycle in seconds when
//	                    DynamicMultiplier is on.
//	ExemptGroup       – index of the group (0-based) that should bypass rate
//	                    limiting. Use -1 when no group is exempt.
//
// All values must be non-negative; BuildGroups will validate them.
type CameraTrafficConfig struct {
	TotalCameras      int
	NumGroups         int
	MinRate           float64
	MaxRate           float64
	DynamicMultiplier bool
	PeriodSec         int
	ExemptGroup       int
}

// CameraGroup represents one rate bucket.
// BaseRate is the per-camera rate before applying the global multiplier.
// RateLimited marks whether requests from this group should go through the
// rate-limiter.

type CameraGroup struct {
	ID          int
	CameraIDs   []string
	BaseRate    float64
	RateLimited bool
}

// BuildGroups splits the TotalCameras into NumGroups groups, assigns base-rates
// linearly spaced between MinRate and MaxRate, and returns the resulting slice.
//
// Example: TotalCameras=250, NumGroups=10 → each group receives 25 cameras.
// Groups[0].BaseRate == MinRate, Groups[NumGroups-1].BaseRate == MaxRate.
func BuildGroups(cfg CameraTrafficConfig) ([]CameraGroup, error) {
	if cfg.TotalCameras <= 0 {
		return nil, errors.New("TotalCameras must be > 0")
	}
	if cfg.NumGroups <= 0 {
		return nil, errors.New("NumGroups must be > 0")
	}
	if cfg.MinRate <= 0 || cfg.MaxRate <= 0 {
		return nil, errors.New("rates must be positive")
	}
	if cfg.MinRate > cfg.MaxRate {
		return nil, fmt.Errorf("MinRate %.2f cannot exceed MaxRate %.2f", cfg.MinRate, cfg.MaxRate)
	}
	if cfg.ExemptGroup >= cfg.NumGroups {
		return nil, fmt.Errorf("ExemptGroup %d out of range (0-%d)", cfg.ExemptGroup, cfg.NumGroups-1)
	}
	if cfg.PeriodSec <= 0 {
		cfg.PeriodSec = 60 // sensible default
	}

	camsPerGroup := cfg.TotalCameras / cfg.NumGroups
	remainder := cfg.TotalCameras % cfg.NumGroups

	groups := make([]CameraGroup, cfg.NumGroups)
	for g := 0; g < cfg.NumGroups; g++ {
		size := camsPerGroup
		if g < remainder { // distribute remainder evenly
			size++
		}
		ids := make([]string, size)
		for i := 0; i < size; i++ {
			globalIdx := g*camsPerGroup + i
			if g < remainder {
				globalIdx += g // shift for previously added cameras
			} else {
				globalIdx += remainder
			}
			ids[i] = fmt.Sprintf("cam_%d", globalIdx)
		}

		// linear interpolation of rates across groups
		var base float64
		if cfg.NumGroups == 1 {
			base = cfg.MinRate
		} else {
			frac := float64(g) / float64(cfg.NumGroups-1)
			base = cfg.MinRate + frac*(cfg.MaxRate-cfg.MinRate)
		}

		groups[g] = CameraGroup{
			ID:          g,
			CameraIDs:   ids,
			BaseRate:    base,
			RateLimited: g != cfg.ExemptGroup,
		}
	}
	return groups, nil
}

// Multiplier returns the global dynamic factor for second t according to cfg.
// If DynamicMultiplier is false, the function always returns 1.
// Otherwise it returns a sine oscillating smoothly between 0.5 and 2.0.
//
//	factor(t) = 1.25 + 0.75 * sin(2π * t / Period)
//	⇒ min 0.5, max 2.0
func Multiplier(t int, cfg CameraTrafficConfig) float64 {
	if !cfg.DynamicMultiplier {
		return 1.0
	}
	theta := 2 * math.Pi * float64(t) / float64(cfg.PeriodSec)
	return 1.25 + 0.75*math.Sin(theta)
}
