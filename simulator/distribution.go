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
	default:
		return DistGeometric, fmt.Errorf("invalid DistributionType: %s (must be 'uniform', 'exponential', or 'geometric')", s)
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

// NewDistribution creates a distribution based on type
func NewDistribution(distType DistributionType) Distribution {
	switch distType {
	case DistUniform:
		return &UniformDistribution{}
	case DistExponential:
		return &ExponentialDistribution{Lambda: 0.5}
	case DistGeometric:
		return &GeometricDistribution{P: 0.3}
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
	return &distributionAdapter{
		dist: NewDistribution(distType),
		rng:  rand.New(rand.NewSource(rand.Int63())), // Could be injected for testing
	}
}
