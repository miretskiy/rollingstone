package simulator

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
)

// DistributionType represents different probability distributions
type DistributionType int

const (
	DistUniform DistributionType = iota
	DistExponential
	DistGeometric
	DistFixed
)

// String returns the string representation of DistributionType
func (dt DistributionType) String() string {
	switch dt {
	case DistUniform:
		return "uniform"
	case DistExponential:
		return "exponential"
	case DistGeometric:
		return "geometric"
	case DistFixed:
		return "fixed"
	default:
		return fmt.Sprintf("unknown(%d)", int(dt))
	}
}

// ParseDistributionType parses a string into a DistributionType
func ParseDistributionType(s string) (DistributionType, error) {
	switch s {
	case "uniform":
		return DistUniform, nil
	case "exponential":
		return DistExponential, nil
	case "geometric":
		return DistGeometric, nil
	case "fixed":
		return DistFixed, nil
	default:
		return DistGeometric, fmt.Errorf("invalid DistributionType: %s (must be 'uniform', 'exponential', 'geometric', or 'fixed')", s)
	}
}

// MarshalJSON implements json.Marshaler for DistributionType
func (dt DistributionType) MarshalJSON() ([]byte, error) {
	return json.Marshal(dt.String())
}

// UnmarshalJSON implements json.Unmarshaler for DistributionType
func (dt *DistributionType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseDistributionType(s)
	if err != nil {
		return err
	}
	*dt = parsed
	return nil
}

// Distribution interface for generating random values
type Distribution interface {
	Sample(rng *rand.Rand, min, max int) int
}

// UniformDistribution samples uniformly between min and max
type UniformDistribution struct{}

func (d *UniformDistribution) Sample(rng *rand.Rand, min, max int) int {
	if min >= max {
		return min
	}
	return min + rng.Intn(max-min+1)
}

// ExponentialDistribution samples with exponential bias toward min
type ExponentialDistribution struct {
	Lambda float64 // Rate parameter (higher = more skewed toward min)
}

func (d *ExponentialDistribution) Sample(rng *rand.Rand, min, max int) int {
	if min >= max {
		return min
	}

	// Generate exponential random variable using inverse transform sampling
	// Standard formula: X = -ln(U) / lambda
	u := rng.Float64()
	if u == 0 {
		u = 1e-10 // Avoid log(0)
	}
	x := -math.Log(u) / d.Lambda

	// Normalize to [0, 1] by clamping at a reasonable upper bound
	// For lambda=0.5, 95% of values are < 6, so use that as max
	maxVal := 6.0 / d.Lambda
	normalized := x / maxVal
	if normalized > 1.0 {
		normalized = 1.0
	}

	// Scale to [min, max] range
	range_ := float64(max - min)
	scaled := normalized * range_

	return min + int(scaled)
}

// GeometricDistribution samples with geometric distribution
type GeometricDistribution struct {
	P float64 // Success probability (higher = more skewed toward min)
}

func (d *GeometricDistribution) Sample(rng *rand.Rand, min, max int) int {
	if min >= max {
		return min
	}

	// Generate geometric random variable (number of trials until success)
	u := rng.Float64()
	if u == 0 {
		u = 1e-10 // Avoid log(0)
	}
	if u >= 1.0 {
		u = 0.999999 // Avoid log(1-u) = log(0)
	}

	// Geometric distribution: k = floor(log(u) / log(1-p))
	// Using floor(log(1-u) / log(1-p)) to get number of failures before first success
	trials := 0
	if d.P > 0 && d.P < 1 {
		trials = int(math.Log(1-u) / math.Log(1-d.P))
		if trials < 0 {
			trials = 0
		}
	}

	// Scale to range
	range_ := max - min
	if trials > range_ {
		trials = range_
	}

	return min + trials
}

// FixedDistribution samples a fixed percentage of the range
type FixedDistribution struct {
	Percentage float64 // Percentage of range to use (0.0 to 1.0)
}

func (d *FixedDistribution) Sample(rng *rand.Rand, min, max int) int {
	if min >= max {
		return min
	}

	// Clamp percentage to [0.0, 1.0]
	percentage := d.Percentage
	if percentage < 0.0 {
		percentage = 0.0
	}
	if percentage > 1.0 {
		percentage = 1.0
	}

	// Handle extremes explicitly
	if percentage == 0.0 {
		// For 0.0, return 0 (no overlaps, trivial moves only)
		// Note: This allows returning 0 even though min might be 1
		// The caller (pickOverlapCount) will handle this correctly
		return 0
	}
	if percentage == 1.0 {
		return max // All overlaps
	}

	// Calculate fixed position in range
	// For percentage p, we want: min + p * (max - min)
	// This gives us a value between min and max
	range_ := float64(max - min)
	offset := percentage * range_
	result := min + int(offset)

	// Ensure result is within bounds (handle floating point precision)
	if result < min {
		return min
	}
	if result > max {
		return max
	}

	return result
}

// NewDistribution creates a distribution based on type
func NewDistribution(distType DistributionType) Distribution {
	switch distType {
	case DistUniform:
		return &UniformDistribution{}
	case DistExponential:
		return &ExponentialDistribution{Lambda: 0.5}
	case DistGeometric:
		return &GeometricDistribution{P: 0.3}
	case DistFixed:
		return &FixedDistribution{Percentage: 0.5} // Default 50%
	default:
		return &UniformDistribution{}
	}
}

// filePicker interface for selecting files (internal to compactor)
type filePicker interface {
	Pick(min, max int) int
}

// Adapter to use distribution.go with existing code
type distributionAdapter struct {
	dist Distribution
	rng  *rand.Rand
}

func (da *distributionAdapter) Pick(min, max int) int {
	return da.dist.Sample(da.rng, min, max)
}

func newDistributionAdapter(distType DistributionType) filePicker {
	// NOTE: This function is deprecated - use newDistributionAdapterWithSeed instead
	// This creates a random source without a seed, breaking reproducibility
	// Kept for backward compatibility but should not be used
	return &distributionAdapter{
		dist: NewDistribution(distType),
		rng:  rand.New(rand.NewSource(rand.Int63())), // BUG: Not using seed - breaks reproducibility
	}
}

// newDistributionAdapterWithSeed creates a distribution adapter with a specific seed
func newDistributionAdapterWithSeed(distType DistributionType, seed int64) filePicker {
	var rng *rand.Rand
	if seed == 0 {
		rng = rand.New(rand.NewSource(rand.Int63())) // Use random seed if 0
	} else {
		rng = rand.New(rand.NewSource(seed))
	}
	return &distributionAdapter{
		dist: NewDistribution(distType),
		rng:  rng,
	}
}
