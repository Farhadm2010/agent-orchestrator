package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/forkseed"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// forkSeedApplier writes a fork source's captured files into the fork.
type forkSeedApplier interface {
	Apply(ctx context.Context, worktree, ref string) error
	Delete(ctx context.Context, workspace, ref string) error
	// RecordBase, UnchangedSince and DiscardChanges let cleanup tell a fork's
	// inherited uncommitted changes from its own.
	RecordBase(ctx context.Context, worktree, ref string) error
	UnchangedSince(ctx context.Context, worktree, ref string) (bool, error)
	DiscardChanges(ctx context.Context, worktree string) error
	// RestoreBase writes a fork's starting files back into a recreated
	// worktree that is still at the fork's start commit.
	RestoreBase(ctx context.Context, worktree, ref string) (bool, error)
}

type gitForkSeeds struct{}

func (gitForkSeeds) Apply(ctx context.Context, worktree, ref string) error {
	return forkseed.Apply(ctx, worktree, ref)
}

func (gitForkSeeds) Delete(ctx context.Context, workspace, ref string) error {
	return forkseed.Delete(ctx, workspace, ref)
}

func (gitForkSeeds) RecordBase(ctx context.Context, worktree, ref string) error {
	return forkseed.RecordBase(ctx, worktree, ref)
}

func (gitForkSeeds) UnchangedSince(ctx context.Context, worktree, ref string) (bool, error) {
	return forkseed.UnchangedSince(ctx, worktree, ref)
}

func (gitForkSeeds) RestoreBase(ctx context.Context, worktree, ref string) (bool, error) {
	return forkseed.RestoreBase(ctx, worktree, ref)
}

func (gitForkSeeds) DiscardChanges(ctx context.Context, worktree string) error {
	return forkseed.DiscardChanges(ctx, worktree)
}

// createForkWorkspace creates the fork's branch at the source's HEAD, then
// copies in the source's uncommitted and untracked files. A worktree that was
// created is returned with any error, so the caller rolls it back.
func (m *Manager) createForkWorkspace(ctx context.Context, fork *ports.SpawnFork, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, *ports.WorkspaceProjectInfo, error) {
	if fork.StartPoint == "" {
		return ports.WorkspaceInfo{}, nil, errors.New("session fork: missing start point")
	}
	cfg.FreshBranch = true
	cfg.StartPoint = fork.StartPoint
	ws, err := m.workspace.Create(ctx, cfg)
	if err != nil || fork.SeedRef == "" {
		return ws, nil, err
	}
	applyErr := m.forkSeeds.Apply(ctx, ws.Path, fork.SeedRef)
	if !fork.KeepSeedRef {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		// The ref is only needed to copy the files; the reaper removes it if
		// this fails, since a seed ref with no live fork is a leftover.
		_ = m.forkSeeds.Delete(cleanupCtx, ws.Path, fork.SeedRef)
	}
	if applyErr != nil {
		return ws, nil, fmt.Errorf("session fork: copy source files: %w", applyErr)
	}
	m.markForkWorkspace(ws.Path)
	// Record what the fork starts with, so archiving an unchanged fork can
	// clean up its worktree instead of keeping the inherited changes.
	if err := m.forkSeeds.RecordBase(ctx, ws.Path, forkseed.BaseRef(string(cfg.SessionID))); err != nil {
		m.logger.Warn("session fork: record the fork's starting files", "workspace", ws.Path, "err", err)
	}
	if err := m.restoreAttachments(ctx, cfg.SessionID, ws); err != nil {
		m.logger.Warn("session fork: place attachments in the fork", "workspace", ws.Path, "err", err)
	}
	if fork.SourceWorkspace != "" {
		cloned, err := cloneForkDependencies(ctx, fork.SourceWorkspace, ws.Path)
		if err != nil && !errors.Is(err, errCloneUnsupported) {
			m.logger.Warn("session fork: clone dependencies; setup will install them", "workspace", ws.Path, "err", err)
		} else if len(cloned) > 0 {
			m.logger.Info("session fork: cloned dependencies", "workspace", ws.Path, "folders", cloned)
		}
	}
	return ws, nil, nil
}

// copyForkArtifacts copies the source's artifact files into the fork's own
// folder, so the fork's links and edits never touch the original's. The
// version store is not copied: the fork's history of its artifacts starts
// fresh. A failure only loses artifacts, so it is logged rather than failing
// the fork.
func (m *Manager) copyForkArtifacts(source domain.SessionID, dst string) {
	if source == "" {
		return
	}
	if err := copyTree(m.artifactDir(source), dst, sessionartifacts.VersionsDir); err != nil {
		m.logger.Warn("session fork: copy artifacts", "source", source, "err", err)
	}
}

type artifactVersionLister interface {
	ListSessionArtifactVersions(ctx context.Context, id domain.SessionID, path string) ([]domain.SessionArtifactVersion, error)
}

// copyForkArtifactsAsOf copies the source's artifacts as they were at asOf,
// for a fork of an older turn. Each file takes its newest saved version
// modified by then; a file first written later is left out. A file with no
// saved versions goes by its modification time.
func (m *Manager) copyForkArtifactsAsOf(source domain.SessionID, dst string, asOf time.Time) {
	src := m.artifactDir(source)
	lister, _ := m.store.(artifactVersionLister)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel == sessionartifacts.VersionsDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		target := filepath.Join(dst, rel)
		var versions []domain.SessionArtifactVersion
		if lister != nil {
			if versions, err = lister.ListSessionArtifactVersions(ctx, source, filepath.ToSlash(rel)); err != nil {
				return err
			}
		}
		if len(versions) == 0 {
			info, err := entry.Info()
			if err != nil || info.ModTime().After(asOf) {
				return err
			}
			return copyRegularFileMkdir(path, target)
		}
		var chosen *domain.SessionArtifactVersion
		for i := range versions {
			if !versions[i].ModTime.After(asOf) && (chosen == nil || versions[i].Version > chosen.Version) {
				chosen = &versions[i]
			}
		}
		if chosen == nil {
			return nil // first written after that turn
		}
		blob := filepath.Join(src, sessionartifacts.VersionsDir, "blobs", chosen.SHA256[:2], chosen.SHA256)
		return copyRegularFileMkdir(blob, target)
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		m.logger.Warn("session fork: copy artifacts as of a turn", "source", source, "err", err)
	}
}

func copyRegularFileMkdir(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	return copyRegularFile(from, to)
}

// copyForkAttachments copies the files attached to the source's messages into
// the fork's own store; they are placed in the fork's worktree once it exists.
func (m *Manager) copyForkAttachments(source, fork domain.SessionID) {
	if source == "" || m.dataDir == "" {
		return
	}
	root := filepath.Join(m.dataDir, "attachments")
	if err := copyTree(filepath.Join(root, string(source)), filepath.Join(root, string(fork)), ""); err != nil {
		m.logger.Warn("session fork: copy attachments", "source", source, "err", err)
	}
}

// copyTree copies regular files and folders, skipping symlinks, special
// files, and a top-level folder named skip. A missing src is not an error.
func copyTree(src, dst, skip string) error {
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if skip != "" && entry.IsDir() && rel == skip {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, 0o700)
		case entry.Type().IsRegular():
			return copyRegularFile(path, target)
		default:
			return nil
		}
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

type forkLinkRewriter interface {
	ConversationForSession(ctx context.Context, id domain.SessionID) (domain.ConversationRecord, error)
	RewriteConversationText(ctx context.Context, conversationID, from, to string) (int, error)
	CopyForkMessageContent(ctx context.Context, sourceConversationID, forkConversationID string) (int, error)
}

// pointForkLinksAtFork fixes the fork's imported history: the original's
// artifact links point at the fork's own copies, and pasted images come back
// from the original's messages. Links to the original session itself are left
// alone.
func (m *Manager) pointForkLinksAtFork(ctx context.Context, project domain.ProjectID, source, fork domain.SessionID) {
	rewriter, ok := m.store.(forkLinkRewriter)
	if !ok || source == "" {
		return
	}
	conversation, err := rewriter.ConversationForSession(ctx, fork)
	if err != nil {
		m.logger.Warn("session fork: find conversation for link rewrite", "session", fork, "err", err)
		return
	}
	from := "ao://sessions/" + string(project) + "/" + string(source) + "/artifacts/"
	to := "ao://sessions/" + string(project) + "/" + string(fork) + "/artifacts/"
	if _, err := rewriter.RewriteConversationText(ctx, conversation.ID, from, to); err != nil {
		m.logger.Warn("session fork: point artifact links at the fork", "session", fork, "err", err)
	}
	// The agent's history writes pasted images as "[Image]"; take them back
	// from the original's messages.
	if original, err := rewriter.ConversationForSession(ctx, source); err != nil {
		m.logger.Warn("session fork: find the original's conversation", "session", source, "err", err)
	} else if _, err := rewriter.CopyForkMessageContent(ctx, original.ID, conversation.ID); err != nil {
		m.logger.Warn("session fork: copy images from the original", "session", fork, "err", err)
	}
}

func copyRegularFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

type forkOriginKey struct{}

type forkOrigin struct {
	sourceID domain.SessionID
	title    string
}

func withForkOrigin(ctx context.Context, sourceID domain.SessionID, title string) context.Context {
	return context.WithValue(ctx, forkOriginKey{}, forkOrigin{sourceID: sourceID, title: title})
}

type forkLineageReader interface {
	GetSessionFork(ctx context.Context, forkID domain.SessionID) (domain.SessionFork, bool, error)
}

// forkBoundaryPrompt tells a fork's agent that it is a separate session from
// the one it was copied from, so it doesn't act on the original's PRs.
func (m *Manager) forkBoundaryPrompt(ctx context.Context, id domain.SessionID) string {
	origin, ok := ctx.Value(forkOriginKey{}).(forkOrigin)
	if !ok {
		lineage, isReader := m.store.(forkLineageReader)
		if !isReader {
			return ""
		}
		fork, found, err := lineage.GetSessionFork(ctx, id)
		if err != nil || !found {
			return ""
		}
		origin = forkOrigin{sourceID: fork.SourceSessionID, title: fork.SourceTitle}
	}
	if origin.sourceID == "" {
		return ""
	}
	title := ""
	if origin.title != "" {
		title = fmt.Sprintf(" (%q)", origin.title)
	}
	return fmt.Sprintf(`## Session Fork

This session is a fork of Orchestrator session %s%s. It started with that session's conversation and files, but it is a separate session (%s) with its own worktree, branch, and artifact folder. Work only in this session's own worktree; never edit the original's worktree.

The original's pull requests belong to the original session. Don't push to their branches, comment on them, request reviews of them, or claim them. If this work needs a PR, open a new one from this session's branch; target the original's branch when this work builds on it.

Plans and tasks from before the fork are background. Continue only what the user asks in this session, and report progress as this session, not as the original.`, origin.sourceID, title, id)
}

func (m *Manager) isSessionFork(ctx context.Context, id domain.SessionID) bool {
	lineage, ok := m.store.(forkLineageReader)
	if !ok {
		return false
	}
	_, found, err := lineage.GetSessionFork(ctx, id)
	return err == nil && found
}

// cleanupForkLeftovers removes what a fork copied for itself: its artifact
// files, its starting-files ref, and the agent's own session file made for it. The original's are
// never touched.
func (m *Manager) cleanupForkLeftovers(rec domain.SessionRecord, nativeFile string) {
	m.cleanupArtifactDir(rec.ID)
	if project, err := m.loadProject(context.Background(), rec.ProjectID); err == nil && project.Path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = m.forkSeeds.Delete(ctx, project.Path, forkseed.BaseRef(string(rec.ID)))
		cancel()
	}
	if nativeFile == "" {
		return
	}
	if err := os.Remove(nativeFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logger.Warn("session fork: remove the agent's session copy", "session", rec.ID, "path", nativeFile, "err", err)
	}
}

// forkNativeSessionFile is the agent's own session file for a fork, found
// through the agent's transcript locator. Empty when the agent has none.
func (m *Manager) forkNativeSessionFile(ctx context.Context, rec domain.SessionRecord) string {
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return ""
	}
	configs, hasConfig := agent.(ports.AgentNativeSessionConfigProvider)
	locator, hasLocator := agent.(ports.AgentTranscriptLocator)
	if !hasConfig || !hasLocator {
		return ""
	}
	native := rec.Metadata.ProviderConversationID
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		native = rec.Metadata.AgentSessionID
	}
	if strings.TrimSpace(native) == "" {
		return ""
	}
	var env map[string]string
	if project, err := m.loadProject(ctx, rec.ProjectID); err == nil {
		env = m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	}
	dir, err := configs.NativeSessionConfigDir(ctx, env)
	if err != nil || dir == "" {
		return ""
	}
	path, found, err := locator.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: native, ConfigDir: dir})
	if err != nil || !found {
		return ""
	}
	return path
}

// discardForkConversation removes a native copy made for a fork. Only Claude's
// copies are files AO places; other agents keep theirs in their own stores.
func (m *Manager) discardForkConversation(ctx context.Context, harness domain.AgentHarness, providerID, workspace string, env map[string]string) {
	if harness != domain.HarnessClaudeCode || providerID == "" || workspace == "" || m.claudeSessionCleanup == nil {
		return
	}
	if err := m.claudeSessionCleanup(context.WithoutCancel(ctx), providerID, workspace, env); err != nil {
		m.logger.Warn("session fork: remove Claude session copy", "session", providerID, "err", err)
	}
}

// ForkRetrier rebuilds the copy step for a fork whose start failed.
type ForkRetrier func(ctx context.Context, fork domain.SessionRecord) (*ports.SpawnFork, error)

// SetForkRetrier wires how a failed fork is made again on retry.
func (m *Manager) SetForkRetrier(retry ForkRetrier) { m.forkRetry = retry }

// dropInheritedForkChanges discards a fork's uncommitted changes when they
// are only the ones it inherited: its files still match what it started with,
// and its original is a live session that still holds them. Cleanup then
// removes the worktree like any clean one. It reports whether it discarded.
func (m *Manager) dropInheritedForkChanges(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo) bool {
	lineage, ok := m.store.(forkLineageReader)
	if !ok || ws.Path == "" {
		return false
	}
	fork, found, err := lineage.GetSessionFork(ctx, rec.ID)
	if err != nil || !found {
		return false
	}
	source, found, err := m.store.GetSession(ctx, fork.SourceSessionID)
	if err != nil || !found || source.IsTerminated {
		return false
	}
	unchanged, err := m.forkSeeds.UnchangedSince(ctx, ws.Path, forkseed.BaseRef(string(rec.ID)))
	if err != nil || !unchanged {
		return false
	}
	if err := m.forkSeeds.DiscardChanges(ctx, ws.Path); err != nil {
		m.logger.Warn("session fork: drop inherited changes before cleanup", "session", rec.ID, "err", err)
		return false
	}
	return true
}

// restoreForkStartingFiles puts a fork's starting files back into its worktree
// after restore recreated it. Archiving drops a fork's inherited changes when
// it never touched them, so the worktree can be removed; they live on under
// the fork's starting-files ref until it is deleted.
func (m *Manager) restoreForkStartingFiles(ctx context.Context, id domain.SessionID, worktree string) {
	if worktree == "" || !m.isSessionFork(ctx, id) {
		return
	}
	if _, err := m.forkSeeds.RestoreBase(ctx, worktree, forkseed.BaseRef(string(id))); err != nil {
		m.logger.Warn("session fork: put starting files back after restore", "session", id, "err", err)
	}
}
