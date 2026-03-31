package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

type timeoutStopper struct {
	stopped []string
	stopErr error
}

func (t *timeoutStopper) StopContainer(_ context.Context, containerID string) error {
	t.stopped = append(t.stopped, containerID)
	return t.stopErr
}

func (t *timeoutStopper) RemoveContainer(_ context.Context, _ string) error {
	return nil
}

func TestEnforceSessionTimeouts_StopsExpiredRunningSessions(t *testing.T) {
	resetStore()
	now := time.Now().UTC()

	storeMu.Lock()
	sessions["expired"] = types.Session{
		ID:             "expired",
		State:          types.SessionStateRunning,
		ContainerID:    "cid-expired",
		UpdatedAt:      now.Add(-20 * time.Second),
		TimeoutSeconds: 5,
	}
	sessions["active"] = types.Session{
		ID:             "active",
		State:          types.SessionStateRunning,
		ContainerID:    "cid-active",
		UpdatedAt:      now,
		TimeoutSeconds: 120,
	}
	sessions["already-stopped"] = types.Session{
		ID:             "already-stopped",
		State:          types.SessionStateStopped,
		ContainerID:    "cid-stopped",
		UpdatedAt:      now.Add(-1 * time.Hour),
		TimeoutSeconds: 1,
	}
	storeMu.Unlock()

	stopper := &timeoutStopper{}
	EnforceSessionTimeouts(stopper)

	if len(stopper.stopped) != 1 || stopper.stopped[0] != "cid-expired" {
		t.Fatalf("expected only expired container to be stopped, got %#v", stopper.stopped)
	}

	storeMu.RLock()
	defer storeMu.RUnlock()
	if sessions["expired"].State != types.SessionStateStopped {
		t.Fatalf("expected expired session to be stopped, got %s", sessions["expired"].State)
	}
	if sessions["active"].State != types.SessionStateRunning {
		t.Fatalf("expected active session to remain running, got %s", sessions["active"].State)
	}
}

func TestEnforceSessionTimeouts_StillMarksStoppedWhenContainerStopFails(t *testing.T) {
	resetStore()
	now := time.Now().UTC()

	storeMu.Lock()
	sessions["expired"] = types.Session{
		ID:             "expired",
		State:          types.SessionStateRunning,
		ContainerID:    "cid-expired",
		UpdatedAt:      now.Add(-30 * time.Second),
		TimeoutSeconds: 1,
	}
	storeMu.Unlock()

	stopper := &timeoutStopper{stopErr: errors.New("container runtime error")}
	EnforceSessionTimeouts(stopper)

	storeMu.RLock()
	defer storeMu.RUnlock()
	if sessions["expired"].State != types.SessionStateStopped {
		t.Fatalf("expected session to be marked stopped despite stop error, got %s", sessions["expired"].State)
	}
}

func TestPruneAnonymousSessions_RemovesPCAPArtifacts(t *testing.T) {
	resetStore()
	now := time.Now().UTC()
	tmpDir := t.TempDir()
	pcapPath := filepath.Join(tmpDir, "anon-expired.pcap")
	if err := os.WriteFile(pcapPath, []byte("pcap"), 0644); err != nil {
		t.Fatalf("write pcap fixture: %v", err)
	}

	storeMu.Lock()
	sessions["anon-expired"] = types.Session{
		ID:           "anon-expired",
		OwnerType:    string(OwnerTypeAnonymous),
		OwnerID:      "anon-1",
		State:        types.SessionStateStopped,
		PCAPFilePath: pcapPath,
		UpdatedAt:    now.Add(-3 * time.Hour),
	}
	storeMu.Unlock()

	PruneAnonymousSessions(newFakeContainerManager(), 2*time.Hour)

	storeMu.RLock()
	_, stillExists := sessions["anon-expired"]
	storeMu.RUnlock()
	if stillExists {
		t.Fatalf("expected expired anonymous session to be deleted")
	}

	if _, err := os.Stat(pcapPath); !os.IsNotExist(err) {
		t.Fatalf("expected pruner to remove pcap artifact, stat err=%v", err)
	}
}

func TestPruneOrphanedPCAPArtifacts(t *testing.T) {
	resetStore()
	tmpDir := t.TempDir()
	t.Setenv("PCAP_BASE_DIR", tmpDir)

	oldOrphan := filepath.Join(tmpDir, "old-orphan.pcap")
	referencedOld := filepath.Join(tmpDir, "referenced-old.pcap")
	recentOrphan := filepath.Join(tmpDir, "recent-orphan.pcap")

	for _, p := range []string{oldOrphan, referencedOld, recentOrphan} {
		if err := os.WriteFile(p, []byte("pcap"), 0644); err != nil {
			t.Fatalf("write pcap fixture %s: %v", p, err)
		}
	}

	oldTime := time.Now().UTC().Add(-3 * time.Hour)
	if err := os.Chtimes(oldOrphan, oldTime, oldTime); err != nil {
		t.Fatalf("set old mtime for orphan: %v", err)
	}
	if err := os.Chtimes(referencedOld, oldTime, oldTime); err != nil {
		t.Fatalf("set old mtime for referenced: %v", err)
	}

	storeMu.Lock()
	sessions["active"] = types.Session{
		ID:           "active",
		PCAPFilePath: referencedOld,
		UpdatedAt:    time.Now().UTC(),
	}
	storeMu.Unlock()

	PruneOrphanedPCAPArtifacts(2 * time.Hour)

	if _, err := os.Stat(oldOrphan); !os.IsNotExist(err) {
		t.Fatalf("expected old orphan artifact to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(referencedOld); err != nil {
		t.Fatalf("expected referenced artifact to be preserved, stat err=%v", err)
	}
	if _, err := os.Stat(recentOrphan); err != nil {
		t.Fatalf("expected recent orphan artifact to be preserved, stat err=%v", err)
	}
}
