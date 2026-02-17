package linearapi

import (
	"regexp"
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
	URL         string
	CreatedAt   time.Time
	UpdatedAt   time.Time
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
