package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	git "github.com/libgit2/git2go/v33"
	"go.uber.org/zap"

	"getsturdy.com/api/pkg/analytics"
	analytics_service "getsturdy.com/api/pkg/analytics/service"
	"getsturdy.com/api/pkg/changes/message"
	service_change "getsturdy.com/api/pkg/changes/service"
	vcs_change "getsturdy.com/api/pkg/changes/vcs"
	"getsturdy.com/api/pkg/codebases"
	"getsturdy.com/api/pkg/remote"
	db_remote "getsturdy.com/api/pkg/remote/enterprise/db"
	"getsturdy.com/api/pkg/remote/service"
	"getsturdy.com/api/pkg/snapshots/snapshotter"
	"getsturdy.com/api/pkg/users"
	"getsturdy.com/api/pkg/workspaces"
	db_workspaces "getsturdy.com/api/pkg/workspaces/db"
	"getsturdy.com/api/vcs"
	"getsturdy.com/api/vcs/executor"
)

type EnterpriseService struct {
	repo             db_remote.Repository
	executorProvider executor.Provider
	logger           *zap.Logger
	workspaceReader  db_workspaces.WorkspaceReader
	workspaceWriter  db_workspaces.WorkspaceWriter
	snap             snapshotter.Snapshotter
	changeService    *service_change.Service
	analyticsService *analytics_service.Service
}

var _ service.Service = (*EnterpriseService)(nil)

func New(
	repo db_remote.Repository,
	executorProvider executor.Provider,
	logger *zap.Logger,
	workspaceReader db_workspaces.WorkspaceReader,
	workspaceWriter db_workspaces.WorkspaceWriter,
	snap snapshotter.Snapshotter,
	changeService *service_change.Service,
	analyticsService *analytics_service.Service,
) *EnterpriseService {
	return &EnterpriseService{
		repo:             repo,
		executorProvider: executorProvider,
		logger:           logger,
		workspaceReader:  workspaceReader,
		workspaceWriter:  workspaceWriter,
		snap:             snap,
		changeService:    changeService,
		analyticsService: analyticsService,
	}
}

func (svc *EnterpriseService) Get(ctx context.Context, codebaseID codebases.ID) (*remote.Remote, error) {
	rep, err := svc.repo.GetByCodebaseID(ctx, codebaseID)
	if err != nil {
		return nil, err
	}
	return rep, nil
}

type SetRemoteInput struct {
	Name              string
	URL               string
	BasicAuthUsername string
	BasicAuthPassword string
	TrackedBranch     string
	BrowserLinkRepo   string
	BrowserLinkBranch string
}

func (svc *EnterpriseService) SetRemote(ctx context.Context, codebaseID codebases.ID, input *SetRemoteInput) (*remote.Remote, error) {
	// update existing if exists
	rep, err := svc.repo.GetByCodebaseID(ctx, codebaseID)
	switch {
	case err == nil:
		// update
		rep.Name = input.Name
		rep.URL = input.URL
		rep.BasicAuthUsername = input.BasicAuthUsername
		rep.BasicAuthPassword = input.BasicAuthPassword
		rep.TrackedBranch = input.TrackedBranch
		rep.BrowserLinkRepo = input.BrowserLinkRepo
		rep.BrowserLinkBranch = input.BrowserLinkBranch
		if err := svc.repo.Update(ctx, rep); err != nil {
			return nil, fmt.Errorf("failed to update remote: %w", err)
		}

		svc.analyticsService.Capture(ctx, "updated remote integration", analytics.CodebaseID(codebaseID), analytics.Property("remote_name", rep.Name))

		return rep, nil
	case errors.Is(err, sql.ErrNoRows):
		// create
		r := remote.Remote{
			ID:                uuid.NewString(),
			CodebaseID:        codebaseID,
			Name:              input.Name,
			URL:               input.URL,
			BasicAuthUsername: input.BasicAuthUsername,
			BasicAuthPassword: input.BasicAuthPassword,
			TrackedBranch:     input.TrackedBranch,
			BrowserLinkRepo:   input.BrowserLinkRepo,
			BrowserLinkBranch: input.BrowserLinkBranch,
		}

		if err := svc.repo.Create(ctx, r); err != nil {
			return nil, fmt.Errorf("failed to add remote: %w", err)
		}

		svc.analyticsService.Capture(ctx, "created remote integration", analytics.CodebaseID(codebaseID), analytics.Property("remote_name", r.Name))

		return &r, nil
	default:
		return nil, fmt.Errorf("failed to set remote: %w", err)
	}
}

func (svc *EnterpriseService) Push(ctx context.Context, user *users.User, ws *workspaces.Workspace) error {
	rem, err := svc.repo.GetByCodebaseID(ctx, ws.CodebaseID)
	if err != nil {
		return fmt.Errorf("could not get remote: %w", err)
	}

	localBranchName := "sturdy-" + ws.ID
	gitCommitMessage := message.CommitMessage(ws.DraftDescription)

	_, err = svc.PrepareBranchForPush(ctx, localBranchName, ws, gitCommitMessage, user.Name, user.Email)
	if err != nil {
		return err
	}

	refspec := fmt.Sprintf("+refs/heads/%s:refs/heads/sturdy-%s", localBranchName, ws.ID)

	push := func(repo vcs.RepoGitWriter) error {
		_, err := repo.PushRemoteUrlWithRefspec(
			svc.logger,
			rem.URL,
			newCredentialsCallback(rem.BasicAuthPassword, rem.BasicAuthPassword),
			[]string{refspec},
		)
		if err != nil {
			return fmt.Errorf("failed to push: %w", err)
		}
		return nil
	}

	if err := svc.executorProvider.New().GitWrite(push).ExecTrunk(ws.CodebaseID, "pushRemote"); err != nil {
		return fmt.Errorf("failed to push workspace to remote: %w", err)
	}

	svc.analyticsService.Capture(ctx, "pushed workspace to remote", analytics.CodebaseID(ws.CodebaseID), analytics.Property("workspace_id", ws.ID))

	return nil
}

func (svc *EnterpriseService) PushTrunk(ctx context.Context, codebaseID codebases.ID) error {
	rem, err := svc.repo.GetByCodebaseID(ctx, codebaseID)
	if err != nil {
		return fmt.Errorf("could not get remote: %w", err)
	}

	refspec := fmt.Sprintf("refs/heads/sturdytrunk:refs/heads/%s", rem.TrackedBranch)

	push := func(repo vcs.RepoGitWriter) error {
		_, err := repo.PushRemoteUrlWithRefspec(
			svc.logger,
			rem.URL,
			newCredentialsCallback(rem.BasicAuthPassword, rem.BasicAuthPassword),
			[]string{refspec},
		)
		if err != nil {
			return fmt.Errorf("failed to push: %w", err)
		}
		return nil
	}

	if err := svc.executorProvider.New().GitWrite(push).ExecTrunk(codebaseID, "pushTrunkRemote"); err != nil {
		return fmt.Errorf("failed to push trunk to remote: %w", err)
	}

	svc.analyticsService.Capture(ctx, "pushed trunk to remote", analytics.CodebaseID(codebaseID))

	return nil
}

func (svc *EnterpriseService) Pull(ctx context.Context, codebaseID codebases.ID) error {
	rem, err := svc.repo.GetByCodebaseID(ctx, codebaseID)
	if err != nil {
		return fmt.Errorf("could not get remote: %w", err)
	}

	refspec := fmt.Sprintf("+refs/heads/%s:refs/heads/sturdytrunk", rem.TrackedBranch)

	pull := func(repo vcs.RepoGitWriter) error {
		err := repo.FetchUrlRemoteWithCreds(
			rem.URL,
			newCredentialsCallback(rem.BasicAuthPassword, rem.BasicAuthPassword),
			[]string{refspec},
		)
		if err != nil {
			return fmt.Errorf("failed to pull: %w", err)
		}
		return nil
	}

	if err := svc.executorProvider.New().GitWrite(pull).ExecTrunk(codebaseID, "pullRemote"); err != nil {
		return fmt.Errorf("failed to pull: %w", err)
	}

	svc.analyticsService.Capture(ctx, "pulled trunk from remote", analytics.CodebaseID(codebaseID))

	if err := svc.changeService.UnsetHeadChangeCache(codebaseID); err != nil {
		return fmt.Errorf("failed to unset head: %w", err)
	}

	// Allow all workspaces to be rebased/synced on the latest head
	if err := svc.workspaceWriter.UnsetUpToDateWithTrunkForAllInCodebase(codebaseID); err != nil {
		return fmt.Errorf("failed to unset up to date with trunk for all in codebase: %w", err)
	}

	return nil
}

func newCredentialsCallback(username, password string) git.CredentialsCallback {
	return func(url string, usernameFromUrl string, allowedTypes git.CredentialType) (*git.Credential, error) {
		cred, _ := git.NewCredentialUserpassPlaintext(username, password)
		return cred, nil
	}
}

func (svc *EnterpriseService) PrepareBranchForPush(ctx context.Context, prBranchName string, ws *workspaces.Workspace, commitMessage, userName, userEmail string) (commitSha string, err error) {
	if ws.ViewID == nil && ws.LatestSnapshotID != nil {
		commitSha, err = svc.prepareBranchForPullRequestFromSnapshot(ctx, prBranchName, ws, commitMessage, userName, userEmail)
		if err != nil {
			return "", fmt.Errorf("failed to prepare branch from snapshot: %w", err)
		}
		return
	} else if ws.ViewID != nil {
		commitSha, err = svc.prepareBranchForPullRequestWithView(prBranchName, ws, commitMessage, userName, userEmail)
		if err != nil {
			return "", fmt.Errorf("failed to prepare branch from snapshot: %w", err)
		}
		return
	} else {
		return "", errors.New("workspace does not have either view nor snapshot")
	}

}

func (svc *EnterpriseService) prepareBranchForPullRequestFromSnapshot(ctx context.Context, prBranchName string, ws *workspaces.Workspace, commitMessage, userName, userEmail string) (string, error) {
	signature := git.Signature{
		Name:  userName,
		Email: userEmail,
		When:  time.Now(),
	}

	snapshot, err := svc.snap.GetByID(ctx, *ws.LatestSnapshotID)
	if err != nil {
		return "", fmt.Errorf("failed to get snapshot: %w", err)
	}

	var resSha string

	exec := svc.executorProvider.New().GitWrite(func(r vcs.RepoGitWriter) error {
		sha, err := r.CreateNewCommitBasedOnCommit(prBranchName, snapshot.CommitID, signature, commitMessage)
		if err != nil {
			return err
		}

		resSha = sha
		return nil
	})

	if err := exec.ExecTrunk(ws.CodebaseID, "prepareBranchForPullRequestFromSnapshot"); err != nil {
		return "", fmt.Errorf("failed to create pr branch from snapshot")
	}

	return resSha, nil
}

func (svc *EnterpriseService) prepareBranchForPullRequestWithView(prBranchName string, ws *workspaces.Workspace, commitMessage, userName, userEmail string) (string, error) {
	signature := git.Signature{
		Name:  userName,
		Email: userEmail,
		When:  time.Now(),
	}

	var resSha string

	exec := svc.executorProvider.New().FileReadGitWrite(func(r vcs.RepoReaderGitWriter) error {
		treeID, err := vcs_change.CreateChangesTreeFromPatches(svc.logger, r, ws.CodebaseID, nil)
		if err != nil {
			return err
		}

		// No changes where added
		if treeID == nil {
			return fmt.Errorf("no changes to add")
		}

		if err := r.CreateNewBranchOnHEAD(prBranchName); err != nil {
			return fmt.Errorf("failed to create pr branch: %w", err)
		}

		sha, err := r.CommitIndexTreeWithReference(treeID, commitMessage, signature, "refs/heads/"+prBranchName)
		if err != nil {
			return fmt.Errorf("failed save change: %w", err)
		}

		if err := r.ForcePush(svc.logger, prBranchName); err != nil {
			return fmt.Errorf("failed to push to sturdytrunk: %w", err)
		}

		resSha = sha
		return nil
	})

	if err := exec.ExecView(ws.CodebaseID, *ws.ViewID, "prepareBranchForPullRequestWithView"); err != nil {
		return "", fmt.Errorf("failed to create pr branch from view: %w", err)
	}

	return resSha, nil
}