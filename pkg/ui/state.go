package ui

import (
	"html/template"
	"sync"
	"time"
)

// state holds demo UI data for templates. It is not durable across restarts.
// A RWMutex protects reads (snapshot) and writes (addTask) under concurrent HTTP handlers.
type state struct {
	mu           sync.RWMutex
	tasks        []string // Newest tasks are stored at the front after addTask.
	lastUpdated  time.Time
	serviceState string // Shown on the dashboard status card.
}

// shellViewModel drives the outer layout template: title, server-side timestamp, and the
// inner HTML produced by executing a panel template into a string.
type shellViewModel struct {
	Title   string
	Now     string
	Content template.HTML // Panel HTML; safe because it originates from our own templates.
}

// panelViewModel is the data shape passed to dashboard, tasks, and settings templates.
type panelViewModel struct {
	Now          string
	Tasks        []string
	LastUpdated  string
	ServiceState string
}

// snapshot returns a consistent copy of state for template rendering under RLock.
func (s *state) snapshot() panelViewModel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasksCopy := make([]string, len(s.tasks))
	copy(tasksCopy, s.tasks)
	return panelViewModel{
		Now:          time.Now().UTC().Format(time.RFC1123),
		Tasks:        tasksCopy,
		LastUpdated:  s.lastUpdated.Format(time.RFC1123),
		ServiceState: s.serviceState,
	}
}

// addTask prepends a task and updates lastUpdated. Must not be called while holding RLock.
func (s *state) addTask(task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append([]string{task}, s.tasks...)
	s.lastUpdated = time.Now().UTC()
}
