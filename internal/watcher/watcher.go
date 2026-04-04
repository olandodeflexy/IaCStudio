package watcher

import (
	"encoding/json"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Broadcaster is the interface for pushing updates to clients.
type Broadcaster interface {
	Broadcast(msg []byte)
}

// FileWatcher monitors project directories and notifies the UI of changes.
type FileWatcher struct {
	watcher    *fsnotify.Watcher
	hub        Broadcaster
	paused     map[string]bool
	mu         sync.RWMutex
	debounce   map[string]*time.Timer
	debounceMu sync.Mutex
}

func New(hub Broadcaster) *FileWatcher {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Cannot create file watcher: %v", err)
	}

	fw := &FileWatcher{
		watcher:  w,
		hub:      hub,
		paused:   make(map[string]bool),
		debounce: make(map[string]*time.Timer),
	}

	go fw.loop()
	return fw
}

// Watch adds a directory to the watch list.
func (fw *FileWatcher) Watch(dir string) error {
	log.Printf("Watching: %s", dir)
	return fw.watcher.Add(dir)
}

// Pause temporarily stops notifications for a directory (used during UI → disk writes).
func (fw *FileWatcher) Pause(dir string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.paused[dir] = true
}

// Resume re-enables notifications for a directory.
func (fw *FileWatcher) Resume(dir string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	delete(fw.paused, dir)
}

func (fw *FileWatcher) Close() {
	fw.watcher.Close()
}

func (fw *FileWatcher) loop() {
	for {
		select {
		case event, ok := <-fw.watcher.Events:
			if !ok {
				return
			}
			fw.handleEvent(event)

		case err, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

func (fw *FileWatcher) handleEvent(event fsnotify.Event) {
	// Only care about writes and creates to IaC files
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
		return
	}

	ext := filepath.Ext(event.Name)
	if ext != ".tf" && ext != ".yml" && ext != ".yaml" {
		return
	}

	// Check if this directory is paused (UI is writing)
	dir := filepath.Dir(event.Name)
	fw.mu.RLock()
	if fw.paused[dir] {
		fw.mu.RUnlock()
		return
	}
	fw.mu.RUnlock()

	// Debounce: wait 500ms before notifying (editors save multiple times)
	fw.debounceMu.Lock()
	if timer, exists := fw.debounce[event.Name]; exists {
		timer.Stop()
	}
	fw.debounce[event.Name] = time.AfterFunc(500*time.Millisecond, func() {
		fw.notify(event.Name, dir)
	})
	fw.debounceMu.Unlock()
}

func (fw *FileWatcher) notify(file, dir string) {
	// Determine project name from directory
	project := filepath.Base(dir)
	tool := "terraform"
	if strings.HasSuffix(file, ".yml") || strings.HasSuffix(file, ".yaml") {
		tool = "ansible"
	}

	msg := map[string]interface{}{
		"type":    "file_changed",
		"project": project,
		"file":    file,
		"tool":    tool,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal file change: %v", err)
		return
	}

	log.Printf("File changed: %s", file)
	fw.hub.Broadcast(data)
}
