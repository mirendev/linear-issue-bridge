package linearapi

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

type Issue struct {
	ID          string
	Identifier  string
	Title       string
	Description string
	State       State
	Priority    int
	Labels      []Label
	Attachments []Attachment
	Relations   []Relation
	URL       string
	CreatedAt time.Time
	UpdatedAt time.Time
	Creator   Creator
}

type Relation struct {
	Type               string
	RelatedIdentifier  string
	RelatedTitle       string
}

type Creator struct {
	DisplayName string
	AvatarURL   string
}

type Attachment struct {
	URL   string
	Title string
}

type State struct {
	Name  string
	Color string
	Type  string // backlog, unstarted, started, completed, cancelled
}

type Label struct {
	ID    string
	Name  string
	Color string
}

func (s State) DisplayName() string {
	switch strings.ToLower(s.Name) {
	case "todo", "triage", "backlog":
		return "Open"
	case "in progress", "in review":
		return "In Progress"
	case "done":
		return "Done"
	default:
		if s.Type == "completed" || s.Type == "cancelled" {
			return "Done"
		}
		return s.Name
	}
}

func (s State) DisplayColor() string {
	switch s.DisplayName() {
	case "In Progress":
		return "#4caf50"
	case "Open":
		return "#9e9e9e"
	case "Done":
		return "#bb87fc"
	default:
		return s.Color
	}
}

var stateOrder = map[string]int{
	"In Progress": 0,
	"Open":        1,
	"Done":        2,
}

func (i *Issue) stateRank() int {
	if rank, ok := stateOrder[i.State.DisplayName()]; ok {
		return rank
	}
	return 3
}

func SortByProgress(issues []*Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		ri, rj := issues[i].stateRank(), issues[j].stateRank()
		if ri != rj {
			return ri < rj
		}
		return issues[i].UpdatedAt.After(issues[j].UpdatedAt)
	})
}

func (i *Issue) IsOpen() bool {
	switch i.State.Type {
	case "completed", "cancelled":
		return false
	}
	switch i.State.Name {
	case "Canceled", "Cancelled", "Duplicate", "Done":
		return false
	}
	return true
}

func (i *Issue) DuplicateOf() *Relation {
	for idx := range i.Relations {
		if i.Relations[idx].Type == "duplicate" {
			return &i.Relations[idx]
		}
	}
	return nil
}

func (i *Issue) HasLabel(name string) bool {
	for _, l := range i.Labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

var githubPRPattern = regexp.MustCompile(`^https://github\.com/.+/pull/\d+`)

func (i *Issue) GitHubPRs() []Attachment {
	var prs []Attachment
	for _, a := range i.Attachments {
		if githubPRPattern.MatchString(a.URL) {
			prs = append(prs, a)
		}
	}
	return prs
}
