package tasks

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

type Mem struct {
	mu    sync.Mutex
	tasks map[ID]Task
}

var _ Store = &Mem{}

func (m *Mem) List(ctx context.Context) ([]TaskWithID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]TaskWithID, 0, len(m.tasks))
	for id, t := range m.tasks {
		if t.Deleted {
			continue
		}
		list = append(list, TaskWithID{id, t})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ID < list[j].ID
	})
	return list, nil
}
func (m *Mem) Get(ctx context.Context, id ID) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, exists := m.tasks[id]
	if !exists {
		return t, fmt.Errorf("No such task: id=%s", id)
	}
	return t, nil
}
func (m *Mem) Add(ctx context.Context, t Task) (ID, error) {
	log.Println("adding task", t.Title)
	m.mu.Lock()
	defer m.mu.Unlock()
	id := ID(fmt.Sprint(time.Now().UnixNano()))
	if m.tasks == nil {
		m.tasks = map[ID]Task{}
	}
	m.tasks[id] = t
	return id, nil
}
func (m *Mem) Do(ctx context.Context, id ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, exists := m.tasks[id]
	if !exists {
		return fmt.Errorf("No such task: id=%s", id)
	}
	t.History = append([]time.Time{time.Now()}, t.History...)
	m.tasks[id] = t
	return nil
}
func (m *Mem) Delete(ctx context.Context, id ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, exists := m.tasks[id]
	if !exists {
		return fmt.Errorf("No such task: id=%s", id)
	}
	t.Deleted = true
	m.tasks[id] = t
	return nil
}
