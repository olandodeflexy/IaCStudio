package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State holds the current state of a project as seen by the UI.
type State struct {
	Name         string            `json:"name"`
	Tool         string            `json:"tool"`
	Path         string            `json:"path"`
	Resources    []Node            `json:"resources"`
	Layout       string            `json:"layout,omitempty"`
	Blueprint    string            `json:"blueprint,omitempty"`
	ProjectName  string            `json:"project_name,omitempty"`
	Cloud        string            `json:"cloud,omitempty"`
	Environments []string          `json:"environments,omitempty"`
	EnvTools     map[string]string `json:"environment_tools,omitempty"`
	Modules      json.RawMessage   `json:"modules,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// Node represents a resource on the visual canvas.
type Node struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Label       string         `json:"label"`
	Icon        string         `json:"icon"`
	Properties  map[string]any `json:"properties"`
	File        string         `json:"file,omitempty"`
	Line        int            `json:"line,omitempty"`
	X           float64        `json:"x"`
	Y           float64        `json:"y"`
	Connections []Connection   `json:"connections,omitempty"`
}

// Connection represents a link between two resources.
type Connection struct {
	TargetID string `json:"target_id"`
	Field    string `json:"field"` // The property that references the target
	Label    string `json:"label"` // e.g., "vpc_id", "subnet_id"
}

// Manager handles project state persistence.
type Manager struct {
	projectsDir string
	states      map[string]*State
	mu          sync.RWMutex
}

func NewManager(projectsDir string) *Manager {
	return &Manager{
		projectsDir: projectsDir,
		states:      make(map[string]*State),
	}
}

// stateFilePath returns the path to the .iac-studio.json state file.
func (m *Manager) stateFilePath(projectName string) string {
	return filepath.Join(m.projectsDir, projectName, ".iac-studio.json")
}

// Load reads project state from disk.
func (m *Manager) Load(projectName string) (*State, error) {
	m.mu.RLock()
	if s, ok := m.states[projectName]; ok {
		m.mu.RUnlock()
		return s, nil
	}
	m.mu.RUnlock()

	path := m.stateFilePath(projectName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	if state.Name == "" {
		state.Name = projectName
	}
	if state.Path == "" {
		state.Path = filepath.Join(m.projectsDir, projectName)
	}

	m.mu.Lock()
	m.states[projectName] = &state
	m.mu.Unlock()

	return &state, nil
}

// Save writes project state to disk.
func (m *Manager) Save(projectName string, state *State) error {
	if state.CreatedAt.IsZero() {
		state.CreatedAt = time.Now()
	}
	state.UpdatedAt = time.Now()

	m.mu.Lock()
	m.states[projectName] = state
	m.mu.Unlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	path := m.stateFilePath(projectName)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}

	return nil
}

// Delete removes project state from memory and disk.
func (m *Manager) Delete(projectName string) error {
	m.mu.Lock()
	delete(m.states, projectName)
	m.mu.Unlock()

	path := m.stateFilePath(projectName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListAll returns all known project states.
func (m *Manager) ListAll() ([]*State, error) {
	entries, err := os.ReadDir(m.projectsDir)
	if err != nil {
		return nil, err
	}

	var states []*State
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		state, err := m.Load(e.Name())
		if err != nil {
			continue
		}
		if state == nil {
			// No state file, create minimal info
			state = &State{
				Name: e.Name(),
				Path: filepath.Join(m.projectsDir, e.Name()),
			}
		}
		states = append(states, state)
	}
	return states, nil
}
