package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

type mockSnapshotStore struct {
	loadBinaries map[string]types.Binary
	loadSessions map[string]types.Session
	loadErr      error
	saveErr      error
	saveCalls    int
	lastSavedB   map[string]types.Binary
	lastSavedS   map[string]types.Session
}

func (m *mockSnapshotStore) LoadState() (map[string]types.Binary, map[string]types.Session, error) {
	if m.loadErr != nil {
		return nil, nil, m.loadErr
	}
	b := make(map[string]types.Binary, len(m.loadBinaries))
	s := make(map[string]types.Session, len(m.loadSessions))
	for k, v := range m.loadBinaries {
		b[k] = v
	}
	for k, v := range m.loadSessions {
		s[k] = v
	}
	return b, s, nil
}

func (m *mockSnapshotStore) SaveState(b map[string]types.Binary, s map[string]types.Session) error {
	m.saveCalls++
	m.lastSavedB = make(map[string]types.Binary, len(b))
	m.lastSavedS = make(map[string]types.Session, len(s))
	for k, v := range b {
		m.lastSavedB[k] = v
	}
	for k, v := range s {
		m.lastSavedS[k] = v
	}
	return m.saveErr
}

func TestPersistAndLoadState(t *testing.T) {
	resetStore()
	t.Cleanup(func() {
		SetStateSnapshotStore(nil)
		resetStore()
	})

	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	if err := ConfigureStatePersistence(statePath); err != nil {
		t.Fatalf("configure persistence: %v", err)
	}

	now := time.Now().UTC()
	storeMu.Lock()
	binaries["b1"] = types.Binary{ID: "b1", Name: "app.elf", FilePath: "/tmp/app.elf", UploadedAt: now}
	sessions["s1"] = types.Session{ID: "s1", BinaryID: "b1", State: types.SessionStateStopped, CreatedAt: now, UpdatedAt: now}
	storeMu.Unlock()

	payload := []byte(fmt.Sprintf(`{"binaries":{"b1":{"id":"b1","name":"app.elf","file_path":"/tmp/app.elf","uploaded_at":"%s"}},"sessions":{"s1":{"id":"s1","binary_id":"b1","state":"stopped","created_at":"%s","updated_at":"%s"}}}`,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)))
	if err := os.WriteFile(statePath, payload, 0644); err != nil {
		t.Fatalf("write state file: %v", err)
	}

	resetStore()
	if err := ConfigureStatePersistence(statePath); err != nil {
		t.Fatalf("reload state: %v", err)
	}

	storeMu.RLock()
	_, hasBinary := binaries["b1"]
	_, hasSession := sessions["s1"]
	storeMu.RUnlock()

	if !hasBinary || !hasSession {
		t.Fatalf("expected persisted binary and session to load, got binary=%v session=%v", hasBinary, hasSession)
	}
}

func TestConfigureStatePersistenceLoadsFromExternalStore(t *testing.T) {
	resetStore()
	now := time.Now().UTC()
	store := &mockSnapshotStore{
		loadBinaries: map[string]types.Binary{"b1": {ID: "b1", Name: "app.elf", UploadedAt: now}},
		loadSessions: map[string]types.Session{"s1": {ID: "s1", BinaryID: "b1", State: types.SessionStateStopped, CreatedAt: now, UpdatedAt: now}},
	}
	SetStateSnapshotStore(store)

	t.Cleanup(func() {
		SetStateSnapshotStore(nil)
		resetStore()
	})

	if err := ConfigureStatePersistence(""); err != nil {
		t.Fatalf("configure persistence with external store: %v", err)
	}

	storeMu.RLock()
	_, hasBinary := binaries["b1"]
	_, hasSession := sessions["s1"]
	storeMu.RUnlock()

	if !hasBinary || !hasSession {
		t.Fatalf("expected external state to load, got binary=%v session=%v", hasBinary, hasSession)
	}
}

func TestPersistStateSavesToExternalStore(t *testing.T) {
	resetStore()
	store := &mockSnapshotStore{}
	SetStateSnapshotStore(store)
	t.Cleanup(func() {
		SetStateSnapshotStore(nil)
		resetStore()
	})

	storeMu.Lock()
	binaries["b1"] = types.Binary{ID: "b1", Name: "app.elf", UploadedAt: time.Now().UTC()}
	sessions["s1"] = types.Session{ID: "s1", BinaryID: "b1", State: types.SessionStateStopped}
	storeMu.Unlock()

	if err := persistState(); err != nil {
		t.Fatalf("persist state: %v", err)
	}
	if store.saveCalls != 1 {
		t.Fatalf("expected 1 external save call, got %d", store.saveCalls)
	}
}

func TestConfigureStatePersistenceReturnsExternalLoadError(t *testing.T) {
	resetStore()
	SetStateSnapshotStore(&mockSnapshotStore{loadErr: fmt.Errorf("boom")})
	t.Cleanup(func() {
		SetStateSnapshotStore(nil)
		resetStore()
	})

	if err := ConfigureStatePersistence(""); err == nil {
		t.Fatal("expected external load error")
	}
}

func TestConfigureStatePersistenceBootstrapsExternalFromFile(t *testing.T) {
	resetStore()
	store := &mockSnapshotStore{}
	SetStateSnapshotStore(store)
	t.Cleanup(func() {
		SetStateSnapshotStore(nil)
		resetStore()
	})

	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")
	now := time.Now().UTC()
	statePayload := []byte(fmt.Sprintf(`{"binaries":{"b1":{"id":"b1","name":"app.elf","uploaded_at":"%s"}},"sessions":{"s1":{"id":"s1","binary_id":"b1","state":"stopped","created_at":"%s","updated_at":"%s"}}}`,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)))
	if err := os.WriteFile(statePath, statePayload, 0644); err != nil {
		t.Fatalf("write state file: %v", err)
	}

	if err := ConfigureStatePersistence(statePath); err != nil {
		t.Fatalf("configure persistence: %v", err)
	}

	if store.saveCalls != 1 {
		t.Fatalf("expected bootstrap save call to external store, got %d", store.saveCalls)
	}
	if _, ok := store.lastSavedB["b1"]; !ok {
		t.Fatal("expected binary b1 to be backfilled to external store")
	}
	if _, ok := store.lastSavedS["s1"]; !ok {
		t.Fatal("expected session s1 to be backfilled to external store")
	}
}
