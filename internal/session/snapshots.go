package session

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

// SnapshotManager handles session serialization and restoration
type SnapshotManager struct{}

// NewSnapshotManager creates a new snapshot manager
func NewSnapshotManager() *SnapshotManager {
	return &SnapshotManager{}
}

// CreateSnapshot serializes a session's current state for persistence
func (sm *SnapshotManager) CreateSnapshot(session *types.Session, binary *types.Binary) (types.Snapshot, error) {
	if session == nil || binary == nil {
		return types.Snapshot{}, fmt.Errorf("session and binary required")
	}

	// Build flags map from session state
	flags := map[string]interface{}{
		"seed":          session.Seed,
		"use_real_time": session.UseRealTime,
		"verbose":       true,
	}

	// Parse PCAPPath if present
	if session.PCAPFilePath != "" {
		flags["pcap"] = session.PCAPFilePath
	}

	snapshot := types.Snapshot{
		SessionID:   session.ID,
		BinaryID:    binary.ID,
		State:       session.State,
		Seed:        session.Seed,
		UseRealTime: session.UseRealTime,
		CreatedAt:   time.Now(),
		Flags:       flags,
		Volumes: types.SnapshotVolumes{
			FlashPath:  fmt.Sprintf("/var/lib/zephyr-emu/sessions/%s/flash.bin", session.ID),
			EEPROMPath: fmt.Sprintf("/var/lib/zephyr-emu/sessions/%s/eeprom.bin", session.ID),
		},
	}

	return snapshot, nil
}

// SerializeSnapshot converts a snapshot to JSON
func (sm *SnapshotManager) SerializeSnapshot(snapshot types.Snapshot) (string, error) {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal snapshot: %w", err)
	}
	return string(data), nil
}

// DeserializeSnapshot converts JSON back to a snapshot
func (sm *SnapshotManager) DeserializeSnapshot(data string) (types.Snapshot, error) {
	var snapshot types.Snapshot
	if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
		return types.Snapshot{}, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	return snapshot, nil
}

// RestoreSession recreates a session from a snapshot
func (sm *SnapshotManager) RestoreSession(snapshot types.Snapshot, oldSessionID, newSessionID string) types.Session {
	// Create a new session based on snapshot data
	// Note: container ID will be assigned after Docker container creation
	return types.Session{
		ID:             newSessionID,
		BinaryID:       snapshot.BinaryID,
		State:          types.SessionStateStopped, // Start in stopped state
		Seed:           snapshot.Seed,
		UseRealTime:    snapshot.UseRealTime,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		TimeoutSeconds: 300, // Default timeout
	}
}

// ValidateSnapshot checks if a snapshot is valid and can be restored
func (sm *SnapshotManager) ValidateSnapshot(snapshot types.Snapshot) error {
	if snapshot.SessionID == "" {
		return fmt.Errorf("snapshot missing session ID")
	}
	if snapshot.BinaryID == "" {
		return fmt.Errorf("snapshot missing binary ID")
	}
	if snapshot.Flags == nil || len(snapshot.Flags) == 0 {
		return fmt.Errorf("snapshot missing flags")
	}
	return nil
}

// CompareSnapshots compares two snapshots for significant differences
func (sm *SnapshotManager) CompareSnapshots(s1, s2 types.Snapshot) map[string]interface{} {
	changes := make(map[string]interface{})

	if s1.Seed != s2.Seed {
		changes["seed"] = map[string]uint64{
			"old": s1.Seed,
			"new": s2.Seed,
		}
	}

	if s1.UseRealTime != s2.UseRealTime {
		changes["use_real_time"] = map[string]bool{
			"old": s1.UseRealTime,
			"new": s2.UseRealTime,
		}
	}

	if s1.State != s2.State {
		changes["state"] = map[string]types.SessionState{
			"old": s1.State,
			"new": s2.State,
		}
	}

	return changes
}

// GetSnapshotMetadata extracts metadata about a snapshot
func (sm *SnapshotManager) GetSnapshotMetadata(snapshot types.Snapshot) map[string]interface{} {
	return map[string]interface{}{
		"session_id":    snapshot.SessionID,
		"binary_id":     snapshot.BinaryID,
		"state":         snapshot.State,
		"created_at":    snapshot.CreatedAt,
		"seed":          snapshot.Seed,
		"use_real_time": snapshot.UseRealTime,
	}
}
