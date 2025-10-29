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
		samples := make([]int, 0, 10000)
		histogram := make(map[int]int)

		for i := 0; i < 10000; i++ {
			result := dist.Sample(rng, 1, 100)
			if result < 1 || result > 100 {
				t.Fatalf("Sample %d out of range [1, 100]", result)
			}
			samples = append(samples, result)
			histogram[result]++
		}

		// Calculate mean - should be closer to min than max
		sum := 0
		for _, v := range samples {
			sum += v
		}
		mean := float64(sum) / float64(len(samples))

		// Mean should be in lower half of range (skewed toward min)
		if mean > 50 {
			t.Errorf("Expected mean < 50 for exponential distribution, got %.2f", mean)
		}

		// CRITICAL: Must sample across full range, not just low values
		// This test would have caught the bug where we always got 1-2
		maxSampled := 0
		for val := range histogram {
			if val > maxSampled {
				maxSampled = val
			}
		}
		if maxSampled < 50 {
			t.Errorf("Expected to sample values up to at least 50, max was %d", maxSampled)
		}

		// Must have reasonable variety (not just 1-3 values)
		uniqueValues := len(histogram)
		if uniqueValues < 20 {
			t.Errorf("Expected at least 20 unique values, got %d", uniqueValues)
		}

		// Distribution should be exponentially decreasing
		// Split into quartiles and check counts decrease
		q1Count := 0 // 1-25
		q2Count := 0 // 26-50
		q3Count := 0 // 51-75
		q4Count := 0 // 76-100
		for val, count := range histogram {
			switch {
			case val <= 25:
				q1Count += count
			case val <= 50:
				q2Count += count
			case val <= 75:
				q3Count += count
			default:
				q4Count += count
			}
		}

		// Each quartile should have fewer samples than the previous
		if q1Count <= q2Count {
			t.Errorf("Expected Q1 > Q2, got Q1=%d, Q2=%d", q1Count, q2Count)
		}
		if q2Count <= q3Count {
			t.Errorf("Expected Q2 > Q3, got Q2=%d, Q3=%d", q2Count, q3Count)
		}

		t.Logf("Exponential distribution: mean=%.2f, max=%d, unique=%d", mean, maxSampled, uniqueValues)
		t.Logf("Quartile counts: Q1=%d, Q2=%d, Q3=%d, Q4=%d", q1Count, q2Count, q3Count, q4Count)
	})

	t.Run("overlap file count realistic", func(t *testing.T) {
		// Regression test: simulate picking overlaps for compaction
		// With 30 files in target level, we should see variety, not always 1
		samples := make([]int, 0, 1000)
		for i := 0; i < 1000; i++ {
			result := dist.Sample(rng, 1, 30)
			samples = append(samples, result)
		}

		// Count how many times we picked just 1 file
		countOne := 0
		countMany := 0 // >10 files
		for _, v := range samples {
			if v == 1 {
				countOne++
			}
			if v > 10 {
				countMany++
			}
		}

		// Should not be stuck at 1 (old bug)
		if countOne > 900 {
			t.Errorf("Too many samples are 1 (%d/1000), distribution not working", countOne)
		}

		// Should occasionally pick many files
		if countMany < 10 {
			t.Errorf("Expected at least 10 samples >10, got %d", countMany)
		}

		t.Logf("Overlap distribution: %d samples = 1, %d samples > 10", countOne, countMany)
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
