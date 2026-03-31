package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"log"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

// ContainerStopper is the subset of ContainerManager needed by the pruner.
// Using an interface slice here avoids a circular import if ContainerManager
// is ever moved; for now it matches ContainerManager exactly.
type ContainerStopper interface {
	StopContainer(ctx context.Context, containerID string) error
	RemoveContainer(ctx context.Context, containerID string) error
}

// canAccessSession reports whether the caller identified by id is allowed to
// read the session with the given ownerType/ownerID. Admins can access all
// sessions; other callers can only access sessions they own.
func canAccessSession(id Identity, ownerType, ownerID string) bool {
	if id.IsAdmin {
		return true
	}
	// Sessions without an owner (created before auth was introduced) are
	// accessible to all callers for backward compatibility.
	if ownerID == "" {
		return true
	}
	return string(id.Type) == ownerType && id.ID != "" && id.ID == ownerID
}

// requireSessionOwner checks that the caller owns the session (or is an admin).
// Returns true and writes a 403 response if access is denied; the caller must
// return immediately in that case.
func requireSessionOwner(w http.ResponseWriter, r *http.Request, ownerType, ownerID string) bool {
	id := GetIdentity(r)
	if canAccessSession(id, ownerType, ownerID) {
		return false // access granted, no error written
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(Response{Success: false, Error: "Access denied"})
	return true // access denied, caller must return
}

// requireAuthenticated ensures the caller is a logged-in user (or admin).
// Returns true and writes a 401 response if the caller is anonymous.
func requireAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	id := GetIdentity(r)
	if id.Type == OwnerTypeUser {
		return false // authenticated, no error written
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(Response{Success: false, Error: "Login required"})
	return true // denied, caller must return
}

// PruneAnonymousSessions deletes anonymous sessions whose UpdatedAt is older
// than ttl, stopping any running containers first.
// Called periodically from the server pruner goroutine.
func PruneAnonymousSessions(mgr ContainerManager, ttl time.Duration) {
	cutoff := time.Now().UTC().Add(-ttl)

	storeMu.Lock()
	var toDelete []string
	containerIDs := map[string]string{} // sessionID → containerID
	pcapPaths := make([]string, 0)
	for id, s := range sessions {
		if s.OwnerType == string(OwnerTypeAnonymous) && s.UpdatedAt.Before(cutoff) {
			toDelete = append(toDelete, id)
			if s.ContainerID != "" {
				containerIDs[id] = s.ContainerID
			}
			if strings.TrimSpace(s.PCAPFilePath) != "" {
				pcapPaths = append(pcapPaths, s.PCAPFilePath)
			}
		}
	}
	for _, id := range toDelete {
		delete(sessions, id)
	}
	storeMu.Unlock()

	if len(toDelete) == 0 {
		return
	}

	// Persist after deletion.
	if err := persistState(); err != nil {
		log.Printf("[pruner] persist state failed: %v", err)
	}

	// Stop and remove containers outside the lock.
	ctx := context.Background()
	for _, cid := range containerIDs {
		if err := mgr.StopContainer(ctx, cid); err != nil {
			log.Printf("[pruner] stop container %s failed: %v", cid, err)
		}
		if err := mgr.RemoveContainer(ctx, cid); err != nil {
			log.Printf("[pruner] remove container %s failed: %v", cid, err)
		}
	}

	for _, pcapPath := range pcapPaths {
		cleanupPCAPArtifact(pcapPath)
	}

	log.Printf("[pruner] removed %d expired anonymous session(s)", len(toDelete))
}

// PruneOrphanedPCAPArtifacts removes old capture files that are no longer
// referenced by any active session.
func PruneOrphanedPCAPArtifacts(retention time.Duration) {
	if retention <= 0 {
		return
	}

	baseDir := getPCAPBaseDir()
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[pcap-pruner] read pcap directory failed: %v", err)
		}
		return
	}

	storeMu.RLock()
	activePaths := make(map[string]struct{}, len(sessions))
	for _, s := range sessions {
		if p := strings.TrimSpace(s.PCAPFilePath); p != "" {
			activePaths[filepath.Clean(p)] = struct{}{}
		}
	}
	storeMu.RUnlock()

	cutoff := time.Now().UTC().Add(-retention)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pcap") {
			continue
		}

		fullPath := filepath.Join(baseDir, entry.Name())
		cleanedPath := filepath.Clean(fullPath)
		if _, inUse := activePaths[cleanedPath]; inUse {
			continue
		}

		info, statErr := entry.Info()
		if statErr != nil {
			log.Printf("[pcap-pruner] stat failed for %s: %v", fullPath, statErr)
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}

		cleanupPCAPArtifact(fullPath)
		removed++
	}

	if removed > 0 {
		log.Printf("[pcap-pruner] removed %d orphaned capture artifact(s)", removed)
	}
}

// EnforceSessionTimeouts stops running sessions whose timeout window has
// elapsed. It uses UpdatedAt as the start/reference point for the current run.
// Called periodically from the server timeout enforcer goroutine.
func EnforceSessionTimeouts(mgr ContainerStopper) {
	now := time.Now().UTC()

	type timeoutCandidate struct {
		sessionID   string
		containerID string
	}

	storeMu.RLock()
	var candidates []timeoutCandidate
	for id, s := range sessions {
		if s.State != types.SessionStateRunning || s.TimeoutSeconds <= 0 {
			continue
		}
		deadline := s.UpdatedAt.Add(time.Duration(s.TimeoutSeconds) * time.Second)
		if now.After(deadline) {
			candidates = append(candidates, timeoutCandidate{sessionID: id, containerID: s.ContainerID})
		}
	}
	storeMu.RUnlock()

	if len(candidates) == 0 {
		return
	}

	ctx := context.Background()
	for _, c := range candidates {
		if c.containerID != "" {
			if err := mgr.StopContainer(ctx, c.containerID); err != nil {
				log.Printf("[timeout-enforcer] stop container %s failed: %v", c.containerID, err)
			}
		}
	}

	storeMu.Lock()
	updated := 0
	for _, c := range candidates {
		s, ok := sessions[c.sessionID]
		if !ok {
			continue
		}
		if s.State != types.SessionStateRunning {
			continue
		}
		s.State = types.SessionStateStopped
		s.UpdatedAt = now
		sessions[c.sessionID] = s
		updated++
	}
	storeMu.Unlock()

	if updated == 0 {
		return
	}

	if err := persistState(); err != nil {
		log.Printf("[timeout-enforcer] persist state failed: %v", err)
	}

	log.Printf("[timeout-enforcer] stopped %d timed-out session(s)", updated)
}
