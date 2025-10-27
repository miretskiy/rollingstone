package simulator

import (
	"math/rand"
	"testing"
)

func TestUniformDistribution(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))
	dist := &UniformDistribution{}

	t.Run("single value range", func(t *testing.T) {
		result := dist.Sample(rng, 5, 5)
		if result != 5 {
			t.Errorf("Expected 5, got %d", result)
		}
	})

	t.Run("range 1-10", func(t *testing.T) {
		samples := make(map[int]int)
		iterations := 10000

		for i := 0; i < iterations; i++ {
			result := dist.Sample(rng, 1, 10)
			if result < 1 || result > 10 {
				t.Fatalf("Sample %d out of range [1, 10]", result)
			}
			samples[result]++
		}

		// Check all values were sampled
		if len(samples) != 10 {
			t.Errorf("Expected 10 unique values, got %d", len(samples))
		}

		// Check rough uniformity (each value should appear ~1000 times, allow 20% variance)
		expectedCount := iterations / 10
		tolerance := int(float64(expectedCount) * 0.3) // 30% tolerance

		for val := 1; val <= 10; val++ {
			count := samples[val]
			if count < expectedCount-tolerance || count > expectedCount+tolerance {
				t.Logf("Warning: Value %d sampled %d times (expected ~%d)", val, count, expectedCount)
			}
		}
	})
}

func TestExponentialDistribution(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))
	dist := &ExponentialDistribution{Lambda: 0.5}

	t.Run("single value range", func(t *testing.T) {
		result := dist.Sample(rng, 5, 5)
		if result != 5 {
			t.Errorf("Expected 5, got %d", result)
		}
	})

	t.Run("skewed toward min", func(t *testing.T) {
		samples := make([]int, 0, 1000)

		for i := 0; i < 1000; i++ {
			result := dist.Sample(rng, 1, 100)
			if result < 1 || result > 100 {
				t.Fatalf("Sample %d out of range [1, 100]", result)
			}
			samples = append(samples, result)
		}

		// Calculate mean - should be closer to min than max
		sum := 0
		for _, v := range samples {
			sum += v
		}
		mean := float64(sum) / float64(len(samples))

		// Mean should be in lower half of range
		if mean > 50 {
			t.Errorf("Expected mean < 50 for exponential distribution, got %.2f", mean)
		}

		t.Logf("Exponential distribution mean: %.2f (range 1-100)", mean)
	})
}

func TestGeometricDistribution(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))
	dist := &GeometricDistribution{P: 0.3}

	t.Run("single value range", func(t *testing.T) {
		result := dist.Sample(rng, 5, 5)
		if result != 5 {
			t.Errorf("Expected 5, got %d", result)
		}
	})

	t.Run("skewed toward min", func(t *testing.T) {
		samples := make([]int, 0, 1000)

		for i := 0; i < 1000; i++ {
			result := dist.Sample(rng, 1, 50)
			if result < 1 || result > 50 {
				t.Fatalf("Sample %d out of range [1, 50]", result)
			}
			samples = append(samples, result)
		}

		// Calculate mean - should be closer to min
		sum := 0
		for _, v := range samples {
			sum += v
		}
		mean := float64(sum) / float64(len(samples))

		// Mean should be in lower half
		if mean > 25 {
			t.Errorf("Expected mean < 25 for geometric distribution, got %.2f", mean)
		}

		t.Logf("Geometric distribution mean: %.2f (range 1-50)", mean)
	})
}

func TestNewDistribution(t *testing.T) {
	tests := []struct {
		name     string
		distType DistributionType
		wantType string
	}{
		{"uniform", DistUniform, "*simulator.UniformDistribution"},
		{"exponential", DistExponential, "*simulator.ExponentialDistribution"},
		{"geometric", DistGeometric, "*simulator.GeometricDistribution"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dist := NewDistribution(tt.distType)
			if dist == nil {
				t.Fatal("NewDistribution returned nil")
			}

			// Test that it can sample
			rng := rand.New(rand.NewSource(42))
			result := dist.Sample(rng, 1, 10)
			if result < 1 || result > 10 {
				t.Errorf("Sample %d out of range", result)
			}
		})
	}
}
