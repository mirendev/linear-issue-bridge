package linearapi

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

type PublicLabeler struct {
	client  *Client
	teamKey string

	labelOnce sync.Once
	labelID   string
	labelErr  error
}

func NewPublicLabeler(client *Client, teamKey string) *PublicLabeler {
	return &PublicLabeler{
		client:  client,
		teamKey: teamKey,
	}
}

func (l *PublicLabeler) EnsurePublicLabel(ctx context.Context, identifier string) error {
	issue, err := l.client.FetchIssue(ctx, identifier)
	if err != nil {
		return fmt.Errorf("fetch issue %s: %w", identifier, err)
	}
	if issue == nil {
		slog.Info("issue not found, skipping", "identifier", identifier)
		return nil
	}

	if issue.HasLabel("public") {
		slog.Info("issue already has public label", "identifier", identifier)
		return nil
	}

	labelID, err := l.resolveLabelID(ctx)
	if err != nil {
		return err
	}

	if err := l.client.AddLabel(ctx, issue.ID, labelID); err != nil {
		return fmt.Errorf("add label to %s: %w", identifier, err)
	}

	slog.Info("applied public label", "identifier", identifier)
	return nil
}

func (l *PublicLabeler) resolveLabelID(ctx context.Context) (string, error) {
	l.labelOnce.Do(func() {
		l.labelID, l.labelErr = l.client.FetchLabelByName(ctx, l.teamKey, "public")
		if l.labelErr == nil && l.labelID == "" {
			l.labelErr = fmt.Errorf("label %q not found in team %s", "public", l.teamKey)
		}
	})
	return l.labelID, l.labelErr
}
