package tasks

import (
	"context"
	"time"

	"github.com/augustoroman/taskmaster/tasks/schedule"
)

type ID string

type Store interface {
	List(ctx context.Context) ([]TaskWithID, error)
	Get(ctx context.Context, id ID) (Task, error)
	Add(ctx context.Context, t Task) (ID, error)
	Do(ctx context.Context, id ID) error
	Delete(ctx context.Context, id ID) error
}

type TaskWithID struct {
	ID
	Task
}

type Task struct {
	Title             string      `json:"title"`
	Start             time.Time   `json:"start"`
	History           []time.Time `json:"history"` // most recent first
	schedule.Schedule `json:"schedule"`
	Deleted           bool `json:"deleted"`
}

func (t *Task) Next() time.Time {
	return t.Schedule.Next(time.Now(), t.Start, t.last())
}

func (t *Task) last() time.Time {
	if len(t.History) == 0 {
		return t.Start
	}
	return t.History[0]
}
