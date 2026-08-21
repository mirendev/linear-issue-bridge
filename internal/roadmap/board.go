package roadmap

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

// Visibility contract: nothing reaches the public board until someone
// deliberately labels it in Linear. Projects are invisible until labelled
// `public`, `Roadmap - In Progress`, or `Roadmap - Shipped`; issues until
// labelled `Roadmap - Up Next` or `Roadmap - Researching`. The GraphQL filter
// already selects on those labels, and everything here re-checks anyway:
// defense in depth, because a query change should never be able to widen what
// the public sees.
const (
	PublicLabel      = "public"
	InProgressLabel  = "Roadmap - In Progress"
	ShippedLabel     = "Roadmap - Shipped"
	UpNextLabel      = "Roadmap - Up Next"
	ResearchingLabel = "Roadmap - Researching"
)

// ProjectLabels admit a project to the board at all.
var ProjectLabels = []string{PublicLabel, InProgressLabel, ShippedLabel}

// IssueLabels are the per-column opt-in for individual issues. The 2026-08-11
// call settled this: explicit labels, never implicit "top N of the backlog",
// so nothing shows up merely because of where it sits in a list.
var IssueLabels = []string{UpNextLabel, ResearchingLabel}

// denyLabels force-hide regardless of any opt-in, compared case-insensitively.
// `security` is the load-bearing one and is deliberately the same convention
// the issue bridge already uses in Issue.IsPublic: it gets applied in Linear
// by whoever knows a thing is sensitive, at the moment they know it, which is
// exactly when a hardcoded id list cannot help.
var denyLabels = map[string]bool{
	"marketing":  true,
	"video":      true,
	"conference": true,
	"blogs":      true,
	"security":   true,
}

// denyIDs must never render regardless of labels.
var denyIDs = map[string]bool{
	"09eccf4d-0975-48f7-bb01-392720053237": true,
}

// hiddenIDs are editorial hides: not sensitive, just not for the board.
var hiddenIDs = map[string]bool{}

const (
	// ColumnLimit caps every column. The board is a glance, not an archive.
	ColumnLimit = 5
	// ShippedWindow keeps Shipped a highlight reel rather than a changelog.
	ShippedWindow = 30 * 24 * time.Hour
)

// releasePattern matches a version label like "v0.31", which renders inside
// the Shipped pill instead of as an ordinary tag.
var releasePattern = regexp.MustCompile(`(?i)^v\d`)

// Card is one item on the board. It carries only what the page renders, so
// internal Linear fields cannot leak through the wire format by accident.
type Card struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	StatusName  string   `json:"statusName"`
	Labels      []string `json:"labels"`
	Release     *string  `json:"release"`
	Links       []Link   `json:"links"`
	// DocsURL and BlogURL remain until the website has deployed the generic
	// links contract. They can then disappear without a flag day.
	DocsURL *string `json:"docsUrl"`
	BlogURL *string `json:"blogUrl"`
	Votes   int     `json:"votes"`
}

// Link is a titled Linear project resource safe to render on the public card.
type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Board is the finished four-column roadmap.
type Board struct {
	Exploring  []Card `json:"exploring"`
	UpNext     []Card `json:"upNext"`
	InProgress []Card `json:"inProgress"`
	Shipped    []Card `json:"shipped"`
	Stale      bool   `json:"stale,omitempty"`
}

func hasDeniedLabel(names []string) bool {
	for _, n := range names {
		if denyLabels[strings.ToLower(n)] {
			return true
		}
	}
	return false
}

func containsAny(names []string, wanted []string) bool {
	for _, n := range names {
		for _, w := range wanted {
			if n == w {
				return true
			}
		}
	}
	return false
}

// projectAllowed reports whether a project may appear anywhere on the board.
func projectAllowed(p *linearapi.Project) bool {
	if denyIDs[p.ID] || hiddenIDs[p.ID] {
		return false
	}
	names := p.LabelNames()
	if !containsAny(names, ProjectLabels) {
		return false
	}
	return !hasDeniedLabel(names)
}

// isInProgress decides the In Progress column. The explicit label decides;
// status only rules out terminal states, so a completed project never lingers
// here and reappears under Shipped once the Thursday review swaps its label.
func isInProgress(p *linearapi.Project) bool {
	switch p.Status.Type {
	case "completed", "canceled", "cancelled":
		return false
	}
	return p.HasLabel(InProgressLabel)
}

// issueAllowed applies the same gates to an individual issue.
func issueAllowed(i *linearapi.Issue) bool {
	if denyIDs[i.ID] || hiddenIDs[i.ID] {
		return false
	}
	switch i.State.Type {
	case "completed", "canceled", "cancelled":
		return false
	}
	names := make([]string, 0, len(i.Labels))
	for _, l := range i.Labels {
		names = append(names, l.Name)
	}
	if !containsAny(names, IssueLabels) {
		return false
	}
	return !hasDeniedLabel(names)
}

// issueColumn picks a column from the explicit label. An issue carrying both
// lands in the nearer-term one.
func issueColumn(i *linearapi.Issue) string {
	for _, l := range i.Labels {
		if l.Name == UpNextLabel {
			return "upNext"
		}
	}
	for _, l := range i.Labels {
		if l.Name == ResearchingLabel {
			return "researching"
		}
	}
	return ""
}

// priorityRank orders Urgent(1) first through Low(4), with None(0) last.
func priorityRank(p int) int {
	if p == 0 {
		return 99
	}
	return p
}

func sortByPriority(cards []Card, priorities map[string]int) {
	sort.SliceStable(cards, func(a, b int) bool {
		return priorityRank(priorities[cards[a].ID]) < priorityRank(priorities[cards[b].ID])
	})
}

func projectLinks(p *linearapi.Project) []Link {
	links := make([]Link, 0, len(p.ExternalLinks))
	for _, l := range p.ExternalLinks {
		title := strings.TrimSpace(l.Label)
		rawURL := strings.TrimSpace(l.URL)
		parsed, err := url.Parse(rawURL)
		// Untitled links have no useful anchor text, and only https links are
		// trusted onto the page.
		if title == "" || err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" {
			continue
		}
		links = append(links, Link{Title: title, URL: rawURL})
	}
	return links
}

func findLink(links []Link, pattern *regexp.Regexp) *string {
	for _, l := range links {
		if pattern.MatchString(l.Title) {
			url := l.URL
			return &url
		}
	}
	return nil
}

var (
	docsPattern = regexp.MustCompile(`(?i)docs`)
	blogPattern = regexp.MustCompile(`(?i)blog|launch`)
)

// projectCard builds the common card shape. The opt-in labels are plumbing,
// not display tags, so they never reach the page.
func projectCard(p *linearapi.Project) Card {
	display := []string{}
	links := projectLinks(p)
	var release *string
	for _, name := range p.LabelNames() {
		if containsAny([]string{name}, ProjectLabels) {
			continue
		}
		if release == nil && releasePattern.MatchString(name) {
			r := name
			release = &r
			continue
		}
		display = append(display, name)
	}
	return Card{
		ID:          p.ID,
		Title:       p.Name,
		Description: CleanDescription(p.Description, defaultSummaryMax),
		StatusName:  p.Status.Name,
		Labels:      display,
		Release:     release,
		Links:       links,
		DocsURL:     findLink(links, docsPattern),
		BlogURL:     findLink(links, blogPattern),
	}
}

// issueCard uses the same authored-copy rule as In Progress project cards:
// the body comes from a "Roadmap Summary:" block or stays blank.
func issueCard(i *linearapi.Issue) Card {
	display := []string{}
	for _, l := range i.Labels {
		if l.Name == PublicLabel || containsAny([]string{l.Name}, IssueLabels) {
			continue
		}
		display = append(display, l.Name)
	}
	return Card{
		ID:          i.ID,
		Title:       i.Title,
		Description: RoadmapSummary(i.Description),
		StatusName:  i.State.Name,
		Labels:      display,
		Links:       []Link{},
	}
}

// Build turns raw Linear records into the finished board.
func Build(projects []*linearapi.Project, issues []*linearapi.Issue, now time.Time) Board {
	priorities := map[string]int{}

	var allowed []*linearapi.Project
	for _, p := range projects {
		if projectAllowed(p) {
			allowed = append(allowed, p)
			priorities[p.ID] = p.Priority
		}
	}

	// In Progress is claimed first (label beats status) and claimed projects
	// are excluded from the other columns, so nothing renders twice. A Started
	// project with no label gets no column at all: explicit opt-in only.
	claimed := map[string]bool{}
	var inProgress []Card
	for _, p := range allowed {
		if !isInProgress(p) {
			continue
		}
		claimed[p.ID] = true
		card := projectCard(p)
		// In Progress copy is authored, not scraped: it comes from the project
		// document, never the short internal one-liner.
		card.Description = RoadmapSummary(p.Content)
		inProgress = append(inProgress, card)
	}
	sortByPriority(inProgress, priorities)
	inProgress = capColumn(inProgress)

	// The status-bucketed columns take only projects opted in via `public`, so
	// a column label alone can't spill a project into an unrelated column.
	var exploring, upNext []Card
	var shipped []Card
	shippedCutoff := now.Add(-ShippedWindow)

	for _, p := range allowed {
		if claimed[p.ID] {
			continue
		}
		if p.HasLabel(ShippedLabel) && p.Status.Type == "completed" &&
			p.CompletedAt != nil && !p.CompletedAt.Before(shippedCutoff) {
			shipped = append(shipped, projectCard(p))
			continue
		}
		if !p.HasLabel(PublicLabel) {
			continue
		}
		switch p.Status.Type {
		case "backlog":
			exploring = append(exploring, projectCard(p))
		case "planned":
			upNext = append(upNext, projectCard(p))
		}
	}

	sortByPriority(exploring, priorities)
	sortByPriority(upNext, priorities)

	// Shipped is capped to the most recently *completed*. The query's updatedAt
	// order would let an edit to an old project bump it past a newer ship.
	completedAt := map[string]time.Time{}
	for _, p := range allowed {
		if p.CompletedAt != nil {
			completedAt[p.ID] = *p.CompletedAt
		}
	}
	sort.SliceStable(shipped, func(a, b int) bool {
		return completedAt[shipped[a].ID].After(completedAt[shipped[b].ID])
	})
	shipped = capColumn(shipped)

	// Issue cards slot in after any project cards in the same column. Within a
	// column priority wins; the query returns newest-first and the sort is
	// stable, so equal priorities stay in recency order.
	var researchIssues, queuedIssues []Card
	issuePriorities := map[string]int{}
	for _, i := range issues {
		if !issueAllowed(i) {
			continue
		}
		issuePriorities[i.ID] = i.Priority
		switch issueColumn(i) {
		case "upNext":
			queuedIssues = append(queuedIssues, issueCard(i))
		case "researching":
			researchIssues = append(researchIssues, issueCard(i))
		}
	}
	sortByPriority(researchIssues, issuePriorities)
	sortByPriority(queuedIssues, issuePriorities)

	return Board{
		Exploring:  capColumn(append(exploring, researchIssues...)),
		UpNext:     capColumn(append(upNext, queuedIssues...)),
		InProgress: inProgress,
		Shipped:    shipped,
	}
}

func capColumn(cards []Card) []Card {
	if cards == nil {
		return []Card{}
	}
	if len(cards) > ColumnLimit {
		return cards[:ColumnLimit]
	}
	return cards
}
