package tui

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func readGitMeta(dir string) gitMeta {
	root := gitOutput(dir, "rev-parse", "--show-toplevel")
	if root == "" {
		return gitMeta{}
	}
	gitDir := gitOutput(dir, "rev-parse", "--git-dir")
	commonDir := gitOutput(dir, "rev-parse", "--git-common-dir")
	branch := gitOutput(dir, "branch", "--show-current")
	if branch == "" {
		branch = gitOutput(dir, "rev-parse", "--short", "HEAD")
	}
	status := gitOutput(dir, "status", "--porcelain")
	shortstat := gitOutput(dir, "diff", "--shortstat")
	return gitMeta{
		Repo:      true,
		Worktree:  isLinkedWorktree(gitDir, commonDir),
		Branch:    branch,
		Dirty:     strings.TrimSpace(status) != "",
		Untracked: hasGitUntracked(status),
		Shortstat: compactShortstat(shortstat),
	}
}

func isLinkedWorktree(gitDir string, commonDir string) bool {
	gitDir = filepath.Clean(strings.TrimSpace(gitDir))
	commonDir = filepath.Clean(strings.TrimSpace(commonDir))
	if gitDir == "" || commonDir == "" {
		return false
	}
	return gitDir != commonDir
}

func gitOutput(dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func compactShortstat(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, " files changed", " files")
	text = strings.ReplaceAll(text, " file changed", " file")
	text = strings.ReplaceAll(text, " insertions(+)", "+")
	text = strings.ReplaceAll(text, " insertion(+)", "+")
	text = strings.ReplaceAll(text, " deletions(-)", "-")
	text = strings.ReplaceAll(text, " deletion(-)", "-")
	text = strings.ReplaceAll(text, ",", "")
	return strings.Join(strings.Fields(text), " ")
}

func hasGitUntracked(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "??") {
			return true
		}
	}
	return false
}
