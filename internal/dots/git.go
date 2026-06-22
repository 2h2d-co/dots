package dots

import (
	"context"
	"errors"
	"fmt"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func ensureNothingToPull(repoPath string) error {
	repository, err := git.PlainOpen(repoPath)
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			return nil
		}
		return fmt.Errorf("open git repo before reindex: %w", err)
	}

	head, err := repository.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil
		}
		return fmt.Errorf("read git HEAD before reindex: %w", err)
	}
	if !head.Name().IsBranch() {
		return nil
	}

	cfg, err := repository.Config()
	if err != nil {
		return fmt.Errorf("read git config before reindex: %w", err)
	}
	branch := cfg.Branches[head.Name().Short()]
	if branch == nil || branch.Remote == "" || branch.Merge == "" {
		return nil
	}

	remote, err := repository.Remote(branch.Remote)
	if err != nil {
		return fmt.Errorf("read git remote %s before reindex: %w", branch.Remote, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	remoteRefs, err := remote.ListContext(ctx, &git.ListOptions{Timeout: 30})
	if err != nil {
		return fmt.Errorf("verify remote freshness before reindex: %w", err)
	}

	remoteRef := findRemoteRef(remoteRefs, branch.Merge)
	if remoteRef == nil {
		return fmt.Errorf("verify remote freshness before reindex: remote ref %s not found", branch.Merge)
	}
	remoteHash := remoteRef.Hash()
	if remoteHash == head.Hash() {
		return nil
	}

	upstreamName := plumbing.NewRemoteReferenceName(branch.Remote, branch.Merge.Short())
	upstreamRef, err := repository.Reference(upstreamName, true)
	if err != nil {
		return errors.New("remote has changes that are not reflected locally; pull before reindex")
	}
	if remoteHash != upstreamRef.Hash() {
		return errors.New("remote has changes that are not reflected locally; pull before reindex")
	}

	headCommit, err := repository.CommitObject(head.Hash())
	if err != nil {
		return fmt.Errorf("read git HEAD commit before reindex: %w", err)
	}
	upstreamCommit, err := repository.CommitObject(upstreamRef.Hash())
	if err != nil {
		return fmt.Errorf("read upstream git commit before reindex: %w", err)
	}
	ancestor, err := upstreamCommit.IsAncestor(headCommit)
	if err != nil {
		return fmt.Errorf("compare git upstream before reindex: %w", err)
	}
	if !ancestor {
		return errors.New("upstream has commits that are not in HEAD; pull before reindex")
	}
	return nil
}

func findRemoteRef(refs []*plumbing.Reference, name plumbing.ReferenceName) *plumbing.Reference {
	for _, ref := range refs {
		if ref.Name() == name {
			return ref
		}
	}
	return nil
}
