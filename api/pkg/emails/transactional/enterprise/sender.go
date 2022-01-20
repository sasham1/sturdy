package enterprise

import (
	"context"
	"errors"
	"fmt"

	db_codebase "mash/pkg/codebase/db"
	"mash/pkg/emails/transactional"
	"mash/pkg/emails/transactional/templates"
	db_github "mash/pkg/github/enterprise/db"
	"mash/pkg/notification"
	"mash/pkg/user"
)

type Sender struct {
	*transactional.Sender

	codebaseRepo         db_codebase.CodebaseRepository
	githubRepositoryRepo db_github.GitHubRepositoryRepo
}

func New(
	ossSender *transactional.Sender,
	codebaseRepo db_codebase.CodebaseRepository,
	githubRepositoryRepo db_github.GitHubRepositoryRepo,
) *Sender {
	return &Sender{
		Sender:               ossSender,
		codebaseRepo:         codebaseRepo,
		githubRepositoryRepo: githubRepositoryRepo,
	}
}

func (e *Sender) SendNotification(ctx context.Context, usr *user.User, notif *notification.Notification) error {
	err := e.Sender.SendNotification(ctx, usr, notif)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, transactional.ErrNotSupported):
	default:
		return fmt.Errorf("failed to send notification: %w", err)
	}

	switch notif.NotificationType {
	case notification.GitHubRepositoryImported:
		if err := e.sendGitHubRepositoryImportedNotification(ctx, usr, notif.ReferenceID); err != nil {
			return fmt.Errorf("failed to send github repository imported notification: %w", err)
		}
		return nil
	default:
		return transactional.ErrNotSupported
	}
}

func (e *Sender) sendGitHubRepositoryImportedNotification(ctx context.Context, u *user.User, gitHubRepoID string) error {
	repo, err := e.githubRepositoryRepo.GetByID(gitHubRepoID)
	if err != nil {
		return fmt.Errorf("failed to get github reposotory: %w", err)
	}
	c, err := e.codebaseRepo.Get(repo.CodebaseID)
	if err != nil {
		return fmt.Errorf("failed to get codebase: %w", err)
	}
	return e.Send(
		ctx,
		u,
		fmt.Sprintf("[Sturdy] \"%s\" is now ready", c.Name),
		templates.NotificationGitHubRepositoryImportedTemplate,
		&templates.NotificationGitHubRepositoryImportedTemplateData{
			GitHubRepo: repo,
			Codebase:   c,
			User:       u,
		})
}