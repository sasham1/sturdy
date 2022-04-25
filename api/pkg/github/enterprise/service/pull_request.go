package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	gh "github.com/google/go-github/v39/github"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"getsturdy.com/api/pkg/activity"
	"getsturdy.com/api/pkg/analytics"
	"getsturdy.com/api/pkg/auth"
	"getsturdy.com/api/pkg/changes/message"
	service_change "getsturdy.com/api/pkg/changes/service"
	"getsturdy.com/api/pkg/codebases"
	"getsturdy.com/api/pkg/events/v2"
	"getsturdy.com/api/pkg/github"
	"getsturdy.com/api/pkg/github/api"
	github_client "getsturdy.com/api/pkg/github/enterprise/client"
	vcs_github "getsturdy.com/api/pkg/github/enterprise/vcs"
	gqlerrors "getsturdy.com/api/pkg/graphql/errors"
	"getsturdy.com/api/pkg/users"
	"getsturdy.com/api/pkg/workspaces"
	db_workspaces "getsturdy.com/api/pkg/workspaces/db"
	"getsturdy.com/api/vcs"
)

var ErrNotFound = errors.New("not found")
var ErrIntegrationNotEnabled = errors.New("github integration is not enabled")

type GitHubUserError struct {
	msg string
}

func (g GitHubUserError) Error() string {
	return g.msg
}

func (svc *Service) setPRState(ctx context.Context, pr *github.PullRequest, state github.PullRequestState) error {
	pr.State = github.PullRequestStateMerged
	if err := svc.gitHubPullRequestRepo.Update(ctx, pr); err != nil {
		return fmt.Errorf("failed to update pull request: %w", err)
	}

	if err := svc.eventsPublisher.GitHubPRUpdated(ctx, events.Codebase(pr.CodebaseID), pr); err != nil {
		svc.logger.Error("failed to send codebase event",
			zap.String("workspace_id", pr.WorkspaceID),
			zap.String("github_pr_id", pr.ID),
			zap.Error(err),
		)
		// do not fail
	}
	return nil
}

func (svc *Service) MergePullRequest(ctx context.Context, ws *workspaces.Workspace) error {
	userID, err := auth.UserID(ctx)
	if err != nil {
		return err
	}

	existingGitHubUser, err := svc.gitHubUserRepo.GetByUserID(userID)
	if err != nil {
		return fmt.Errorf("failed to get github user: %w", err)
	}

	if existingGitHubUser.AccessToken == nil {
		return fmt.Errorf("no github access token found for user %s", userID)
	}

	prs, err := svc.gitHubPullRequestRepo.ListOpenedByWorkspace(ws.ID)
	if err != nil {
		return fmt.Errorf("pull request not found: %w", err)
	}
	if len(prs) != 1 {
		return fmt.Errorf("unexpected number of open pull requests found")
	}

	pr := prs[0]

	ghRepo, err := svc.gitHubRepositoryRepo.GetByCodebaseID(pr.CodebaseID)
	if err != nil {
		return fmt.Errorf("gh repo not found: %w", err)
	}

	ghInstallation, err := svc.gitHubInstallationRepo.GetByInstallationID(ghRepo.InstallationID)
	if err != nil {
		return fmt.Errorf("gh installation not found: %w", err)
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: *existingGitHubUser.AccessToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	userAuthClient := gh.NewClient(tc)

	mergeOpts := &gh.PullRequestOptions{
		CommitTitle: fmt.Sprintf("Merge pull request #%d - %s", pr.GitHubPRNumber, ws.NameOrFallback()),
		// TODO: Do we want to set this to rebase?
		// MergeMethod: "rebase",
	}

	commitMessage := message.CommitMessage(ws.DraftDescription) + "\n\nMerged via Sturdy"

	// update PR state to merging
	previousState := pr.State
	if err := svc.setPRState(ctx, pr, github.PullRequestStateMerging); err != nil {
		return fmt.Errorf("failed to update pull request state: %w", err)
	}

	// check if pr is already merged, without any api error checking
	// actual error will come when trying to merge pr, if needed
	if apiPR, _, err := userAuthClient.PullRequests.Get(ctx, ghInstallation.Owner, ghRepo.Name, pr.GitHubPRNumber); err == nil && apiPR.GetMerged() {
		return svc.UpdatePullRequestFromGitHub(ctx, ghRepo, pr, api.ConvertPullRequest(apiPR), *existingGitHubUser.AccessToken)
	}

	//nolint:contextcheck
	res, resp, err := userAuthClient.PullRequests.Merge(ctx, ghInstallation.Owner, ghRepo.Name, pr.GitHubPRNumber, commitMessage, mergeOpts)
	if err != nil {
		// rollback github pr state
		if err := svc.setPRState(ctx, pr, previousState); err != nil {
			return fmt.Errorf("failed to update pull request state: %w", err)
		}

		var errorResponse *gh.ErrorResponse
		if resp.StatusCode == http.StatusMethodNotAllowed && errors.As(err, &errorResponse) {
			// 405 not allowed
			// This happens if the repo is configured with branch protection rules (require approvals, tests to pass, etc).
			// Proxy GitHub's error message to the end user.
			//
			// Examples:
			// * "failed to merge pr: PUT https://api.github.com/repos/zegl/empty-11/pulls/4/merge: 405 At least 1 approving review is required by reviewers with write access. []"
			return GitHubUserError{errorResponse.Message}
		}

		return fmt.Errorf("failed to merge pr: %w", err)
	}

	if !res.GetMerged() {
		// rollback github pr state
		if err := svc.setPRState(ctx, pr, previousState); err != nil {
			return fmt.Errorf("failed to update pull request state: %w", err)
		}
		return fmt.Errorf("pull request was not merged")
	}

	apiPR, _, err := userAuthClient.PullRequests.Get(ctx, ghInstallation.Owner, ghRepo.Name, pr.GitHubPRNumber)
	if err != nil {
		return fmt.Errorf("failed to get pull request: %w", err)
	}

	return svc.UpdatePullRequestFromGitHub(ctx, ghRepo, pr, api.ConvertPullRequest(apiPR), *existingGitHubUser.AccessToken)
}

func getPRState(apiPR *api.PullRequest) github.PullRequestState {
	switch apiPR.GetState() {
	case "open":
		return github.PullRequestStateOpen
	case "closed":
		if apiPR.GetMerged() {
			return github.PullRequestStateMerged
		}
		return github.PullRequestStateClosed
	default:
		return github.PullRequestStateUnknown
	}
}

func (svc *Service) updatePRFromGitHub(ctx context.Context, pr *github.PullRequest, gitHubPR *api.PullRequest) error {
	now := time.Now()
	pr.UpdatedAt = &now
	pr.ClosedAt = gitHubPR.ClosedAt
	pr.MergedAt = gitHubPR.MergedAt
	pr.State = getPRState(gitHubPR)
	// make sure we send pr updated event after this function returns in any case
	if err := svc.gitHubPullRequestRepo.Update(ctx, pr); err != nil {
		svc.logger.Error("failed to update pull request", zap.Error(err))
	}

	if err := svc.eventsPublisher.GitHubPRUpdated(ctx, events.Codebase(pr.CodebaseID), pr); err != nil {
		svc.logger.Error("failed to send codebase event", zap.Error(err))
		// do not fail
	}
	return nil
}

func (svc *Service) UpdatePullRequestFromGitHub(
	ctx context.Context,
	repo *github.Repository,
	pr *github.PullRequest,
	gitHubPR *api.PullRequest,
	accessToken string,
) error {
	ws, err := svc.workspacesService.GetByID(ctx, pr.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		svc.logger.Warn("handled a github pull request webhook for non-existing workspace",
			zap.String("workspace_id", pr.WorkspaceID),
			zap.String("github_pr_id", pr.ID),
			zap.String("github_pr_link", gitHubPR.GetHTMLURL()),
		)
		return nil // noop
	} else if err != nil {
		return fmt.Errorf("failed to get workspace from db: %w", err)
	}

	// make context user context to connect all events to the same user
	ctx = auth.NewUserContext(ctx, ws.UserID)

	if shoudUpdateDescription := pr.Importing; shoudUpdateDescription {
		newDescription, err := DescriptionFromPullRequest(gitHubPR)
		if err != nil {
			return fmt.Errorf("failed to build description: %w", err)
		}
		ws.DraftDescription = newDescription
		if err := svc.workspaceWriter.UpdateFields(ctx, ws.ID, db_workspaces.SetDraftDescription(newDescription)); err != nil {
			return fmt.Errorf("failed to update workspace: %w", err)
		}
	}

	newState := getPRState(gitHubPR)

	if shouldUnarchive := newState == github.PullRequestStateOpen && pr.Importing; shouldUnarchive {
		// if pr is open and not importing, unarchive it
		if err := svc.workspacesService.Unarchive(ctx, ws); err != nil {
			return fmt.Errorf("failed to unarchive workspace: %w", err)
		}
	}

	if shouldArchive := newState == github.PullRequestStateClosed && pr.Importing; shouldArchive {
		// if pr is closed and importing, archive it
		if err := svc.updatePRFromGitHub(ctx, pr, gitHubPR); err != nil {
			return fmt.Errorf("failed to update pull request state: %w", err)
		}
		return svc.workspacesService.Archive(ctx, ws)
	} else if shouldUpdateState := newState != github.PullRequestStateMerged; shouldUpdateState {
		// if the PR is not merged, fetch the latest PR data from GitHub
		if err := svc.UpdatePullRequest(ctx, pr, gitHubPR, accessToken, ws); err != nil {
			return fmt.Errorf("failed to update pull request: %w", err)
		}
		if err := svc.updatePRFromGitHub(ctx, pr, gitHubPR); err != nil {
			return fmt.Errorf("failed to update pull request state: %w", err)
		}
		return nil
	} else {
		// merge pr
		if err := svc.mergePullRequest(ctx, repo, pr, gitHubPR, accessToken); err != nil {
			return fmt.Errorf("failed to merge pull request: %w", err)
		}
		if err := svc.updatePRFromGitHub(ctx, pr, gitHubPR); err != nil {
			return fmt.Errorf("failed to update pull request state: %w", err)
		}
		return nil
	}
}

func (svc *Service) mergePullRequest(
	ctx context.Context,
	repo *github.Repository,
	pr *github.PullRequest,
	gitHubPR *api.PullRequest,
	accessToken string,
) error {
	if pr.State == github.PullRequestStateMerging {
		// another goroutine is already merging this PR
		return nil
	}

	ws, err := svc.workspacesService.GetByID(ctx, pr.WorkspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	if alreadyMerged := pr.State == github.PullRequestStateMerged && ws.ChangeID != nil; alreadyMerged {
		// this is a noop, pr is already merged
		return nil
	}

	// pull from github if sturdy doesn't have the commits
	if err := svc.pullFromGitHubIfCommitNotExists(pr.CodebaseID, []string{
		gitHubPR.GetMergeCommitSHA(),
		gitHubPR.GetBase().GetSHA(),
	}, accessToken, repo.TrackedBranch); err != nil {
		return fmt.Errorf("failed to pullFromGitHubIfCommitNotExists: %w", err)
	}

	ch, err := svc.changeService.CreateWithCommitAsParent(ctx, ws, gitHubPR.GetMergeCommitSHA(), gitHubPR.GetBase().GetSHA())
	if errors.Is(err, service_change.ErrAlreadyExists) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to create change: %w", err)
	}

	svc.analyticsService.Capture(ctx, "pull request merged",
		analytics.DistinctID(ws.UserID.String()),
		analytics.CodebaseID(ws.CodebaseID),
		analytics.Property("workspace_id", ws.ID),
		analytics.Property("github", true),
		analytics.Property("importing", pr.Importing),
	)

	// send change to ci
	if err := svc.buildQueue.EnqueueChange(ctx, ch); err != nil {
		svc.logger.Error("failed to enqueue change", zap.Error(err))
		// do not fail
	}

	// all workspaces that were up to date with trunk are not up to data anymore
	if err := svc.workspaceWriter.UnsetUpToDateWithTrunkForAllInCodebase(ws.CodebaseID); err != nil {
		return fmt.Errorf("failed to unset up to date with trunk for all in codebase: %w", err)
	}

	// Create workspace activity that it has created a change
	if err := svc.activitySender.Codebase(ctx, ws.CodebaseID, ws.ID, ws.UserID, activity.TypeCreatedChange, string(ch.ID)); err != nil {
		return fmt.Errorf("failed to create workspace activity: %w", err)
	}

	// copy all workspace activities to change activities
	if err := svc.activityService.SetChange(ctx, ws.ID, ch.ID); err != nil {
		return fmt.Errorf("failed to set change: %w", err)
	}

	if err := svc.commentsService.MoveCommentsFromWorkspaceToChange(ctx, ws.ID, ch.ID); err != nil {
		return fmt.Errorf("failed to migrate comments: %w", err)
	}

	// Archive the workspace
	if err := svc.workspacesService.ArchiveWithChange(ctx, ws, ch); err != nil {
		return fmt.Errorf("failed to archive workspace: %w", err)
	}

	return nil
}

func (svc *Service) pullFromGitHubIfCommitNotExists(codebaseID codebases.ID, commitShas []string, accessToken, trackedBranchName string) error {
	shouldPull := false

	if err := svc.executorProvider.New().
		GitRead(func(repo vcs.RepoGitReader) error {
			for _, sha := range commitShas {
				if _, err := repo.GetCommitDetails(sha); err != nil {
					shouldPull = true
				}
			}
			return nil
		}).
		ExecTrunk(codebaseID, "pullFromGitHubIfCommitNotExists.Check"); err != nil {
		return fmt.Errorf("failed to fetch changes from github: %w", err)
	}

	if !shouldPull {
		return nil
	}

	if err := svc.executorProvider.New().
		GitWrite(vcs_github.FetchTrackedToSturdytrunk(accessToken, "refs/heads/"+trackedBranchName)).
		ExecTrunk(codebaseID, "pullFromGitHubIfCommitNotExists.Pull"); err != nil {
		return fmt.Errorf("failed to fetch changes from github: %w", err)
	}

	return nil
}

func (svc *Service) GetPullRequestForWorkspace(workspaceID string) (*github.PullRequest, error) {
	prs, err := svc.gitHubPullRequestRepo.ListByWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	return primaryPullRequest(prs)
}

func primaryPullRequest(prs []*github.PullRequest) (*github.PullRequest, error) {
	// Priority:
	// * Any open PR
	// * Forks over non-forks
	// * Created At

	// newest first
	sort.SliceStable(prs, func(i, j int) bool {
		a, b := prs[i], prs[j]

		// prefer open prs
		if a.State == github.PullRequestStateOpen && b.State != github.PullRequestStateOpen {
			return true
		}
		if a.State != github.PullRequestStateOpen && b.State == github.PullRequestStateOpen {
			return false
		}

		// prefer non-forks
		if !a.Fork && b.Fork {
			return true
		}
		if a.Fork && !b.Fork {
			return false
		}

		// prefer most recently created
		return prs[i].CreatedAt.After(prs[j].CreatedAt)
	})

	if len(prs) > 0 {
		return prs[0], nil
	}

	return nil, ErrNotFound
}

func (svc *Service) CreateOrUpdatePullRequest(ctx context.Context, user *users.User, ws *workspaces.Workspace) (*github.PullRequest, error) {
	ghRepo, err := svc.gitHubRepositoryRepo.GetByCodebaseID(ws.CodebaseID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrIntegrationNotEnabled
	case err != nil:
		return nil, err
	}

	// Pull Requests can only be made if the integration is enabled and GitHub is considered to be the source of truth
	if !ghRepo.IntegrationEnabled || !ghRepo.GitHubSourceOfTruth {
		return nil, ErrIntegrationNotEnabled
	}

	ghInstallation, err := svc.gitHubInstallationRepo.GetByInstallationID(ghRepo.InstallationID)
	if err != nil {
		return nil, err
	}

	ghUser, err := svc.gitHubUserRepo.GetByUserID(user.ID)
	if err != nil {
		return nil, err
	}

	if ghUser.AccessToken == nil {
		return nil, fmt.Errorf("gitub user has no access token")
	}

	cb, err := svc.codebaseRepo.Get(ws.CodebaseID)
	if err != nil {
		return nil, err
	}

	logger := svc.logger.With(
		zap.Stringer("codebase_id", cb.ID),
		zap.Int64("github_installation_id", ghInstallation.InstallationID),
		zap.String("workspace_id", ws.ID),
		zap.Stringer("user_id", user.ID),
	)

	prBranch := "sturdy-pr-" + ws.ID
	remoteBranchName := prBranch
	updateExistingPR := false

	existingPR, err := svc.GetPullRequestForWorkspace(ws.ID)
	switch {
	case errors.Is(err, ErrNotFound):
	// do nothing
	case err != nil:
		return nil, fmt.Errorf("unable to get existing pr for workspace: %w", err)

	// found open pr
	case existingPR.State == github.PullRequestStateOpen:
		if existingPR.Head != "" {
			remoteBranchName = existingPR.Head
		}
		// create new pr if the imported one is from a fork
		updateExistingPR = !existingPR.Fork
	}

	gitCommitMessage := message.CommitMessage(ws.DraftDescription)

	prSHA, err := svc.remoteService.PrepareBranchForPush(ctx, prBranch, ws, gitCommitMessage, user.Name, user.Email)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare branch: %w", err)
	}

	accessToken, err := github_client.GetAccessToken(ctx, logger, svc.gitHubAppConfig, ghInstallation, ghRepo.GitHubRepositoryID, svc.gitHubRepositoryRepo, svc.gitHubInstallationClientProvider)
	if err != nil {
		return nil, err
	}

	t := time.Now()

	// GitHub Repository might have been modified at this point, refresh it
	ghRepo, err = svc.gitHubRepositoryRepo.GetByID(ghRepo.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to re-load ghRepo: %w", err)
	}

	// GitHub automatically promotes the first branch pushed to a repository to be the HEAD branch
	// If the repository is _empty_ there is a risk that the branch pushed for the PR is the first branch pushed to GH
	// If this is the case, first push the sturdytrunk to be the new "master"/"main".
	// This is done _without_ force, to not screw anything up if we're in the wrong.
	if err := vcs_github.HaveTrackedBranch(svc.executorProvider, ws.CodebaseID, ghRepo.TrackedBranch); err != nil {
		logger.Info("pushing sturdytrunk to github")
		userVisibleError, pushTrunkErr := vcs_github.PushBranchToGithubSafely(svc.executorProvider, ws.CodebaseID, "sturdytrunk", ghRepo.TrackedBranch, accessToken)
		if pushTrunkErr != nil {
			logger.Error("failed to push trunk to github (github is source of truth)", zap.Error(pushTrunkErr))

			// save that the push failed
			ghRepo.LastPushAt = &t
			ghRepo.LastPushErrorMessage = &userVisibleError
			if err := svc.gitHubRepositoryRepo.Update(ghRepo); err != nil {
				logger.Error("failed to update status of github integration", zap.Error(err))
			}

			return nil, gqlerrors.Error(pushTrunkErr, "pushFailure", userVisibleError)
		}
	} else {
		logger.Info("github have a default branch, not pushing sturdytrunk")
	}

	userVisibleError, pushErr := vcs_github.PushBranchToGithubWithForce(svc.executorProvider, ws.CodebaseID, prBranch, remoteBranchName, *ghUser.AccessToken)
	if pushErr != nil {
		logger.Error("failed to push to github (github is source of truth)", zap.Error(pushErr))

		// save that the push failed
		ghRepo.LastPushAt = &t
		ghRepo.LastPushErrorMessage = &userVisibleError
		if err := svc.gitHubRepositoryRepo.Update(ghRepo); err != nil {
			logger.Error("failed to update status of github integration", zap.Error(err))
		}

		return nil, gqlerrors.Error(pushErr, "pushFailure", userVisibleError)
	}

	// Mark as successfully pushed
	ghRepo.LastPushAt = &t
	ghRepo.LastPushErrorMessage = nil
	if err := svc.gitHubRepositoryRepo.Update(ghRepo); err != nil {
		logger.Error("failed to update status of github integration", zap.Error(err))
	}

	pullRequestDescription := prDescription(user.Name, ghUser.Username, cb, ws)

	// GitHub Client to be used on behalf of this user
	// TODO: Fallback to make these requests with the tokenClient if the users Access Token is invalid? (or they don't have one?)
	personalClient, err := svc.gitHubPersonalClientProvider(*ghUser.AccessToken)
	if err != nil {
		return nil, err
	}

	// GitHub client to be used on behalf of the app
	tokenClient, _, err := svc.gitHubInstallationClientProvider(
		svc.gitHubAppConfig,
		ghRepo.InstallationID,
	)
	if err != nil {
		return nil, err
	}

	pullRequestTitle := ws.NameOrFallback()

	// create a new pull request
	if !updateExistingPR {
		// using the personal client to create the PR behalf of the user
		apiPR, _, err := personalClient.PullRequests.Create(ctx, ghInstallation.Owner, ghRepo.Name, &gh.NewPullRequest{
			Title: &pullRequestTitle,
			Head:  &prBranch,
			Base:  &ghRepo.TrackedBranch,
			Body:  pullRequestDescription,
		})
		if err != nil {
			return nil, gqlerrors.Error(err, "createPullRequestFailure", "Failed to create a GitHub Pull Request")
		}
		pr := github.PullRequest{
			ID:                 uuid.NewString(),
			WorkspaceID:        ws.ID,
			GitHubID:           apiPR.GetID(),
			GitHubRepositoryID: ghRepo.GitHubRepositoryID,
			CreatedBy:          user.ID,
			GitHubPRNumber:     apiPR.GetNumber(),
			Head:               prBranch,
			HeadSHA:            &prSHA,
			CodebaseID:         ghRepo.CodebaseID,
			Base:               ghRepo.TrackedBranch,
			State:              github.PullRequestStateOpen,
			CreatedAt:          time.Now(),
		}
		if err := svc.gitHubPullRequestRepo.Create(pr); err != nil {
			return nil, err
		}

		svc.analyticsService.Capture(ctx, "created pull request",
			analytics.CodebaseID(ws.CodebaseID),
			analytics.Property("github", true),
		)

		return &pr, nil
	}

	// update an existing pull request
	apiPR, _, err := tokenClient.PullRequests.Get(ctx, ghInstallation.Owner, ghRepo.Name, existingPR.GitHubPRNumber)
	if err != nil {
		return nil, gqlerrors.Error(err, "getPullRequestFailure", fmt.Sprintf("Failed to get Pull Request #%d from GitHub", existingPR.GitHubPRNumber))
	}
	apiPR.Title = &pullRequestTitle
	apiPR.Body = pullRequestDescription

	// on behalf of the user
	_, _, err = personalClient.PullRequests.Edit(ctx, ghInstallation.Owner, ghRepo.Name, existingPR.GitHubPRNumber, apiPR)
	if err != nil {
		return nil, gqlerrors.Error(err, "updatePullRequestFailure", fmt.Sprintf("Failed to update Pull Request #%d on GitHub", existingPR.GitHubPRNumber))
	}

	t0 := time.Now()
	existingPR.UpdatedAt = &t0
	existingPR.HeadSHA = &prSHA
	existingPR.Importing = false // stop importing changes
	if err := svc.gitHubPullRequestRepo.Update(ctx, existingPR); err != nil {
		return nil, err
	}
	svc.analyticsService.Capture(ctx, "updated pull request",
		analytics.CodebaseID(ws.CodebaseID),
		analytics.Property("github", true),
	)

	return existingPR, nil
}

// GitHub support (some) HTML the Pull Request descriptions, so we don't need to clean that up here.
func prDescription(userName, userGitHubLogin string, cb *codebases.Codebase, ws *workspaces.Workspace) *string {
	var builder strings.Builder
	builder.WriteString(ws.DraftDescription)
	builder.WriteString("\n\n---\n\n")

	workspaceUrl := fmt.Sprintf("https://getsturdy.com/%s/%s", cb.GenerateSlug(), ws.ID)
	builder.WriteString(fmt.Sprintf("This PR was created by %s (%s) on [Sturdy](%s).\n\n", userName, userGitHubLogin, workspaceUrl))
	builder.WriteString("Update this PR by making changes through Sturdy.\n")

	res := builder.String()
	return &res
}
