package ui

import (
	"html/template"
	"sync"
	"time"
)

// state is a small in-memory store used to render SPA content.
// It is guarded by a mutex so handlers remain safe under concurrency.
type state struct {
	mu           sync.RWMutex
	tasks        []string
	lastUpdated  time.Time
	serviceState string
}

// shellViewModel is used by the top-level template that hosts HTMX fragments.
type shellViewModel struct {
	Title   string
	Now     string
	Content template.HTML
}

// panelViewModel is reused across dashboard/tasks/settings fragments.
type panelViewModel struct {
	Now          string
	Tasks        []string
	LastUpdated  string
	ServiceState string
}

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

func (s *state) addTask(task string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append([]string{task}, s.tasks...)
	s.lastUpdated = time.Now().UTC()
}
