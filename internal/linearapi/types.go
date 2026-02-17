package linearapi

import "time"

type Issue struct {
	Identifier  string
	Title       string
	Description string
	State       State
	Priority    int
	Labels      []Label
	URL         string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type State struct {
	Name  string
	Color string
	Type  string // backlog, unstarted, started, completed, cancelled
}

type Label struct {
	Name  string
	Color string
}

func (i *Issue) HasLabel(name string) bool {
	for _, l := range i.Labels {
		if l.Name == name {
			return true
		}
	}
	return false
}
