package roadmap

import (
	"strings"
	"testing"
	"time"

	"miren.dev/linear-issue-bridge/internal/linearapi"
)

// Fixtures ported from the website's roadmap.test.ts. The critical assertions
// are the safety guarantees: a denylisted id, a denied label, and anything
// without an opt-in label must never reach the board.

const denyID = "09eccf4d-0975-48f7-bb01-392720053237"

func labels(names ...string) []linearapi.Label {
	out := make([]linearapi.Label, 0, len(names))
	for _, n := range names {
		out = append(out, linearapi.Label{Name: n})
	}
	return out
}

func ago(d time.Duration) *time.Time {
	t := time.Now().Add(-d)
	return &t
}

func projects() []*linearapi.Project {
	return []*linearapi.Project{
		{ID: "explore-1", Name: "Explore 1", Status: linearapi.ProjectStatus{Name: "Idea", Type: "backlog"},
			Labels: labels("public")},
		{ID: "upnext-1", Name: "Up Next 1", Status: linearapi.ProjectStatus{Name: "Planned", Type: "planned"},
			Labels: labels("public")},
		// In Progress is label-driven: the label alone admits the project.
		// The short internal one-liner must NOT leak onto the card.
		{ID: "progress-1", Name: "Progress 1", Status: linearapi.ProjectStatus{Name: "In Progress", Type: "started"},
			Labels:      labels(InProgressLabel),
			Description: "Short internal one-liner.",
			Content:     "Roadmap Summary: Deploy without a Dockerfile wrapper.\n\nInternal: RFD-91, depends on MIR-1428."},
		// A Started status without the label shows nowhere.
		{ID: "started-unlabelled", Name: "Started", Status: linearapi.ProjectStatus{Name: "In Progress", Type: "started"},
			Labels: labels("public")},
		// The label beats status: a backlog-status project marked in-progress.
		{ID: "wip-backlog", Name: "WIP Backlog", Status: linearapi.ProjectStatus{Name: "Idea", Type: "backlog"},
			Labels:      labels(InProgressLabel),
			Description: "A one-liner that must not show either.",
			Content:     "Long internal prose with no summary block. Never for the public board."},
		// Heading-style summary block.
		{ID: "wip-heading", Name: "WIP Heading", Status: linearapi.ProjectStatus{Name: "In Progress", Type: "started"},
			Labels:  labels(InProgressLabel),
			Content: "Context first.\n\n## Roadmap Summary\n\nMachine-readable docs entry points.\n\n## Design\nInternals."},
		// Completed but still wearing the In Progress label: shows nowhere.
		{ID: "wip-done", Name: "WIP Done", Status: linearapi.ProjectStatus{Name: "Completed", Type: "completed"},
			Labels: labels(InProgressLabel), CompletedAt: ago(2 * 24 * time.Hour)},
		{ID: "shipped-1", Name: "Shipped 1", Status: linearapi.ProjectStatus{Name: "Completed", Type: "completed"},
			Labels: labels(ShippedLabel, "v0.31"), CompletedAt: ago(5 * 24 * time.Hour),
			ExternalLinks: []linearapi.ExternalLink{
				{URL: "https://miren.md/anywhere", Label: "Docs"},
				{URL: "https://miren.dev/blog/anywhere", Label: "Launch post"},
				{URL: "http://insecure.example.com", Label: "Docs mirror"},
			}},
		// Listed after shipped-1 but completed more recently.
		{ID: "shipped-2", Name: "Shipped 2", Status: linearapi.ProjectStatus{Name: "Completed", Type: "completed"},
			Labels: labels(ShippedLabel), CompletedAt: ago(24 * time.Hour)},
		// The security label force-hides regardless of opt-in, case-insensitively.
		{ID: "sec-1", Name: "Sensitive", Status: linearapi.ProjectStatus{Name: "In Progress", Type: "started"},
			Labels: labels(InProgressLabel, "Security")},
		// Older than the 30-day window, and undated: neither may appear.
		{ID: "shipped-old", Name: "Old", Status: linearapi.ProjectStatus{Name: "Completed", Type: "completed"},
			Labels: labels(ShippedLabel), CompletedAt: ago(60 * 24 * time.Hour)},
		{ID: "shipped-undated", Name: "Undated", Status: linearapi.ProjectStatus{Name: "Completed", Type: "completed"},
			Labels: labels(ShippedLabel)},
		// `public` alone isn't enough for Shipped.
		{ID: "shipped-unlabelled", Name: "Unlabelled", Status: linearapi.ProjectStatus{Name: "Completed", Type: "completed"},
			Labels: labels("public"), CompletedAt: ago(3 * 24 * time.Hour)},
		// The hard denylist wins over a valid opt-in label.
		{ID: denyID, Name: "Credential rotation", Status: linearapi.ProjectStatus{Name: "Idea", Type: "backlog"},
			Labels: labels("public")},
		// A public project also carrying a denied label must drop.
		{ID: "mktg-1", Name: "Marketing", Status: linearapi.ProjectStatus{Name: "Planned", Type: "planned"},
			Labels: labels("public", "Marketing")},
		// Paused gets no column.
		{ID: "paused-1", Name: "Paused", Status: linearapi.ProjectStatus{Name: "Paused", Type: "paused"},
			Labels: labels("public")},
		{ID: "clean-1", Name: "Clean", Status: linearapi.ProjectStatus{Name: "Idea", Type: "backlog"},
			Labels:      labels("public"),
			Description: "Durable disks.\n\n<linear-embed>junk</linear-embed> **bold** [rfd](http://x)"},
	}
}

func issue(id string, lbls []linearapi.Label, opts ...func(*linearapi.Issue)) *linearapi.Issue {
	i := &linearapi.Issue{
		ID:     id,
		Title:  "Untitled idea",
		State:  linearapi.State{Name: "Backlog", Type: "backlog"},
		Labels: lbls,
	}
	for _, o := range opts {
		o(i)
	}
	return i
}

func issues() []*linearapi.Issue {
	out := []*linearapi.Issue{
		// The explicit label decides the column: backlog state, but queued.
		issue("idea-queued", labels(UpNextLabel), func(i *linearapi.Issue) {
			i.Description = "Roadmap Summary: Upgrades without dropped requests.\n\n## Problem\nInternal notes that must never render."
		}),
		// No roadmap-* label: must never render.
		issue("idea-private", nil),
		// `public` gates projects only; it no longer admits issues.
		issue("idea-legacy", labels("public")),
		// Labelled but terminal: must drop.
		issue("idea-done", labels(ResearchingLabel), func(i *linearapi.Issue) {
			i.State = linearapi.State{Name: "Done", Type: "completed"}
		}),
		// The security label vetoes an otherwise valid opt-in on issues too.
		issue("idea-sec", labels(UpNextLabel, "security")),
		// Highest priority survives the cap.
		issue("idea-hot", labels(ResearchingLabel), func(i *linearapi.Issue) { i.Priority = 2 }),
	}
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		out = append(out, issue("idea-bl-"+n, labels(ResearchingLabel)))
	}
	return out
}

func build() Board {
	return Build(projects(), issues(), time.Now())
}

func allIDs(b Board) []string {
	var ids []string
	for _, col := range [][]Card{b.Exploring, b.UpNext, b.InProgress, b.Shipped} {
		for _, c := range col {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func ids(cards []Card) []string {
	out := make([]string, 0, len(cards))
	for _, c := range cards {
		out = append(out, c.ID)
	}
	return out
}

func find(t *testing.T, cards []Card, id string) Card {
	t.Helper()
	for _, c := range cards {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("card %q not found in %v", id, ids(cards))
	return Card{}
}

func TestDenylistedIDNeverReachesTheBoard(t *testing.T) {
	if contains(allIDs(build()), denyID) {
		t.Error("the denylisted project id reached the board")
	}
}

func TestDeniedLabelsAndNonColumnStatusesDrop(t *testing.T) {
	got := allIDs(build())
	for _, id := range []string{
		"mktg-1",
		"paused-1",
		"sec-1",    // "Security" capitalised, on a project
		"idea-sec", // "security" lowercase, on an issue
	} {
		if contains(got, id) {
			t.Errorf("%q reached the board but must be hidden", id)
		}
	}
}

func TestBucketsProjectsByStatus(t *testing.T) {
	b := build()
	if !contains(ids(b.Exploring), "explore-1") {
		t.Errorf("exploring = %v, want explore-1", ids(b.Exploring))
	}
	if !contains(ids(b.UpNext), "upnext-1") {
		t.Errorf("upNext = %v, want upnext-1", ids(b.UpNext))
	}
}

func TestInProgressIsLabelDriven(t *testing.T) {
	b := build()
	got := ids(b.InProgress)
	if !contains(got, "progress-1") || !contains(got, "wip-backlog") || !contains(got, "wip-heading") {
		t.Errorf("inProgress = %v, want the three labelled projects", got)
	}
	// A Started status without the label shows nowhere.
	if contains(allIDs(b), "started-unlabelled") {
		t.Error("an unlabelled Started project reached the board")
	}
	// Completed still wearing the In Progress label shows nowhere.
	if contains(allIDs(b), "wip-done") {
		t.Error("a completed project lingered in In Progress")
	}
}

func TestInProgressCopyIsAuthored(t *testing.T) {
	b := build()
	// Inline label: the summary sentence only, internal prose never leaks.
	if got := find(t, b.InProgress, "progress-1").Description; got != "Deploy without a Dockerfile wrapper." {
		t.Errorf("progress-1 description = %q", got)
	}
	// Heading form ends at the next blank line.
	if got := find(t, b.InProgress, "wip-heading").Description; got != "Machine-readable docs entry points." {
		t.Errorf("wip-heading description = %q", got)
	}
	// No block means a blank body, not the scraped one-liner.
	if got := find(t, b.InProgress, "wip-backlog").Description; got != "" {
		t.Errorf("wip-backlog description = %q, want empty", got)
	}
}

func TestShippedWindowAndOrdering(t *testing.T) {
	b := build()
	got := ids(b.Shipped)
	for _, id := range []string{"shipped-old", "shipped-undated", "shipped-unlabelled"} {
		if contains(got, id) {
			t.Errorf("%q reached Shipped but must not", id)
		}
	}
	// Ordered by completion, not by the query's updatedAt order: shipped-2
	// completed later but arrives after shipped-1 in the response.
	want := []string{"shipped-2", "shipped-1"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("shipped = %v, want %v", got, want)
	}
}

func TestReleaseLabelBecomesThePill(t *testing.T) {
	b := build()
	s1 := find(t, b.Shipped, "shipped-1")
	if s1.Release == nil || *s1.Release != "v0.31" {
		t.Errorf("shipped-1 release = %v, want v0.31", s1.Release)
	}
	if contains(s1.Labels, "v0.31") {
		t.Error("the release label also rendered as a display tag")
	}
	if e1 := find(t, b.Exploring, "explore-1"); e1.Release != nil {
		t.Errorf("explore-1 release = %v, want nil", *e1.Release)
	}
}

func TestLinksComeFromLabelledHTTPSExternalLinks(t *testing.T) {
	s1 := find(t, build().Shipped, "shipped-1")
	if s1.DocsURL == nil || *s1.DocsURL != "https://miren.md/anywhere" {
		t.Errorf("docsUrl = %v, want the https Docs link (the http mirror must be skipped)", s1.DocsURL)
	}
	if s1.BlogURL == nil || *s1.BlogURL != "https://miren.dev/blog/anywhere" {
		t.Errorf("blogUrl = %v", s1.BlogURL)
	}
	if e1 := find(t, build().Exploring, "explore-1"); e1.DocsURL != nil {
		t.Errorf("explore-1 docsUrl = %v, want nil", *e1.DocsURL)
	}
}

func TestGateLabelIsStrippedAndDescriptionCleaned(t *testing.T) {
	c := find(t, build().Exploring, "clean-1")
	if contains(c.Labels, "public") {
		t.Error("the public gate label rendered as a display tag")
	}
	for _, bad := range []string{"<", "linear-embed"} {
		if strings.Contains(c.Description, bad) {
			t.Errorf("description = %q, still contains %q", c.Description, bad)
		}
	}
	if !strings.Contains(c.Description, "bold") {
		t.Errorf("description = %q, lost the emphasised text", c.Description)
	}
}

func TestIssuesFillColumnsViaLabelsAndColumnsCap(t *testing.T) {
	b := build()
	// Label beats state: idea-queued sits in backlog but is labelled up-next.
	if !contains(ids(b.UpNext), "idea-queued") {
		t.Errorf("upNext = %v, want idea-queued", ids(b.UpNext))
	}
	if contains(ids(b.Exploring), "idea-queued") {
		t.Error("idea-queued also landed in Exploring")
	}
	// 1 project card + 6 eligible researching issues, capped at 5.
	if len(b.Exploring) != ColumnLimit {
		t.Errorf("exploring has %d cards, want %d", len(b.Exploring), ColumnLimit)
	}
	for _, id := range []string{"idea-private", "idea-legacy", "idea-done"} {
		if contains(allIDs(b), id) {
			t.Errorf("%q reached the board but must not", id)
		}
	}
	// The gate label is plumbing, not a display tag.
	if q := find(t, b.UpNext, "idea-queued"); contains(q.Labels, UpNextLabel) {
		t.Error("the opt-in label rendered as a display tag")
	}
	// Issue copy is authored the same way project copy is.
	if got := find(t, b.UpNext, "idea-queued").Description; got != "Upgrades without dropped requests." {
		t.Errorf("idea-queued description = %q", got)
	}
}

func TestPriorityBeatsRecencyWithinAColumn(t *testing.T) {
	b := build()
	var issueIDs []string
	for _, c := range b.Exploring {
		if len(c.ID) > 5 && c.ID[:5] == "idea-" {
			issueIDs = append(issueIDs, c.ID)
		}
	}
	if len(issueIDs) == 0 || issueIDs[0] != "idea-hot" {
		t.Errorf("researching issue order = %v, want idea-hot first", issueIDs)
	}
}
