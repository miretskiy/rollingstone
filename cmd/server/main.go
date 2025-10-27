package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yevgeniy-miretskiy/rollingstone/simulator"
)

var indexTemplate *template.Template

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
}

// simState manages the simulation state and UI pacing
type simState struct {
	sim     *simulator.Simulator
	running bool
	paused  bool
	mu      sync.Mutex
	stopCh  chan struct{}
}

func newSimState(config simulator.SimConfig) (*simState, error) {
	sim, err := simulator.NewSimulator(config)
	if err != nil {
		return nil, err
	}

	return &simState{
		sim:     sim,
		running: false,
		paused:  false,
		stopCh:  make(chan struct{}),
	}, nil
}

// start begins the simulation (sets running flag)
func (s *simState) start() {
	s.mu.Lock()
	defer s.mu.Unlock()
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
func (s *simState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sim.Reset()
	s.running = false
	s.paused = false
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

// step advances simulation by deltaT (called by UI ticker)
func (s *simState) step(deltaT float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running && !s.paused {
		s.sim.Step(deltaT)
	}
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

// stop signals the UI loop to stop
func (s *simState) stop() {
	close(s.stopCh)
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
				// Advance simulation by 1 virtual second
				state.step(1.0)

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

	// Handle messages from client
	for {
		var msg ClientMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Error reading message: %v", err)
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
			state.reset()
			log.Println("Simulator reset")
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
				} else {
					log.Printf("Config updated: %+v", msg.Config)
					running := state.isRunning()
					statusMsg := ServerMessage{
						Type:    "status",
						Running: &running,
						Config:  msg.Config,
					}
					safeConn.WriteJSON(statusMsg)
				}
			}
		}
	}

	// Clean up
	state.stop()
	log.Println("Client disconnected")
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.Execute(w, nil); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
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
	// Load templates
	templatePath := filepath.Join("templates", "index.html")
	var err error
	indexTemplate, err = template.ParseFiles(templatePath)
	if err != nil {
		log.Fatalf("Error loading template: %v", err)
	}
	log.Printf("✓ Loaded template: %s", templatePath)

	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/quitquitquit", quitHandler)

	addr := ":8080"
	log.Printf("🚀 Server starting on http://localhost%s", addr)
	log.Printf("📡 WebSocket endpoint: ws://localhost%s/ws", addr)
	log.Printf("🛑 Shutdown endpoint: http://localhost%s/quitquitquit", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
