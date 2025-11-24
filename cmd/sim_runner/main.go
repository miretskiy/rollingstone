package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/yevgeniy-miretskiy/rollingstone/simulator"
)

func main() {
	// Parse command line flags
	configFile := flag.String("config", "", "Path to JSON configuration file")
	durationSec := flag.Int("duration", 3600, "Simulation duration in virtual seconds")
	outputFile := flag.String("output", "", "Path to output JSON file (optional, prints to stdout if not specified)")
	speedMultiplier := flag.Int("speed", 100, "Simulation speed multiplier (each Step simulates N seconds)")
	verbose := flag.Bool("verbose", false, "Enable verbose logging from simulator")
	flag.Parse()

	if *configFile == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -config <config.json> [-duration <seconds>] [-output <output.json>] [-speed <multiplier>] [-verbose]\n", os.Args[0])
		os.Exit(1)
	}

	// Read configuration from file
	configData, err := os.ReadFile(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
		os.Exit(1)
	}

	var config simulator.SimConfig
	if err := json.Unmarshal(configData, &config); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config JSON: %v\n", err)
		os.Exit(1)
	}

	// Override SimulationSpeedMultiplier if specified via flag
	if *speedMultiplier > 0 {
		config.SimulationSpeedMultiplier = *speedMultiplier
		fmt.Fprintf(os.Stderr, "Using speed multiplier: %d (each Step simulates %d seconds)\n", *speedMultiplier, *speedMultiplier)
	} else if config.SimulationSpeedMultiplier == 0 {
		config.SimulationSpeedMultiplier = 100 // Default to 100x if not set
		fmt.Fprintf(os.Stderr, "Using default speed multiplier: 100 (each Step simulates 100 seconds)\n")
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// Create simulator
	sim, err := simulator.NewSimulator(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating simulator: %v\n", err)
		os.Exit(1)
	}

	// Set up LogEvent callback to capture simulator logs
	if *verbose {
		sim.LogEvent = func(msg string) {
			fmt.Fprintf(os.Stderr, "[SIM] %s\n", msg)
		}
		fmt.Fprintf(os.Stderr, "Verbose logging enabled\n")
	}

	// Reset to initialize events
	if err := sim.Reset(); err != nil {
		fmt.Fprintf(os.Stderr, "Error resetting simulator: %v\n", err)
		os.Exit(1)
	}

	// Run simulation
	fmt.Fprintf(os.Stderr, "Starting simulation for %d virtual seconds...\n", *durationSec)
	startTime := time.Now()

	targetTime := float64(*durationSec)
	for sim.VirtualTime() < targetTime && !sim.IsQueueEmpty() {
		sim.Step()
	}

	elapsed := time.Since(startTime)
	fmt.Fprintf(os.Stderr, "Simulation completed in %v (%.1f virtual seconds)\n", elapsed, sim.VirtualTime())

	// Gather results
	metrics := sim.Metrics()
	lsmState := sim.State()

	results := map[string]interface{}{
		"config":      config,
		"virtualTime": sim.VirtualTime(),
		"realTime":    elapsed.Seconds(),
		"metrics":     metrics,
		"state":       lsmState,
	}

	// Output results
	output, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling results: %v\n", err)
		os.Exit(1)
	}

	if *outputFile != "" {
		if err := os.WriteFile(*outputFile, output, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Results written to %s\n", *outputFile)
	} else {
		fmt.Println(string(output))
	}
}
