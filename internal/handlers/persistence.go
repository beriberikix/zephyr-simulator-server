package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

// StateSnapshotStore provides optional external state persistence (e.g. PocketBase).
// It stores and restores the full binary/session snapshots.
type StateSnapshotStore interface {
	LoadState() (map[string]types.Binary, map[string]types.Session, error)
	SaveState(map[string]types.Binary, map[string]types.Session) error
}

var stateSnapshotStore StateSnapshotStore

// SetStateSnapshotStore configures optional external state persistence.
func SetStateSnapshotStore(store StateSnapshotStore) {
	stateSnapshotStore = store
}

// ConfigureStatePersistence initializes state loading/saving.
// With an external snapshot store configured, that store is authoritative.
// The optional path enables legacy JSON import-only migration behavior.
func ConfigureStatePersistence(path string) error {
	if stateSnapshotStore != nil {
		externalBinaries, externalSessions, err := stateSnapshotStore.LoadState()
		if err != nil {
			return fmt.Errorf("load external state: %w", err)
		}
		if len(externalBinaries) > 0 || len(externalSessions) > 0 {
			storeMu.Lock()
			binaries = externalBinaries
			sessions = externalSessions
			storeMu.Unlock()
			return nil
		}
	}

	if path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read state file: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}

	var decoded struct {
		Binaries map[string]types.Binary  `json:"binaries"`
		Sessions map[string]types.Session `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("decode state file: %w", err)
	}

	storeMu.Lock()
	if decoded.Binaries != nil {
		binaries = decoded.Binaries
	}
	if decoded.Sessions != nil {
		sessions = decoded.Sessions
	}
	storeMu.Unlock()

	// When an external store is configured but empty, use the legacy file as
	// a bootstrap source and immediately backfill the external store.
	if stateSnapshotStore != nil {
		bSnapshot := decoded.Binaries
		sSnapshot := decoded.Sessions
		if bSnapshot == nil {
			bSnapshot = map[string]types.Binary{}
		}
		if sSnapshot == nil {
			sSnapshot = map[string]types.Session{}
		}
		if len(bSnapshot) > 0 || len(sSnapshot) > 0 {
			if err := stateSnapshotStore.SaveState(bSnapshot, sSnapshot); err != nil {
				return fmt.Errorf("bootstrap external state from file: %w", err)
			}
		}
	}

	return nil
}

// persistState writes the current in-memory state to the configured external store.
func persistState() error {
	storeMu.RLock()
	bSnapshot := make(map[string]types.Binary, len(binaries))
	sSnapshot := make(map[string]types.Session, len(sessions))
	for k, v := range binaries {
		bSnapshot[k] = v
	}
	for k, v := range sessions {
		sSnapshot[k] = v
	}
	storeMu.RUnlock()

	if stateSnapshotStore != nil {
		if err := stateSnapshotStore.SaveState(bSnapshot, sSnapshot); err != nil {
			return fmt.Errorf("save external state: %w", err)
		}
	}

	return nil
}
