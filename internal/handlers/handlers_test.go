package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beriberikix/zephyr-simulator-server/internal/container"
	"github.com/beriberikix/zephyr-simulator-server/internal/types"
	"github.com/beriberikix/zephyr-simulator-server/internal/uart"
	"github.com/gorilla/websocket"
)

type fakeContainerManager struct {
	statusByID map[string]types.SessionState
	startErrByID map[string]error
	nextID     int
}

// withUserIdentity injects an authenticated (non-admin) user identity into r.
// Use this in tests for endpoints gated by requireAuthenticated.
func withUserIdentity(r *http.Request) *http.Request {
	return SetIdentity(r, Identity{Type: OwnerTypeUser, ID: "test-user-id", IsAdmin: false})
}

// withAdminIdentity injects an admin identity into r.
func withAdminIdentity(r *http.Request) *http.Request {
	return SetIdentity(r, Identity{Type: OwnerTypeUser, ID: "test-admin-id", IsAdmin: true})
}

func newFakeContainerManager() *fakeContainerManager {
	return &fakeContainerManager{
		statusByID:   map[string]types.SessionState{},
		startErrByID: map[string]error{},
	}
}

func (f *fakeContainerManager) CreateContainer(_ context.Context, _ *types.Session, _ *types.Binary) (string, error) {
	f.nextID++
	id := fmt.Sprintf("fake-container-%d", f.nextID)
	f.statusByID[id] = types.SessionStateStopped
	return id, nil
}

func (f *fakeContainerManager) StartContainer(_ context.Context, containerID string) error {
	if err, ok := f.startErrByID[containerID]; ok {
		return err
	}
	f.statusByID[containerID] = types.SessionStateRunning
	return nil
}

func (f *fakeContainerManager) StopContainer(_ context.Context, containerID string) error {
	f.statusByID[containerID] = types.SessionStateStopped
	return nil
}

func (f *fakeContainerManager) PauseContainer(_ context.Context, containerID string) error {
	f.statusByID[containerID] = types.SessionStatePaused
	return nil
}

func (f *fakeContainerManager) ResumeContainer(_ context.Context, containerID string) error {
	f.statusByID[containerID] = types.SessionStateRunning
	return nil
}

func (f *fakeContainerManager) RemoveContainer(_ context.Context, containerID string) error {
	delete(f.statusByID, containerID)
	return nil
}

func (f *fakeContainerManager) GetContainerStatus(_ context.Context, containerID string) (types.SessionState, error) {
	if st, ok := f.statusByID[containerID]; ok {
		return st, nil
	}
	return types.SessionStateStopped, nil
}

func (f *fakeContainerManager) StreamContainerLogs(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewBufferString("")), nil
}

type sseProbeContainerManager struct {
	*fakeContainerManager
	streamCalls int
}

func (m *sseProbeContainerManager) StreamContainerLogs(_ context.Context, _ string) (io.ReadCloser, error) {
	m.streamCalls++
	return io.NopCloser(bytes.NewBufferString("")), nil
}

func (f *fakeContainerManager) IsContainerTTY(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func TestCloneSessionRuntimeState_SkipsMissingSourceVolumes(t *testing.T) {
	orig := runDockerCLI
	t.Cleanup(func() { runDockerCLI = orig })

	var calls [][]string
		runDockerCLI = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(args) >= 3 && args[0] == "volume" && args[1] == "inspect" {
			return []byte("Error: No such volume"), fmt.Errorf("exit status 1")
		}
		return []byte("ok"), nil
	}

	if err := cloneSessionRuntimeState(context.Background(), "old", "new"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	for _, c := range calls {
		if len(c) > 0 && c[0] == "run" {
			t.Fatalf("expected no docker run copy when source volumes are missing, got calls=%v", calls)
		}
	}
}

func TestCloneSessionRuntimeState_CopiesMainAndTmpVolumes(t *testing.T) {
	orig := runDockerCLI
	t.Cleanup(func() { runDockerCLI = orig })

	var calls [][]string
	runDockerCLI = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(args) >= 3 && args[0] == "volume" && args[1] == "inspect" {
			return []byte("[]"), nil
		}
		return []byte("ok"), nil
	}

	if err := cloneSessionRuntimeState(context.Background(), "old", "new"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	runCount := 0
	for _, c := range calls {
		if len(c) > 0 && c[0] == "run" {
			runCount++
		}
	}
	if runCount != 2 {
		t.Fatalf("expected 2 docker run copy operations (main+tmp), got %d calls=%v", runCount, calls)
	}
}

func resetStore() {
	storeMu.Lock()
	defer storeMu.Unlock()
	binaries = map[string]types.Binary{}
	sessions = map[string]types.Session{}
}

func TestSessionCRUD(t *testing.T) {
	resetStore()

	storeMu.Lock()
	binaries["b1"] = types.Binary{
		ID:         "b1",
		Name:       "app.elf",
		FilePath:   "/tmp/app.elf",
		UploadedAt: time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", HandleCreateSession(nil))
	mux.HandleFunc("GET /api/sessions", HandleListSessions())
	mux.HandleFunc("GET /api/sessions/{id}", HandleGetSession())
	mux.HandleFunc("PATCH /api/sessions/{id}", HandleUpdateSession(nil))

	// Create
	createBody := bytes.NewBufferString(`{"binary_id":"b1","seed":42}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", createBody)
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create session status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	var createResp Response
	if err := json.Unmarshal(createRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if !createResp.Success {
		t.Fatalf("create response not successful: %s", createRec.Body.String())
	}

	createdMap, ok := createResp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("create response data not object: %#v", createResp.Data)
	}
	sessionID, _ := createdMap["id"].(string)
	if sessionID == "" {
		t.Fatalf("missing session id in create response")
	}

	// List
	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list session status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if !bytes.Contains(listRec.Body.Bytes(), []byte(sessionID)) {
		t.Fatalf("created session id %q not found in list response: %s", sessionID, listRec.Body.String())
	}

	// Get
	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get session status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	if !bytes.Contains(getRec.Body.Bytes(), []byte(`"seed":42`)) {
		t.Fatalf("expected seed=42 in get response: %s", getRec.Body.String())
	}

	// Update
	updateReq := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sessionID, bytes.NewBufferString(`{"seed":99}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	mux.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update session status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if !bytes.Contains(updateRec.Body.Bytes(), []byte(`"seed":99`)) {
		t.Fatalf("expected seed=99 in update response: %s", updateRec.Body.String())
	}
}

func TestDeleteBinaryInUseConflict(t *testing.T) {
	resetStore()

	storeMu.Lock()
	binaries["b1"] = types.Binary{ID: "b1", Name: "app.elf", FilePath: "/tmp/app.elf", UploadedAt: time.Now().UTC()}
	sessions["s1"] = types.Session{ID: "s1", BinaryID: "b1", State: types.SessionStateRunning}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/binaries/{id}", HandleDeleteBinary())

	req := httptest.NewRequest(http.MethodDelete, "/api/binaries/b1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteSession_RemovesPCAPArtifact(t *testing.T) {
	resetStore()
	fakeMgr := newFakeContainerManager()

	tmpDir := t.TempDir()
	pcapPath := filepath.Join(tmpDir, "delete-me.pcap")
	if err := os.WriteFile(pcapPath, []byte("pcap"), 0644); err != nil {
		t.Fatalf("write pcap fixture: %v", err)
	}

	storeMu.Lock()
	sessions["s-delete"] = types.Session{
		ID:           "s-delete",
		BinaryID:     "b1",
		State:        types.SessionStateStopped,
		PCAPFilePath: pcapPath,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/sessions/{id}", HandleDeleteSession(fakeMgr))

	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/s-delete", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(pcapPath); !os.IsNotExist(err) {
		t.Fatalf("expected pcap artifact to be removed, stat err=%v", err)
	}
}

func TestUploadInvalidBinaryReturnsBadRequest(t *testing.T) {
	resetStore()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/binaries", HandleUploadBinary(nil))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("binary", `C:\build\zephyr.exe`)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("not-elf-content")); err != nil {
		t.Fatalf("write form part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/binaries", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Analyze binary")) {
		t.Fatalf("expected analyzer error in response, got: %s", rec.Body.String())
	}
}

func TestSessionLifecycleHandlers(t *testing.T) {
	resetStore()
	fakeMgr := newFakeContainerManager()

	storeMu.Lock()
	binaries["b1"] = types.Binary{
		ID:         "b1",
		Name:       "app.elf",
		FilePath:   "/tmp/app.elf",
		UploadedAt: time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", HandleCreateSession(fakeMgr))
	mux.HandleFunc("POST /api/sessions/{id}/start", HandleStartSession(fakeMgr))
	mux.HandleFunc("POST /api/sessions/{id}/pause", HandlePauseSession(fakeMgr))
	mux.HandleFunc("POST /api/sessions/{id}/resume", HandleResumeSession(fakeMgr))
	mux.HandleFunc("POST /api/sessions/{id}/stop", HandleStopSession(fakeMgr))

	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"binary_id":"b1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create session status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	var created struct {
		Success bool          `json:"success"`
		Data    types.Session `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	sid := created.Data.ID

	for _, tc := range []struct {
		name       string
		actionPath string
		expected   types.SessionState
	}{
		{name: "start", actionPath: "/start", expected: types.SessionStateRunning},
		{name: "pause", actionPath: "/pause", expected: types.SessionStatePaused},
		{name: "resume", actionPath: "/resume", expected: types.SessionStateRunning},
		{name: "stop", actionPath: "/stop", expected: types.SessionStateStopped},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sid+tc.actionPath, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", tc.name, rec.Code, rec.Body.String())
		}

		var resp struct {
			Success bool          `json:"success"`
			Data    types.Session `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal %s response: %v", tc.name, err)
		}
		if resp.Data.State != tc.expected {
			t.Fatalf("%s expected state=%s got=%s", tc.name, tc.expected, resp.Data.State)
		}
	}
}

func TestSessionLifecycleTransitionGuards(t *testing.T) {
	resetStore()
	fakeMgr := newFakeContainerManager()

	storeMu.Lock()
	binaries["b1"] = types.Binary{
		ID:         "b1",
		Name:       "app.elf",
		FilePath:   "/tmp/app.elf",
		UploadedAt: time.Now().UTC(),
	}
	sessions["stopped"] = types.Session{
		ID:          "stopped",
		BinaryID:    "b1",
		ContainerID: "cid-stopped",
		State:       types.SessionStateStopped,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	sessions["paused"] = types.Session{
		ID:          "paused",
		BinaryID:    "b1",
		ContainerID: "cid-paused",
		State:       types.SessionStatePaused,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	sessions["running"] = types.Session{
		ID:          "running",
		BinaryID:    "b1",
		ContainerID: "cid-running",
		State:       types.SessionStateRunning,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/start", HandleStartSession(fakeMgr))
	mux.HandleFunc("POST /api/sessions/{id}/pause", HandlePauseSession(fakeMgr))
	mux.HandleFunc("POST /api/sessions/{id}/resume", HandleResumeSession(fakeMgr))
	mux.HandleFunc("POST /api/sessions/{id}/stop", HandleStopSession(fakeMgr))

	tests := []struct {
		name       string
		methodPath string
		expectCode int
	}{
		{name: "start_paused_conflict", methodPath: "/api/sessions/paused/start", expectCode: http.StatusConflict},
		{name: "pause_stopped_conflict", methodPath: "/api/sessions/stopped/pause", expectCode: http.StatusConflict},
		{name: "resume_stopped_conflict", methodPath: "/api/sessions/stopped/resume", expectCode: http.StatusConflict},
		{name: "stop_stopped_idempotent", methodPath: "/api/sessions/stopped/stop", expectCode: http.StatusOK},
		{name: "pause_paused_idempotent", methodPath: "/api/sessions/paused/pause", expectCode: http.StatusOK},
		{name: "resume_running_idempotent", methodPath: "/api/sessions/running/resume", expectCode: http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.methodPath, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.expectCode {
				t.Fatalf("expected status=%d got=%d body=%s", tc.expectCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestStartSession_RecreateOnMissingContainer(t *testing.T) {
	resetStore()
	fakeMgr := newFakeContainerManager()

	storeMu.Lock()
	binaries["b1"] = types.Binary{
		ID:         "b1",
		Name:       "app.elf",
		FilePath:   "/tmp/app.elf",
		UploadedAt: time.Now().UTC(),
	}
	sessions["s1"] = types.Session{
		ID:         "s1",
		BinaryID:   "b1",
		ContainerID: "missing-container",
		State:      types.SessionStateStopped,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	storeMu.Unlock()

	fakeMgr.startErrByID["missing-container"] = fmt.Errorf("No such container: missing-container")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/start", HandleStartSession(fakeMgr))

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/start", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Success bool          `json:"success"`
		Data    types.Session `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success response, got body=%s", rec.Body.String())
	}
	if resp.Data.ContainerID == "missing-container" || resp.Data.ContainerID == "" {
		t.Fatalf("expected recreated container id, got %q", resp.Data.ContainerID)
	}
	if resp.Data.State != types.SessionStateRunning {
		t.Fatalf("expected running state, got %s", resp.Data.State)
	}
}

func TestStartSession_RegistersUARTBackendsHook(t *testing.T) {
	resetStore()
	fakeMgr := newFakeContainerManager()

	origRegister := registerSessionUARTBackends
	origUnregister := unregisterSessionUARTBackends
	t.Cleanup(func() {
		registerSessionUARTBackends = origRegister
		unregisterSessionUARTBackends = origUnregister
	})

	called := false
	registerSessionUARTBackends = func(session *types.Session) error {
		if session != nil && session.ID == "s-uart-start" {
			called = true
		}
		return nil
	}

	storeMu.Lock()
	binaries["b1"] = types.Binary{ID: "b1", Name: "app.elf", FilePath: "/tmp/app.elf", UploadedAt: time.Now().UTC()}
	sessions["s-uart-start"] = types.Session{
		ID:          "s-uart-start",
		BinaryID:    "b1",
		ContainerID: "cid-uart-start",
		State:       types.SessionStateStopped,
		UARTBins:    []string{"/tmp/session-s-uart-start-uart/uart0.fifo"},
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/start", HandleStartSession(fakeMgr))

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-uart-start/start", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatalf("expected registerSessionUARTBackends hook to be called")
	}
}

func TestStopSession_UnregistersUARTBackendsHook(t *testing.T) {
	resetStore()
	fakeMgr := newFakeContainerManager()

	origRegister := registerSessionUARTBackends
	origUnregister := unregisterSessionUARTBackends
	t.Cleanup(func() {
		registerSessionUARTBackends = origRegister
		unregisterSessionUARTBackends = origUnregister
	})

	called := false
	unregisterSessionUARTBackends = func(sessionID string) {
		if sessionID == "s-uart-stop" {
			called = true
		}
	}

	storeMu.Lock()
	sessions["s-uart-stop"] = types.Session{
		ID:          "s-uart-stop",
		BinaryID:    "b1",
		ContainerID: "cid-uart-stop",
		State:       types.SessionStateRunning,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/stop", HandleStopSession(fakeMgr))

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-uart-stop/stop", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatalf("expected unregisterSessionUARTBackends hook to be called")
	}
}

func TestUpdateSessionDebugConfig(t *testing.T) {
	resetStore()

	storeMu.Lock()
	binaries["b1"] = types.Binary{
		ID:         "b1",
		Name:       "app.elf",
		FilePath:   "/tmp/app.elf",
		UploadedAt: time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", HandleCreateSession(nil))
	mux.HandleFunc("PATCH /api/sessions/{id}", HandleUpdateSession(nil))

	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"binary_id":"b1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create session status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	var created struct {
		Success bool          `json:"success"`
		Data    types.Session `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}

	updateReq := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+created.Data.ID, bytes.NewBufferString(`{"debug_config":{"enabled":true,"port":4444,"wait_for_gdb":true}}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	mux.ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("update session status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if !bytes.Contains(updateRec.Body.Bytes(), []byte(`"debug_config"`)) {
		t.Fatalf("expected debug_config in update response: %s", updateRec.Body.String())
	}
	if !bytes.Contains(updateRec.Body.Bytes(), []byte(`"port":4444`)) {
		t.Fatalf("expected debug port in update response: %s", updateRec.Body.String())
	}
}

func TestGetDebugTarget(t *testing.T) {
	resetStore()

	storeMu.Lock()
	sessions["s-debug"] = types.Session{
		ID:          "s-debug",
		BinaryID:    "b1",
		State:       types.SessionStateRunning,
		ContainerID: "container-123",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		DebugConfig: &types.DebugConfig{Enabled: true, Port: 4444, WaitForGDB: true},
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{id}/debug-target", HandleGetDebugTarget())

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s-debug/debug-target", nil)
	req = withUserIdentity(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("debug target status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"enabled":true`)) {
		t.Fatalf("expected enabled true in response: %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"port":4444`)) {
		t.Fatalf("expected port 4444 in response: %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"host":"127.0.0.1"`)) {
		t.Fatalf("expected host in response: %s", rec.Body.String())
	}
}

func TestSetupHostNetworkHandler_Success(t *testing.T) {
	origSetup := setupHostNetworking
	t.Cleanup(func() {
		setupHostNetworking = origSetup
	})

	setupHostNetworking = func(canDevices []types.CanDeviceConfig, tapInterfaces []types.TapConfig) (*container.HostNetworkSetupResult, error) {
		if len(canDevices) != 1 || canDevices[0].Name != "vcan0" {
			t.Fatalf("unexpected can devices payload: %#v", canDevices)
		}
		if len(tapInterfaces) != 1 || tapInterfaces[0].BridgeInterface != "br0" {
			t.Fatalf("unexpected tap interfaces payload: %#v", tapInterfaces)
		}
		return &container.HostNetworkSetupResult{Items: []container.HostNetworkSetupItem{{Resource: "vcan0", Action: "created"}}}, nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/network/setup", HandleSetupHostNetwork())

	req := httptest.NewRequest(http.MethodPost, "/api/network/setup", bytes.NewBufferString(`{
		"can_devices": [{"name":"vcan0","host_device":"/dev/vcan0"}],
		"tap_interfaces": [{"host_interface":"tap0","enable_bridge":true,"bridge_interface":"br0"}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"success":true`)) {
		t.Fatalf("expected success=true, got body=%s", rec.Body.String())
	}
}

func TestSetupHostNetworkHandler_Failure(t *testing.T) {
	origSetup := setupHostNetworking
	t.Cleanup(func() {
		setupHostNetworking = origSetup
	})

	setupHostNetworking = func(_ []types.CanDeviceConfig, _ []types.TapConfig) (*container.HostNetworkSetupResult, error) {
		return &container.HostNetworkSetupResult{Items: []container.HostNetworkSetupItem{{Resource: "br0", Action: "reused"}}}, fmt.Errorf("setup failed")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/network/setup", HandleSetupHostNetwork())

	req := httptest.NewRequest(http.MethodPost, "/api/network/setup", bytes.NewBufferString(`{"can_devices":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`setup failed`)) {
		t.Fatalf("expected error message in body, got=%s", rec.Body.String())
	}
}

func TestNetworkBenchmarkHandler(t *testing.T) {
	resetStore()

	tmpDir := t.TempDir()
	pcapFile := filepath.Join(tmpDir, "capture.pcap")
	if err := os.WriteFile(pcapFile, bytes.Repeat([]byte("A"), 250000), 0644); err != nil {
		t.Fatalf("write pcap fixture: %v", err)
	}

	storeMu.Lock()
	sessions["s-bench"] = types.Session{
		ID:           "s-bench",
		BinaryID:     "b1",
		PCAPEnabled:  true,
		PCAPFilePath: pcapFile,
		Uptime:       5,
		CreatedAt:    time.Now().UTC().Add(-10 * time.Second),
		UpdatedAt:    time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/network/benchmark", HandleNetworkBenchmark())

	req := httptest.NewRequest(http.MethodPost, "/api/network/benchmark", bytes.NewBufferString(`{"session_id":"s-bench"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"estimated_mbps"`)) {
		t.Fatalf("expected throughput in response, got=%s", rec.Body.String())
	}
}

func TestNetworkBenchmarkHandler_Validation(t *testing.T) {
	resetStore()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/network/benchmark", HandleNetworkBenchmark())

	req := httptest.NewRequest(http.MethodPost, "/api/network/benchmark", bytes.NewBufferString(`{"session_id":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestNetworkBenchmarkHandler_PCAPDisabled(t *testing.T) {
	resetStore()

	storeMu.Lock()
	sessions["s-disabled"] = types.Session{
		ID:           "s-disabled",
		BinaryID:     "b1",
		PCAPEnabled:  false,
		PCAPFilePath: "/tmp/placeholder.pcap",
		CreatedAt:    time.Now().UTC().Add(-5 * time.Second),
		UpdatedAt:    time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/network/benchmark", HandleNetworkBenchmark())

	req := httptest.NewRequest(http.MethodPost, "/api/network/benchmark", bytes.NewBufferString(`{"session_id":"s-disabled"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("PCAP capture is disabled")) {
		t.Fatalf("expected disabled message, got=%s", rec.Body.String())
	}
}

func TestNetworkBenchmarkHandler_EmptyCapture(t *testing.T) {
	resetStore()

	tmpDir := t.TempDir()
	pcapFile := filepath.Join(tmpDir, "empty.pcap")
	if err := os.WriteFile(pcapFile, []byte{}, 0644); err != nil {
		t.Fatalf("write empty pcap fixture: %v", err)
	}

	storeMu.Lock()
	sessions["s-empty"] = types.Session{
		ID:           "s-empty",
		BinaryID:     "b1",
		PCAPEnabled:  true,
		PCAPFilePath: pcapFile,
		Uptime:       3,
		CreatedAt:    time.Now().UTC().Add(-3 * time.Second),
		UpdatedAt:    time.Now().UTC(),
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/network/benchmark", HandleNetworkBenchmark())

	req := httptest.NewRequest(http.MethodPost, "/api/network/benchmark", bytes.NewBufferString(`{"session_id":"s-empty"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"empty_capture":true`)) {
		t.Fatalf("expected empty_capture=true, got=%s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"estimated_mbps":0`)) {
		t.Fatalf("expected estimated_mbps=0 for empty capture, got=%s", rec.Body.String())
	}
}

func TestHandleSSE_PrefersContainerLogsWhenSessionBackendsExist(t *testing.T) {
	resetStore()
	mgr := &sseProbeContainerManager{fakeContainerManager: newFakeContainerManager()}
	mux := uart.NewMultiplexer("", 128)

	storeMu.Lock()
	sessions["sse-mux"] = types.Session{
		ID:          "sse-mux",
		BinaryID:    "b1",
		ContainerID: "cid-sse-mux",
		State:       types.SessionStateRunning,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	storeMu.Unlock()

	if err := mux.RegisterSessionBackends("sse-mux", []string{"/tmp/sse-mux-uart0.fifo"}); err != nil {
		t.Fatalf("register session backends: %v", err)
	}

	h := HandleSSE(mgr, mux)
	req := httptest.NewRequest(http.MethodGet, "/api/sse?session=sse-mux", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	h.ServeHTTP(rec, req)

	if mgr.streamCalls == 0 {
		t.Fatalf("expected container logs path to be used when session backends are registered")
	}
}

func TestHandleSSE_UsesContainerLogsWhenNoMuxBackends(t *testing.T) {
	resetStore()
	mgr := &sseProbeContainerManager{fakeContainerManager: newFakeContainerManager()}
	mux := uart.NewMultiplexer("", 128)

	storeMu.Lock()
	sessions["sse-logs"] = types.Session{
		ID:          "sse-logs",
		BinaryID:    "b1",
		ContainerID: "cid-sse-logs",
		State:       types.SessionStateRunning,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	storeMu.Unlock()

	h := HandleSSE(mgr, mux)
	req := httptest.NewRequest(http.MethodGet, "/api/sse?session=sse-logs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if mgr.streamCalls == 0 {
		t.Fatalf("expected container logs path to be used when no mux backends are registered")
	}
}

func TestDebugWebSocketProxy_BidirectionalStream(t *testing.T) {
	resetStore()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock gdbserver: %v", err)
	}
	defer ln.Close()

	errCh := make(chan error, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		n, readErr := conn.Read(buf)
		if readErr != nil {
			errCh <- readErr
			return
		}
		_, writeErr := conn.Write(append([]byte("ACK:"), buf[:n]...))
		errCh <- writeErr
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	storeMu.Lock()
	sessions["s-debug-ws"] = types.Session{
		ID:          "s-debug-ws",
		BinaryID:    "b1",
		State:       types.SessionStateRunning,
		ContainerID: "container-xyz",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		DebugConfig: &types.DebugConfig{Enabled: true, Port: tcpAddr.Port, WaitForGDB: true},
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{id}/debug/ws", HandleDebugWebSocketProxy())
	// Wrap mux to inject user identity so requireAuthenticated passes.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = withUserIdentity(r)
		mux.ServeHTTP(w, r)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/sessions/s-debug-ws/debug/ws"
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer wsConn.Close()

	if err := wsConn.WriteMessage(websocket.BinaryMessage, []byte("qSupported")); err != nil {
		t.Fatalf("write ws message: %v", err)
	}

	_, payload, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws message: %v", err)
	}
	if string(payload) != "ACK:qSupported" {
		t.Fatalf("unexpected proxy payload: %q", string(payload))
	}

	if backendErr := <-errCh; backendErr != nil {
		t.Fatalf("mock gdbserver error: %v", backendErr)
	}
}

func TestDebugWebSocketProxy_RejectsWhenDebugDisabled(t *testing.T) {
	resetStore()

	storeMu.Lock()
	sessions["s-no-debug"] = types.Session{
		ID:          "s-no-debug",
		BinaryID:    "b1",
		State:       types.SessionStateRunning,
		ContainerID: "container-xyz",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
		DebugConfig: &types.DebugConfig{Enabled: false, Port: 3333},
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{id}/debug/ws", HandleDebugWebSocketProxy())

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s-no-debug/debug/ws", nil)
	req = withUserIdentity(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Debugging is not enabled")) {
		t.Fatalf("expected debug disabled message, got: %s", rec.Body.String())
	}
}

func TestDebugBreakpointAndStackHandlers(t *testing.T) {
	resetStore()

	origRun := runGDBBatch
	t.Cleanup(func() {
		runGDBBatch = origRun
	})

	runGDBBatch = func(_ string, _ string, commands []string) (string, error) {
		joined := strings.Join(commands, "\n")
		switch {
		case strings.Contains(joined, "info break"):
			return "Num     Type           Disp Enb Address    What\n1       breakpoint     keep y   0x0000     in main at src/main.c:42\n", nil
		case strings.Contains(joined, "bt"):
			return "#0  main () at src/main.c:42\n#1  z_thread_entry () at kernel/thread.c:100\n", nil
		default:
			return "", nil
		}
	}

	storeMu.Lock()
	binaries["b1"] = types.Binary{ID: "b1", FilePath: "/tmp/app.elf", UploadedAt: time.Now().UTC()}
	sessions["s1"] = types.Session{
		ID:        "s1",
		BinaryID:  "b1",
		State:     types.SessionStateRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		DebugConfig: &types.DebugConfig{
			Enabled: true,
			Port:    4444,
		},
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{id}/debug/breakpoints", HandleListDebugBreakpoints())
	mux.HandleFunc("POST /api/sessions/{id}/debug/breakpoints", HandleAddDebugBreakpoint())
	mux.HandleFunc("DELETE /api/sessions/{id}/debug/breakpoints/{number}", HandleDeleteDebugBreakpoint())
	mux.HandleFunc("GET /api/sessions/{id}/debug/stack", HandleGetDebugStackTrace())

	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/debug/breakpoints", nil)
	listReq = withUserIdentity(listReq)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list breakpoints status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if !bytes.Contains(listRec.Body.Bytes(), []byte(`"number":"1"`)) {
		t.Fatalf("expected breakpoint number in response: %s", listRec.Body.String())
	}

	addReq := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/debug/breakpoints", bytes.NewBufferString(`{"location":"main"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addReq = withUserIdentity(addReq)
	addRec := httptest.NewRecorder()
	mux.ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add breakpoint status=%d body=%s", addRec.Code, addRec.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/sessions/s1/debug/breakpoints/1", nil)
	delReq = withUserIdentity(delReq)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete breakpoint status=%d body=%s", delRec.Code, delRec.Body.String())
	}

	stackReq := httptest.NewRequest(http.MethodGet, "/api/sessions/s1/debug/stack", nil)
	stackReq = withUserIdentity(stackReq)
	stackRec := httptest.NewRecorder()
	mux.ServeHTTP(stackRec, stackReq)
	if stackRec.Code != http.StatusOK {
		t.Fatalf("stack trace status=%d body=%s", stackRec.Code, stackRec.Body.String())
	}
	if !bytes.Contains(stackRec.Body.Bytes(), []byte(`"function":"main ()"`)) {
		t.Fatalf("expected stack frame in response: %s", stackRec.Body.String())
	}
}

func TestDebugBreakpointInjectionRejected(t *testing.T) {
	resetStore()

	origRun := runGDBBatch
	t.Cleanup(func() { runGDBBatch = origRun })
	runGDBBatch = func(_ string, _ string, _ []string) (string, error) {
		t.Error("runGDBBatch should not be called for injected input")
		return "", nil
	}

	storeMu.Lock()
	binaries["b1"] = types.Binary{ID: "b1", FilePath: "/tmp/app.elf", UploadedAt: time.Now().UTC()}
	sessions["s1"] = types.Session{
		ID:        "s1",
		BinaryID:  "b1",
		State:     types.SessionStateRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		DebugConfig: &types.DebugConfig{
			Enabled: true,
			Port:    4444,
		},
	}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/debug/breakpoints", HandleAddDebugBreakpoint())
	mux.HandleFunc("DELETE /api/sessions/{id}/debug/breakpoints/{number}", HandleDeleteDebugBreakpoint())

	injectionCases := []string{
		`main; shell rm -rf /`,
		`main" -ex "shell id`,
		`main && whoami`,
		`$(whoami)`,
		"main\nshell id",
		`main | cat /etc/passwd`,
	}
	for _, loc := range injectionCases {
		body := bytes.NewBufferString(`{"location":"` + loc + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/debug/breakpoints", body)
		req.Header.Set("Content-Type", "application/json")
		req = withUserIdentity(req)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("location %q: expected 400, got %d body=%s", loc, rec.Code, rec.Body.String())
		}
	}

	invalidNumbers := []string{"abc", "1a", "0x1234"}
	for _, num := range invalidNumbers {
		req := httptest.NewRequest(http.MethodDelete, "/api/sessions/s1/debug/breakpoints/"+num, nil)
		req = withUserIdentity(req)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("breakpoint number %q: expected 400, got %d body=%s", num, rec.Code, rec.Body.String())
		}
	}
}

func TestCoverageDownloadAndConfig(t *testing.T) {
	resetStore()

	storeMu.Lock()
	binaries["b1"] = types.Binary{ID: "b1", Name: "app.elf", FilePath: "/tmp/app.elf", UploadedAt: time.Now().UTC()}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", HandleCreateSession(nil))
	mux.HandleFunc("PATCH /api/sessions/{id}", HandleUpdateSession(nil))
	mux.HandleFunc("GET /api/sessions/{id}/coverage", HandleDownloadCoverage())

	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"binary_id":"b1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create session status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	var created struct {
		Success bool          `json:"success"`
		Data    types.Session `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+created.Data.ID, bytes.NewBufferString(`{"coverage_enabled":true}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	mux.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch coverage status=%d body=%s", patchRec.Code, patchRec.Body.String())
	}

	storeMu.RLock()
	session := sessions[created.Data.ID]
	storeMu.RUnlock()
	if !session.CoverageEnabled {
		t.Fatalf("expected coverage_enabled true")
	}
	if strings.TrimSpace(session.CoverageDir) == "" {
		t.Fatalf("expected coverage directory to be configured")
	}

	if err := os.MkdirAll(session.CoverageDir, 0755); err != nil {
		t.Fatalf("mkdir coverage dir: %v", err)
	}
	coverageFile := filepath.Join(session.CoverageDir, "sample.gcda")
	if err := os.WriteFile(coverageFile, []byte("coverage-data"), 0644); err != nil {
		t.Fatalf("write coverage file: %v", err)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+created.Data.ID+"/coverage", nil)
	downloadReq = withUserIdentity(downloadReq)
	downloadRec := httptest.NewRecorder()
	mux.ServeHTTP(downloadRec, downloadReq)

	if downloadRec.Code != http.StatusOK {
		t.Fatalf("download coverage status=%d body=%s", downloadRec.Code, downloadRec.Body.String())
	}
	if ct := downloadRec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("expected application/gzip, got %q", ct)
	}
	if !bytes.Contains([]byte(downloadRec.Header().Get("Content-Disposition")), []byte("coverage.tar.gz")) {
		t.Fatalf("unexpected content-disposition: %s", downloadRec.Header().Get("Content-Disposition"))
	}
	if downloadRec.Body.Len() == 0 {
		t.Fatalf("expected non-empty coverage archive")
	}
}

func TestSanitizerDownloadAndConfig(t *testing.T) {
	resetStore()

	storeMu.Lock()
	binaries["b1"] = types.Binary{ID: "b1", Name: "app.elf", FilePath: "/tmp/app.elf", UploadedAt: time.Now().UTC()}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", HandleCreateSession(nil))
	mux.HandleFunc("PATCH /api/sessions/{id}", HandleUpdateSession(nil))
	mux.HandleFunc("GET /api/sessions/{id}/sanitizers", HandleDownloadSanitizers())

	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"binary_id":"b1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create session status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	var created struct {
		Success bool          `json:"success"`
		Data    types.Session `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+created.Data.ID, bytes.NewBufferString(`{"asan_enabled":true,"ubsan_enabled":true}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	mux.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch sanitizer status=%d body=%s", patchRec.Code, patchRec.Body.String())
	}

	storeMu.RLock()
	session := sessions[created.Data.ID]
	storeMu.RUnlock()
	if !session.AsanEnabled || !session.UbsanEnabled {
		t.Fatalf("expected both sanitizers enabled")
	}
	if strings.TrimSpace(session.SanitizerDir) == "" {
		t.Fatalf("expected sanitizer directory to be configured")
	}

	if err := os.MkdirAll(session.SanitizerDir, 0755); err != nil {
		t.Fatalf("mkdir sanitizer dir: %v", err)
	}
	asanFile := filepath.Join(session.SanitizerDir, "asan.log")
	ubsanFile := filepath.Join(session.SanitizerDir, "ubsan.log")
	if err := os.WriteFile(asanFile, []byte("asan-report"), 0644); err != nil {
		t.Fatalf("write asan file: %v", err)
	}
	if err := os.WriteFile(ubsanFile, []byte("ubsan-report"), 0644); err != nil {
		t.Fatalf("write ubsan file: %v", err)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+created.Data.ID+"/sanitizers", nil)
	downloadReq = withUserIdentity(downloadReq)
	downloadRec := httptest.NewRecorder()
	mux.ServeHTTP(downloadRec, downloadReq)

	if downloadRec.Code != http.StatusOK {
		t.Fatalf("download sanitizer status=%d body=%s", downloadRec.Code, downloadRec.Body.String())
	}
	if ct := downloadRec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("expected application/gzip, got %q", ct)
	}
	if !bytes.Contains([]byte(downloadRec.Header().Get("Content-Disposition")), []byte("sanitizers.tar.gz")) {
		t.Fatalf("unexpected content-disposition: %s", downloadRec.Header().Get("Content-Disposition"))
	}
	if downloadRec.Body.Len() == 0 {
		t.Fatalf("expected non-empty sanitizer archive")
	}
}

func TestSanitizerReportParsing(t *testing.T) {
	resetStore()

	storeMu.Lock()
	binaries["b1"] = types.Binary{ID: "b1", Name: "app.elf", FilePath: "/tmp/app.elf", UploadedAt: time.Now().UTC()}
	storeMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", HandleCreateSession(nil))
	mux.HandleFunc("PATCH /api/sessions/{id}", HandleUpdateSession(nil))
	mux.HandleFunc("GET /api/sessions/{id}/sanitizers/report", HandleGetSanitizerReport())

	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"binary_id":"b1"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create session status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	var created struct {
		Success bool          `json:"success"`
		Data    types.Session `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+created.Data.ID, bytes.NewBufferString(`{"asan_enabled":true,"ubsan_enabled":true}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	mux.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch sanitizer status=%d body=%s", patchRec.Code, patchRec.Body.String())
	}

	storeMu.RLock()
	session := sessions[created.Data.ID]
	storeMu.RUnlock()

	if err := os.MkdirAll(session.SanitizerDir, 0755); err != nil {
		t.Fatalf("mkdir sanitizer dir: %v", err)
	}
	asanLog := strings.Join([]string{
		"==1==ERROR: AddressSanitizer: heap-use-after-free on address 0x1234",
		"    #0 0x123 in foo /workspace/src/foo.c:42:7",
	}, "\n")
	ubsanLog := "src/bar.c:18:5: runtime error: signed integer overflow: 2147483647 + 1 cannot be represented in type 'int'"
	if err := os.WriteFile(filepath.Join(session.SanitizerDir, "asan.log"), []byte(asanLog), 0644); err != nil {
		t.Fatalf("write asan log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(session.SanitizerDir, "ubsan.log"), []byte(ubsanLog), 0644); err != nil {
		t.Fatalf("write ubsan log: %v", err)
	}

	reportReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+created.Data.ID+"/sanitizers/report", nil)
	reportReq = withUserIdentity(reportReq)
	reportRec := httptest.NewRecorder()
	mux.ServeHTTP(reportRec, reportReq)

	if reportRec.Code != http.StatusOK {
		t.Fatalf("sanitizer report status=%d body=%s", reportRec.Code, reportRec.Body.String())
	}
	if !bytes.Contains(reportRec.Body.Bytes(), []byte(`"tool":"asan"`)) {
		t.Fatalf("expected asan finding in response: %s", reportRec.Body.String())
	}
	if !bytes.Contains(reportRec.Body.Bytes(), []byte(`"tool":"ubsan"`)) {
		t.Fatalf("expected ubsan finding in response: %s", reportRec.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+created.Data.ID+"/sanitizers/report?tool=ubsan&limit=1", nil)
	filteredReq = withUserIdentity(filteredReq)
	filteredRec := httptest.NewRecorder()
	mux.ServeHTTP(filteredRec, filteredReq)
	if filteredRec.Code != http.StatusOK {
		t.Fatalf("filtered report status=%d body=%s", filteredRec.Code, filteredRec.Body.String())
	}
	if !bytes.Contains(filteredRec.Body.Bytes(), []byte(`"total":1`)) {
		t.Fatalf("expected limited result count in response: %s", filteredRec.Body.String())
	}
}

func TestParseSanitizerLine_UBSanWithColumn(t *testing.T) {
	finding, ok := parseSanitizerLine("ubsan", "ubsan.log", "src/main.c:77:9: runtime error: division by zero")
	if !ok {
		t.Fatalf("expected parser to detect UBSan line")
	}
	if finding.Tool != "ubsan" || finding.File != "src/main.c" || finding.Line != 77 || finding.Column != 9 {
		t.Fatalf("unexpected finding parsed: %+v", finding)
	}
}

func TestEnsureWritableSessionDir_Fallback(t *testing.T) {
	tmp := t.TempDir()
	blockingFile := filepath.Join(tmp, "block")
	if err := os.WriteFile(blockingFile, []byte("x"), 0644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}

	preferred := filepath.Join(blockingFile, "child")
	fallback := filepath.Join(tmp, "fallback", "session-1")

	resolved, err := ensureWritableSessionDir(preferred, fallback)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if resolved != fallback {
		t.Fatalf("expected fallback path %q, got %q", fallback, resolved)
	}
	if _, statErr := os.Stat(fallback); statErr != nil {
		t.Fatalf("expected fallback directory to exist: %v", statErr)
	}
}
