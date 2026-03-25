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
	URL       string
	CreatedAt time.Time
	UpdatedAt time.Time
	Creator   Creator
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

var stateOrder = map[string]int{
	"in review":   0,
	"in progress": 1,
	"todo":        2,
	"triage":      3,
	"backlog":     4,
}

func (i *Issue) stateRank() int {
	if rank, ok := stateOrder[strings.ToLower(i.State.Name)]; ok {
		return rank
	}
	return 3 // default to triage-level
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
