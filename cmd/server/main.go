package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yevgeniy-miretskiy/rollingstone/simulator"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for development
		return true
	},
}


// Client message types
type ClientMessage struct {
	Type   string               `json:"type"`
	Config *simulator.SimConfig `json:"config,omitempty"`
}

// Server message types
type ServerMessage struct {
	Type    string                 `json:"type"`
	Running *bool                  `json:"running,omitempty"`
	Config  *simulator.SimConfig   `json:"config,omitempty"`
	Metrics *simulator.Metrics     `json:"metrics,omitempty"`
	State   map[string]interface{} `json:"state,omitempty"`
	Error   *string                `json:"error,omitempty"` // Validation or runtime errors
	Log     *string                `json:"log,omitempty"`   // Event log message
}

// simState manages the simulation state and UI pacing
type simState struct {
	sim     *simulator.Simulator
	running bool
	paused  bool
	mu      sync.Mutex
	stopCh  chan struct{}
	logCh   chan string // Buffered channel for log events
}

func newSimState(config simulator.SimConfig) (*simState, error) {
	sim, err := simulator.NewSimulator(config)
	if err != nil {
		return nil, err
	}

	// Create log channel with reasonable buffer (don't block simulation)
	logCh := make(chan string, 100)

	// Set up log event callback
	sim.LogEvent = func(msg string) {
		select {
		case logCh <- msg:
			// Sent successfully
		default:
			// Buffer full, drop message (don't block simulation)
		}
	}

	return &simState{
		sim:     sim,
		running: false,
		paused:  false,
		stopCh:  make(chan struct{}),
		logCh:   logCh,
	}, nil
}

// start begins the simulation
func (s *simState) start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If we're starting from initial state (never started before),
	// we need to call Reset() to schedule events
	// Check if queue is empty to detect this
	if s.sim.IsQueueEmpty() {
		log.Println("First start detected - resetting to schedule events")
		if err := s.sim.Reset(); err != nil {
			log.Printf("Error resetting simulation: %v", err)
			return
		}
	}

	s.running = true
	s.paused = false
}

// pause pauses the simulation
func (s *simState) pause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = true
}

// reset resets the simulation
func (s *simState) reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.sim.Reset(); err != nil {
		return fmt.Errorf("failed to reset simulation: %w", err)
	}
	s.running = false
	s.paused = false
	return nil
}

// updateConfig updates the configuration
func (s *simState) updateConfig(config simulator.SimConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sim.UpdateConfig(config)
}

// isRunning returns true if simulation is running and not paused
func (s *simState) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running && !s.paused
}

// getConfig returns the current simulator configuration
func (s *simState) getConfig() simulator.SimConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sim.Config()
}

// step advances simulation by one step (called by UI ticker)
// Returns error message if simulation panicked or OOM killed
func (s *simState) step() (errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.paused {
		return ""
	}

	// Recover from panics to prevent server crash
	defer func() {
		if r := recover(); r != nil {
			s.running = false // Stop the simulation
			errMsg = fmt.Sprintf("Simulation panic: %v", r)
			log.Printf("⚠️  %s", errMsg)
		}
	}()

	// Check if already OOM killed before stepping
	if s.sim.Metrics().IsOOMKilled {
		s.running = false // Stop the simulation
		return "Simulation OOM killed"
	}

	s.sim.Step()

	// Check if OOM occurred during this step
	if s.sim.Metrics().IsOOMKilled {
		s.running = false // Stop the simulation
		log.Printf("⚠️  Simulation OOM killed at virtual time %.1f", s.sim.VirtualTime())
		return "Simulation OOM killed"
	}

	return ""
}

// metrics returns current metrics
func (s *simState) metrics() *simulator.Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sim.Metrics()
}

// state returns current state
func (s *simState) state() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sim.State()
}

// resetAggregateStats resets aggregate compaction stats after UI update
func (s *simState) resetAggregateStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sim.Metrics().ResetAggregateStats()
}

// stop signals the UI loop to stop
func (s *simState) stop() {
	close(s.stopCh)
}

// logForwardLoop forwards log events from the simulator to the WebSocket
// Batches log messages to reduce WebSocket overhead and UI lag
// This runs in its own goroutine
func logForwardLoop(conn *safeConn, state *simState) {
	ticker := time.NewTicker(200 * time.Millisecond) // Batch every 200ms
	defer ticker.Stop()

	batch := make([]string, 0, 50) // Pre-allocate for typical batch size

	for {
		select {
		case <-state.stopCh:
			// Send any remaining logs before exiting
			if len(batch) > 0 {
				sendLogBatch(conn, batch)
			}
			return

		case logMsg := <-state.logCh:
			batch = append(batch, logMsg)
			// If batch is getting large, send immediately to prevent memory buildup
			if len(batch) >= 100 {
				sendLogBatch(conn, batch)
				batch = batch[:0] // Reset slice, keep capacity
			}

		case <-ticker.C:
			// Periodically flush batch
			if len(batch) > 0 {
				sendLogBatch(conn, batch)
				batch = batch[:0] // Reset slice, keep capacity
			}
		}
	}
}

// sendLogBatch sends a batch of log messages as a single WebSocket message
func sendLogBatch(conn *safeConn, batch []string) {
	if len(batch) == 0 {
		return
	}

	// Join logs with newlines for display
	logText := ""
	for i, msg := range batch {
		if i > 0 {
			logText += "\n"
		}
		logText += msg
	}

	msg := ServerMessage{
		Type: "log",
		Log:  &logText,
	}
	if err := conn.WriteJSON(msg); err != nil {
		log.Printf("Error sending log batch: %v", err)
	}
}

// uiUpdateLoop periodically calls Step() and sends updates to the client
// This runs in its own goroutine and controls UI pacing
func uiUpdateLoop(conn *safeConn, state *simState) {
	ticker := time.NewTicker(500 * time.Millisecond) // 2 updates/sec (reduced to minimize memory churn)
	defer ticker.Stop()

	for {
		select {
		case <-state.stopCh:
			log.Println("UI update loop stopping")
			return

		case <-ticker.C:
			if state.isRunning() {
				// Advance simulation by one step
				// (Virtual time advanced determined by SimulationSpeedMultiplier)
				if errMsg := state.step(); errMsg != "" {
					// Simulation panicked or OOM killed - send error to UI and stop
					errorMsg := ServerMessage{
						Type:  "error",
						Error: &errMsg,
					}
					if err := conn.WriteJSON(errorMsg); err != nil {
						log.Printf("Error sending error message: %v", err)
					}

					// Send stopped status with final metrics/state
					running := false
					config := state.getConfig()
					metrics := state.metrics()
					lsmState := state.state()
					
					// Send final metrics update (includes OOM status)
					metricsMsg := ServerMessage{
						Type:    "metrics",
						Metrics: metrics,
					}
					if err := conn.WriteJSON(metricsMsg); err != nil {
						log.Printf("Error sending final metrics: %v", err)
					}

					// Send final state update
					stateMsg := ServerMessage{
						Type:  "state",
						State: lsmState,
					}
					if err := conn.WriteJSON(stateMsg); err != nil {
						log.Printf("Error sending final state: %v", err)
					}

					// Send stopped status
					statusMsg := ServerMessage{
						Type:    "status",
						Running: &running,
						Config:  &config,
					}
					if err := conn.WriteJSON(statusMsg); err != nil {
						log.Printf("Error sending stopped status: %v", err)
					}
					continue
				}

				// Send metrics update
				metrics := state.metrics()
				metricsMsg := ServerMessage{
					Type:    "metrics",
					Metrics: metrics,
				}
				if err := conn.WriteJSON(metricsMsg); err != nil {
					log.Printf("Error sending metrics: %v", err)
					return
				}

				// Send state update
				lsmState := state.state()
				stateMsg := ServerMessage{
					Type:  "state",
					State: lsmState,
				}
				if err := conn.WriteJSON(stateMsg); err != nil {
					log.Printf("Error sending state: %v", err)
					return
				}

				// Reset aggregate stats after UI update (for fast simulations)
				state.resetAggregateStats()
			}
		}
	}
}

// safeConn wraps a WebSocket connection with a mutex to prevent concurrent writes
type safeConn struct {
	*websocket.Conn
	writeMu sync.Mutex
}

func (sc *safeConn) WriteJSON(v interface{}) error {
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	return sc.Conn.WriteJSON(v)
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading connection: %v", err)
		return
	}
	defer conn.Close()

	// Wrap connection with mutex for safe concurrent writes
	safeConn := &safeConn{Conn: conn}

	log.Println("Client connected")

	// Create simulator state with default config
	// Config will be loaded from client's localStorage and sent via WebSocket
	config := simulator.DefaultConfig()
	state, err := newSimState(config)
	if err != nil {
		log.Printf("Error creating simulator: %v", err)
		return
	}

	// Send initial status
	running := false
	statusMsg := ServerMessage{
		Type:    "status",
		Running: &running,
		Config:  &config,
	}
	if err := safeConn.WriteJSON(statusMsg); err != nil {
		log.Printf("Error sending status: %v", err)
		return
	}

	// Start UI update loop
	go uiUpdateLoop(safeConn, state)

	// Start log forwarding loop
	go logForwardLoop(safeConn, state)

	// Handle messages from client
	for {
		var msg ClientMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			// Log all errors, not just unexpected close errors
			// JSON unmarshaling errors (e.g., invalid enum values) are not close errors
			// but they're still important to log
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Error reading message (unexpected close): %v", err)
			} else if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				// Normal close - don't log as error
				log.Printf("Client closed connection normally")
			} else {
				// JSON unmarshaling or other errors - these are important!
				log.Printf("Error reading/parsing WebSocket message: %v", err)
				log.Printf("This could be due to invalid JSON, type mismatches, or enum parsing errors")
			}
			break
		}

		log.Printf("Received command: %s", msg.Type)

		switch msg.Type {
		case "start":
			state.start()
			log.Println("Simulator started")
			running := true
			cfg := state.getConfig()
			statusMsg := ServerMessage{
				Type:    "status",
				Running: &running,
				Config:  &cfg,
			}
			safeConn.WriteJSON(statusMsg)

		case "pause":
			state.pause()
			log.Println("Simulator paused")
			running := false
			cfg := state.getConfig()
			statusMsg := ServerMessage{
				Type:    "status",
				Running: &running,
				Config:  &cfg,
			}
			safeConn.WriteJSON(statusMsg)

		case "reset":
			if err := state.reset(); err != nil {
				log.Printf("Error resetting simulator: %v", err)
			} else {
				log.Println("Simulator reset")
			}
			running := false
			cfg := state.getConfig()
			statusMsg := ServerMessage{
				Type:    "status",
				Running: &running,
				Config:  &cfg,
			}
			safeConn.WriteJSON(statusMsg)

		case "config_update":
			if msg.Config != nil {
				if err := state.updateConfig(*msg.Config); err != nil {
					log.Printf("Error updating config: %v", err)
					// Send error back to UI
					errStr := err.Error()
					errorMsg := ServerMessage{
						Type:  "error",
						Error: &errStr,
					}
					safeConn.WriteJSON(errorMsg)
				} else {
					log.Printf("Config updated: %+v", msg.Config)
					running := state.isRunning()
					updatedFullConfig := state.getConfig()
					statusMsg := ServerMessage{
						Type:    "status",
						Running: &running,
						Config:  &updatedFullConfig,
					}
					safeConn.WriteJSON(statusMsg)
				}
			}

		case "reset_config":
			// Reset config to defaults
			defaultConfig := simulator.DefaultConfig()
			if err := state.updateConfig(defaultConfig); err != nil {
				log.Printf("Error resetting config to defaults: %v", err)
				errStr := err.Error()
				errorMsg := ServerMessage{
					Type:  "error",
					Error: &errStr,
				}
				safeConn.WriteJSON(errorMsg)
			} else {
				log.Println("Config reset to defaults")
				running := state.isRunning()
				statusMsg := ServerMessage{
					Type:    "status",
					Running: &running,
					Config:  &defaultConfig,
				}
				safeConn.WriteJSON(statusMsg)
			}
		}
	}

	// Clean up
	state.stop()
	log.Println("Client disconnected")
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	// Serve the React app's index.html (for root and SPA routing)
	http.ServeFile(w, r, filepath.Join("web", "dist", "index.html"))
}

func quitHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("🛑 Shutdown requested via /quitquitquit")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Server shutting down...")

	go func() {
		time.Sleep(100 * time.Millisecond)
		log.Println("👋 Server stopped")
		os.Exit(0)
	}()
}

func main() {
	// Serve static files from web/dist (React build output)
	distDir := filepath.Join("web", "dist")
	if _, err := os.Stat(distDir); os.IsNotExist(err) {
		log.Fatalf("❌ Frontend not built! Run 'cd web && npm run build' first, or use ./start.sh")
	}

	// Create file server for static assets
	fileServer := http.FileServer(http.Dir(distDir))

	// Serve static files (favicon, assets, etc.)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// WebSocket endpoint
		if r.URL.Path == "/ws" {
			handleWebSocket(w, r)
			return
		}
		// Shutdown endpoint
		if r.URL.Path == "/quitquitquit" {
			quitHandler(w, r)
			return
		}
		// Static files (favicon, assets, etc.) - serve if file exists
		if r.URL.Path != "/" {
			filePath := filepath.Join(distDir, r.URL.Path)
			if _, err := os.Stat(filePath); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Default to index.html for root and non-existent paths (SPA routing)
		serveHome(w, r)
	})

	addr := ":8080"
	log.Printf("🚀 Server starting on http://localhost%s", addr)
	log.Printf("📁 Serving React app from: %s", distDir)
	log.Printf("📡 WebSocket endpoint: ws://localhost%s/ws", addr)
	log.Printf("🛑 Shutdown endpoint: http://localhost%s/quitquitquit", addr)
	log.Printf("🎨 Favicon: http://localhost%s/vite.svg", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
