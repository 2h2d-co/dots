package dots

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func ensureNothingToPull(repoPath, operation string) error {
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "operation"
	}

	repository, err := git.PlainOpen(repoPath)
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			return nil
		}
		return fmt.Errorf("open git repo before %s: %w", operation, err)
	}

	head, err := repository.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil
		}
		return fmt.Errorf("read git HEAD before %s: %w", operation, err)
	}
	if !head.Name().IsBranch() {
		return nil
	}

	cfg, err := repository.Config()
	if err != nil {
		return fmt.Errorf("read git config before %s: %w", operation, err)
	}
	branch := cfg.Branches[head.Name().Short()]
	if branch == nil || branch.Remote == "" || branch.Merge == "" {
		return nil
	}

	remote, err := repository.Remote(branch.Remote)
	if err != nil {
		return fmt.Errorf("read git remote %s before %s: %w", branch.Remote, operation, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	remoteRefs, err := remote.ListContext(ctx, &git.ListOptions{Timeout: 30})
	if err != nil {
		return fmt.Errorf("verify remote freshness before %s: %w", operation, err)
	}

	remoteRef := findRemoteRef(remoteRefs, branch.Merge)
	if remoteRef == nil {
		return fmt.Errorf("verify remote freshness before %s: remote ref %s not found", operation, branch.Merge)
	}
	remoteHash := remoteRef.Hash()
	if remoteHash == head.Hash() {
		return nil
	}

	upstreamName := plumbing.NewRemoteReferenceName(branch.Remote, branch.Merge.Short())
	upstreamRef, err := repository.Reference(upstreamName, true)
	if err != nil {
		return fmt.Errorf("remote has changes that are not reflected locally; pull before %s", operation)
	}
	if remoteHash != upstreamRef.Hash() {
		return fmt.Errorf("remote has changes that are not reflected locally; pull before %s", operation)
	}

	headCommit, err := repository.CommitObject(head.Hash())
	if err != nil {
		return fmt.Errorf("read git HEAD commit before %s: %w", operation, err)
	}
	upstreamCommit, err := repository.CommitObject(upstreamRef.Hash())
	if err != nil {
		return fmt.Errorf("read upstream git commit before %s: %w", operation, err)
	}
	ancestor, err := upstreamCommit.IsAncestor(headCommit)
	if err != nil {
		return fmt.Errorf("compare git upstream before %s: %w", operation, err)
	}
	if !ancestor {
		return fmt.Errorf("upstream has commits that are not in HEAD; pull before %s", operation)
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
