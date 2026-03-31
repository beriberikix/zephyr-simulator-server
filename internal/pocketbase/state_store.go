package pocketbase

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
	"github.com/pocketbase/pocketbase/core"
)

// SnapshotStore persists full in-memory binary/session snapshots in PocketBase.
type SnapshotStore struct {
	app core.App
}

func NewSnapshotStore(app core.App) *SnapshotStore {
	return &SnapshotStore{app: app}
}

func (s *SnapshotStore) LoadState() (map[string]types.Binary, map[string]types.Session, error) {
	resultBinaries := make(map[string]types.Binary)
	resultSessions := make(map[string]types.Session)

	binaryRecords, err := s.app.FindAllRecords("binaries")
	if err != nil {
		return nil, nil, fmt.Errorf("load binaries records: %w", err)
	}
	for _, rec := range binaryRecords {
		decoded, err := decodeBinaryRecord(rec)
		if err != nil {
			return nil, nil, fmt.Errorf("decode binary %q: %w", rec.Id, err)
		}
		resultBinaries[decoded.ID] = decoded
	}

	sessionRecords, err := s.app.FindAllRecords("sessions")
	if err != nil {
		return nil, nil, fmt.Errorf("load sessions records: %w", err)
	}
	for _, rec := range sessionRecords {
		decoded, err := decodeSessionRecord(rec)
		if err != nil {
			return nil, nil, fmt.Errorf("decode session %q: %w", rec.Id, err)
		}
		resultSessions[decoded.ID] = decoded
	}

	return resultBinaries, resultSessions, nil
}

func (s *SnapshotStore) SaveState(binaries map[string]types.Binary, sessions map[string]types.Session) error {
	if err := s.syncBinaries(binaries); err != nil {
		return err
	}
	if err := s.syncSessions(sessions); err != nil {
		return err
	}
	return nil
}

func (s *SnapshotStore) syncBinaries(binaries map[string]types.Binary) error {
	col, err := s.app.FindCollectionByNameOrId("binaries")
	if err != nil {
		return fmt.Errorf("find binaries collection: %w", err)
	}

	existing, err := s.app.FindAllRecords(col)
	if err != nil {
		return fmt.Errorf("list binaries records: %w", err)
	}

	existingByRuntimeID := make(map[string]*core.Record, len(existing))
	for _, rec := range existing {
		decoded, err := decodeBinaryRecord(rec)
		if err != nil {
			return fmt.Errorf("decode existing binary %q: %w", rec.Id, err)
		}
		runtimeID := decoded.ID
		if runtimeID == "" {
			runtimeID = rec.Id
		}
		existingByRuntimeID[runtimeID] = rec
	}

	for runtimeID, binary := range binaries {
		payload, err := json.Marshal(binary)
		if err != nil {
			return fmt.Errorf("marshal binary %q payload: %w", runtimeID, err)
		}

		rec, ok := existingByRuntimeID[runtimeID]
		if !ok {
			rec = core.NewRecord(col)
		}

		rec.Set("name", binary.Name)
		rec.Set("bits", binary.Bits)
		rec.Set("is_static", binary.IsStatic)
		rec.Set("zephyr_version", binary.ZephyrVersion)
		rec.Set("file_path", binary.FilePath)
		rec.Set("file_size", binary.FileSize)
		rec.Set("checksum", binary.Checksum)
		rec.Set("payload", string(payload))

		if err := s.app.Save(rec); err != nil {
			return fmt.Errorf("save binary %q: %w", runtimeID, err)
		}
		delete(existingByRuntimeID, runtimeID)
	}

	for runtimeID, rec := range existingByRuntimeID {
		if err := s.app.Delete(rec); err != nil {
			return fmt.Errorf("delete stale binary %q: %w", runtimeID, err)
		}
	}

	return nil
}

func (s *SnapshotStore) syncSessions(sessions map[string]types.Session) error {
	col, err := s.app.FindCollectionByNameOrId("sessions")
	if err != nil {
		return fmt.Errorf("find sessions collection: %w", err)
	}

	existing, err := s.app.FindAllRecords(col)
	if err != nil {
		return fmt.Errorf("list sessions records: %w", err)
	}

	existingByRuntimeID := make(map[string]*core.Record, len(existing))
	for _, rec := range existing {
		decoded, err := decodeSessionRecord(rec)
		if err != nil {
			return fmt.Errorf("decode existing session %q: %w", rec.Id, err)
		}
		runtimeID := decoded.ID
		if runtimeID == "" {
			runtimeID = rec.Id
		}
		existingByRuntimeID[runtimeID] = rec
	}

	for runtimeID, session := range sessions {
		payload, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("marshal session %q payload: %w", runtimeID, err)
		}

		rec, ok := existingByRuntimeID[runtimeID]
		if !ok {
			rec = core.NewRecord(col)
		}

		rec.Set("binary_id", session.BinaryID)
		rec.Set("state", string(session.State))
		rec.Set("seed", session.Seed)
		rec.Set("use_real_time", session.UseRealTime)
		rec.Set("container_id", session.ContainerID)
		rec.Set("timeout_seconds", session.TimeoutSeconds)
		rec.Set("uptime", session.Uptime)
		rec.Set("owner_type", session.OwnerType)
		rec.Set("owner_id", session.OwnerID)
		if session.OwnerType == "user" {
			rec.Set("user", session.OwnerID)
		} else {
			rec.Set("user", "")
		}
		rec.Set("payload", string(payload))

		if err := s.app.Save(rec); err != nil {
			return fmt.Errorf("save session %q: %w", runtimeID, err)
		}
		delete(existingByRuntimeID, runtimeID)
	}

	for runtimeID, rec := range existingByRuntimeID {
		if err := s.app.Delete(rec); err != nil {
			return fmt.Errorf("delete stale session %q: %w", runtimeID, err)
		}
	}

	return nil
}

func decodeBinaryRecord(rec *core.Record) (types.Binary, error) {
	payload := strings.TrimSpace(rec.GetString("payload"))
	if payload != "" {
		var binary types.Binary
		if err := json.Unmarshal([]byte(payload), &binary); err != nil {
			return types.Binary{}, err
		}
		if binary.ID == "" {
			binary.ID = rec.Id
		}
		return binary, nil
	}

	return types.Binary{
		ID:         rec.Id,
		Name:       rec.GetString("name"),
		Bits:       rec.GetInt("bits"),
		IsStatic:   rec.GetBool("is_static"),
		FilePath:   rec.GetString("file_path"),
		FileSize:   int64(rec.GetInt("file_size")),
		Checksum:   rec.GetString("checksum"),
		UploadedAt: time.Now().UTC(),
	}, nil
}

func decodeSessionRecord(rec *core.Record) (types.Session, error) {
	payload := strings.TrimSpace(rec.GetString("payload"))
	if payload != "" {
		var session types.Session
		if err := json.Unmarshal([]byte(payload), &session); err != nil {
			return types.Session{}, err
		}
		if session.ID == "" {
			session.ID = rec.Id
		}
		return session, nil
	}

	now := time.Now().UTC()
	return types.Session{
		ID:             rec.Id,
		BinaryID:       rec.GetString("binary_id"),
		State:          types.SessionState(rec.GetString("state")),
		Seed:           uint64(rec.GetInt("seed")),
		UseRealTime:    rec.GetBool("use_real_time"),
		ContainerID:    rec.GetString("container_id"),
		TimeoutSeconds: rec.GetInt("timeout_seconds"),
		Uptime:         int64(rec.GetInt("uptime")),
		OwnerType:      rec.GetString("owner_type"),
		OwnerID:        rec.GetString("owner_id"),
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}
