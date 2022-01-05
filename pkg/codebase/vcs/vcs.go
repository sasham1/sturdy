package vcs

import (
	"fmt"
	"mash/vcs"
	"mash/vcs/provider"
	"os"

	git "github.com/libgit2/git2go/v33"
)

func Create(trunkProvider provider.TrunkProvider, codebaseID string) error {
	path := trunkProvider.TrunkPath(codebaseID)
	repo, err := vcs.CreateBareRepoWithRootCommit(path)
	if err != nil {
		return fmt.Errorf("failed to create trunk: %w", err)
	}
	repo.Free()
	return nil
}

func Import(trunkProvider provider.TrunkProvider, codebaseID, gitURL string) error {
	repo, err := vcs.RemoteBareClone(gitURL, trunkProvider.TrunkPath(codebaseID))
	if err != nil {
		return fmt.Errorf("failed remote clone %s: %w", gitURL, err)
	}
	repo.Free()
	return nil
}

// If no limit is set, a default of 100 is used
func ListChanges(repo vcs.Repo, limit int) ([]*vcs.LogEntry, error) {
	if limit < 1 {
		limit = 100
	}
	changeLog, err := repo.LogHead(limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get changes: %w", err)
	}

	// TODO: Do we really need this?
	var filteredLog []*vcs.LogEntry
	for _, e := range changeLog {
		if e.RawCommitMessage == "Root Commit" {
			continue
		}
		filteredLog = append(filteredLog, e)
	}

	return filteredLog, nil
}

func CloneFromGithub(trunkProvider provider.TrunkProvider, codebaseID, repoOwner, repoName, accessToken string) error {
	barePath := trunkProvider.TrunkPath(codebaseID)
	if _, err := os.Open(barePath); err != nil && os.IsNotExist(err) {
		upstream := fmt.Sprintf("https://github.com/%s/%s.git", repoOwner, repoName)
		_, err := vcs.RemoteCloneWithCreds(
			upstream,
			barePath,
			newCredentialsCallback("x-access-token", accessToken),
			true,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func newCredentialsCallback(tokenUsername, token string) git.CredentialsCallback {
	return func(url string, username string, allowedTypes git.CredType) (*git.Cred, error) {
		cred, _ := git.NewCredUserpassPlaintext(tokenUsername, token)
		return cred, nil
	}
}
