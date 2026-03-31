package handlers

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beriberikix/zephyr-simulator-server/internal/container"
	"github.com/beriberikix/zephyr-simulator-server/internal/types"
	"github.com/beriberikix/zephyr-simulator-server/internal/uart"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/gorilla/websocket"
)

// Response is a standard JSON response wrapper
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type createSessionRequest struct {
	BinaryID       string `json:"binary_id"`
	Seed           uint64 `json:"seed"`
	UseRealTime    bool   `json:"use_real_time"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type updateSessionRequest struct {
	Seed            *uint64                  `json:"seed,omitempty"`
	UseRealTime     *bool                    `json:"use_real_time,omitempty"`
	TimeoutSeconds  *int                     `json:"timeout_seconds,omitempty"`
	PCAPEnabled     *bool                    `json:"pcap_enabled,omitempty"`     // Enable PCAP capture
	CoverageEnabled *bool                    `json:"coverage_enabled,omitempty"` // Enable coverage collection
	AsanEnabled     *bool                    `json:"asan_enabled,omitempty"`     // Enable ASan report collection
	UbsanEnabled    *bool                    `json:"ubsan_enabled,omitempty"`    // Enable UBSan report collection
	CanDevices      *[]types.CanDeviceConfig `json:"can_devices,omitempty"`      // CAN interfaces
	TapInterfaces   *[]types.TapConfig       `json:"tap_interfaces,omitempty"`   // TAP interfaces
	BluetoothConfig *types.BluetoothConfig   `json:"bluetooth_config,omitempty"` // Bluetooth HCI
	UARTForwarding  *types.UARTForwardingConfig `json:"uart_forwarding,omitempty"` // UART-based network forwarding
	DebugConfig     *types.DebugConfig       `json:"debug_config,omitempty"`     // GDB debugging config
}

type debugTargetResponse struct {
	Enabled   bool   `json:"enabled"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	State     string `json:"state"`
	Container string `json:"container,omitempty"`
}

type addBreakpointRequest struct {
	Location string `json:"location"`
}

type debugBreakpoint struct {
	Number   string `json:"number"`
	Location string `json:"location"`
	Enabled  bool   `json:"enabled"`
}

type stackFrame struct {
	Index    int    `json:"index"`
	Function string `json:"function"`
	Location string `json:"location"`
}

type sanitizerFinding struct {
	Tool    string `json:"tool"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Summary string `json:"summary"`
	Source  string `json:"source"`
	Raw     string `json:"raw"`
}

type sanitizerReportResponse struct {
	SessionID string                      `json:"session_id"`
	Total     int                         `json:"total"`
	ByTool    map[string]int              `json:"by_tool"`
	Findings  []sanitizerFinding          `json:"findings"`
	Filters   map[string]string           `json:"filters"`
	Generated time.Time                   `json:"generated_at"`
}

var (
	storeMu  sync.RWMutex
	binaries = map[string]types.Binary{}
	sessions = map[string]types.Session{}
)

var setupHostNetworking = container.SetupHostNetworking

var registerSessionUARTBackends = func(_ *types.Session) error { return nil }
var unregisterSessionUARTBackends = func(_ string) {}

// SetUARTLifecycleHooks wires session lifecycle events to UART backend registration.
func SetUARTLifecycleHooks(register func(*types.Session) error, unregister func(string)) {
	if register != nil {
		registerSessionUARTBackends = register
	}
	if unregister != nil {
		unregisterSessionUARTBackends = unregister
	}
}

// gdbLocationRe matches valid GDB breakpoint locations:
// function names, file:line (e.g. src/main.c:42), C++ qualified names, hex addresses (*0x1234).
// Intentionally excludes whitespace, quotes, semicolons, and shell metacharacters.
var gdbLocationRe = regexp.MustCompile(`^[a-zA-Z0-9_.:/*\-]+$`)

// gdbBreakpointNumberRe matches only a plain positive integer (GDB breakpoint number).
var gdbBreakpointNumberRe = regexp.MustCompile(`^[0-9]+$`)

func isContainerNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no such container") ||
		(strings.Contains(s, "container") && strings.Contains(s, "not found"))
}

var runGDBBatch = func(binaryPath, targetAddr string, commands []string) (string, error) {
	args := []string{
		"-q",
		"--batch",
		"-nx",
		"-ex", "set pagination off",
		"-ex", "set confirm off",
		"-ex", fmt.Sprintf("file %s", binaryPath),
		"-ex", fmt.Sprintf("target remote %s", targetAddr),
	}

	for _, cmd := range commands {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		args = append(args, "-ex", cmd)
	}

	args = append(args, "-ex", "detach")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	proc := exec.CommandContext(ctx, "gdb", args...)
	out, err := proc.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("gdb command timeout")
	}
	if err != nil {
		return string(out), fmt.Errorf("gdb command failed: %w", err)
	}
	return string(out), nil
}

var runDockerCLI = func(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}

func volumeExists(ctx context.Context, volumeName string) (bool, error) {
	out, err := runDockerCLI(ctx, "volume", "inspect", volumeName)
	if err != nil {
		s := strings.ToLower(string(out) + " " + err.Error())
		if strings.Contains(s, "no such volume") || strings.Contains(s, "not found") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func cloneDockerVolume(ctx context.Context, srcVolume, dstVolume string) error {
	if _, err := runDockerCLI(ctx, "volume", "create", dstVolume); err != nil {
		return err
	}

	// Copy persisted runtime state (flash/eeprom/etc.) from source to destination volume.
	_, err := runDockerCLI(
		ctx,
		"run", "--rm",
		"-v", srcVolume+":/from:ro",
		"-v", dstVolume+":/to",
		"alpine:3.19",
		"sh", "-lc", "cp -a /from/. /to/",
	)
	return err
}

func cloneSessionRuntimeState(ctx context.Context, srcSessionID, dstSessionID string) error {
	if strings.TrimSpace(srcSessionID) == "" || strings.TrimSpace(dstSessionID) == "" || srcSessionID == dstSessionID {
		return nil
	}

	prefixes := []string{"zephyr-session-", "zephyr-session-tmp-"}
	for _, prefix := range prefixes {
		srcVolume := prefix + srcSessionID
		dstVolume := prefix + dstSessionID

		exists, err := volumeExists(ctx, srcVolume)
		if err != nil {
			return fmt.Errorf("inspect volume %s: %w", srcVolume, err)
		}
		if !exists {
			continue
		}

		if err := cloneDockerVolume(ctx, srcVolume, dstVolume); err != nil {
			return fmt.Errorf("clone volume %s -> %s: %w", srcVolume, dstVolume, err)
		}
	}

	return nil
}

var debugWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	},
}

type setupHostNetworkRequest struct {
	CanDevices    []types.CanDeviceConfig `json:"can_devices,omitempty"`
	TapInterfaces []types.TapConfig       `json:"tap_interfaces,omitempty"`
}

type networkBenchmarkRequest struct {
	SessionID       string `json:"session_id"`
	DurationSeconds int    `json:"duration_seconds,omitempty"`
}

// ContainerManager abstracts container operations used by handlers.
type ContainerManager interface {
	CreateContainer(ctx context.Context, session *types.Session, binary *types.Binary) (string, error)
	StartContainer(ctx context.Context, containerID string) error
	StopContainer(ctx context.Context, containerID string) error
	PauseContainer(ctx context.Context, containerID string) error
	ResumeContainer(ctx context.Context, containerID string) error
	RemoveContainer(ctx context.Context, containerID string) error
	GetContainerStatus(ctx context.Context, containerID string) (types.SessionState, error)
	StreamContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error)
	IsContainerTTY(ctx context.Context, containerID string) (bool, error)
}

// HandleHealth returns server health status
func HandleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data: map[string]string{
				"status": "ok",
			},
		})
	}
}

// HandleSetupHostNetwork prepares host network resources for advanced networking features.
func HandleSetupHostNetwork() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req setupHostNetworkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Invalid request body: %v", err)})
			return
		}

		result, err := setupHostNetworking(req.CanDevices, req.TapInterfaces)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Data: result, Error: err.Error()})
			return
		}

		json.NewEncoder(w).Encode(Response{Success: true, Data: result})
	}
}

// HandleNetworkBenchmark returns a lightweight throughput estimate from session PCAP artifacts.
func HandleNetworkBenchmark() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		var req networkBenchmarkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Invalid request body: %v", err)})
			return
		}

		req.SessionID = strings.TrimSpace(req.SessionID)
		if req.SessionID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "session_id is required"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[req.SessionID]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if strings.TrimSpace(session.PCAPFilePath) == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session has no PCAP file configured"})
			return
		}

		if !session.PCAPEnabled {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "PCAP capture is disabled for this session"})
			return
		}

		st, err := os.Stat(session.PCAPFilePath)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("PCAP file not found: %v", err)})
			return
		}

		duration := req.DurationSeconds
		if duration <= 0 {
			if session.Uptime > 0 {
				duration = int(session.Uptime)
			} else {
				window := session.UpdatedAt.Sub(session.CreatedAt)
				if window > 0 {
					duration = int(window.Seconds())
				}
			}
		}
		if duration <= 0 {
			duration = 1
		}

		throughputMbps := 0.0
		if st.Size() > 0 {
			throughputMbps = (float64(st.Size()) * 8.0) / (float64(duration) * 1_000_000.0)
		}

		json.NewEncoder(w).Encode(Response{Success: true, Data: map[string]interface{}{
			"session_id": req.SessionID,
			"pcap_path": session.PCAPFilePath,
			"pcap_bytes": st.Size(),
			"empty_capture": st.Size() == 0,
			"duration_seconds": duration,
			"estimated_mbps": throughputMbps,
			"generated_at": time.Now().UTC(),
		}})
	}
}

// HandleUploadBinary handles binary file uploads
func HandleUploadBinary(mgr ContainerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Hard cap request body size to limit upload abuse.
		r.Body = http.MaxBytesReader(w, r.Body, 40<<20)

		// Parse multipart form (32MB max)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{
				Success: false,
				Error:   fmt.Sprintf("Parse form: %v", err),
			})
			return
		}

		file, handler, err := r.FormFile("binary")
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{
				Success: false,
				Error:   "Missing binary file",
			})
			return
		}
		defer file.Close()

		// Save uploaded file
		uploadDir := "/tmp/binaries"
		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{
				Success: false,
				Error:   fmt.Sprintf("Create upload dir: %v", err),
			})
			return
		}

		// Some clients may send full paths in multipart filename; normalize to basename.
		rawName := handler.Filename
		safeName := filepath.Base(strings.ReplaceAll(rawName, "\\", "/"))
		if safeName == "" || safeName == "." || safeName == "/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{
				Success: false,
				Error:   "Invalid upload filename",
			})
			return
		}

		filePath := filepath.Join(uploadDir, safeName)
		if _, err := os.Stat(filePath); err == nil {
			ext := filepath.Ext(safeName)
			base := strings.TrimSuffix(safeName, ext)
			safeName = fmt.Sprintf("%s-%d%s", base, time.Now().UnixMilli(), ext)
			filePath = filepath.Join(uploadDir, safeName)
		}

		dst, err := os.Create(filePath)
		if err != nil {
			log.Printf("upload create file failed: name=%q path=%q err=%v", rawName, filePath, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{
				Success: false,
				Error:   fmt.Sprintf("Create file: %v", err),
			})
			return
		}
		defer dst.Close()

		if _, err := dst.ReadFrom(file); err != nil {
			log.Printf("upload write file failed: path=%q err=%v", filePath, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{
				Success: false,
				Error:   fmt.Sprintf("Write file: %v", err),
			})
			return
		}

		// Analyze binary
		analyzer := container.NewBinaryAnalyzer()
		binary, err := analyzer.Analyze(filePath)
		if err != nil {
			log.Printf("upload analyze failed: name=%q path=%q err=%v", safeName, filePath, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{
				Success: false,
				Error:   fmt.Sprintf("Analyze binary: %v", err),
			})
			return
		}

		binary.ID = safeName // Temporary ID
		binary.UploadedAt = time.Now().UTC()

		storeMu.Lock()
		binaries[binary.ID] = *binary
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after binary upload: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    binary,
		})
	}
}

// HandleListBinaries lists all uploaded binaries
func HandleListBinaries() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		storeMu.RLock()
		list := make([]types.Binary, 0, len(binaries))
		for _, b := range binaries {
			list = append(list, b)
		}
		storeMu.RUnlock()

		sort.Slice(list, func(i, j int) bool {
			return list[i].UploadedAt.After(list[j].UploadedAt)
		})

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    list,
		})
	}
}

// HandleGetBinary retrieves a specific binary
func HandleGetBinary() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing binary id"})
			return
		}

		storeMu.RLock()
		binary, ok := binaries[id]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Binary not found"})
			return
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    binary,
		})
	}
}

// HandleDeleteBinary deletes a binary
func HandleDeleteBinary() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing binary id"})
			return
		}

		storeMu.Lock()
		binary, ok := binaries[id]
		if !ok {
			storeMu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Binary not found"})
			return
		}

		for _, s := range sessions {
			if s.BinaryID == id && s.State != types.SessionStateStopped {
				storeMu.Unlock()
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(Response{Success: false, Error: "Binary is in use by an active session"})
				return
			}
		}

		delete(binaries, id)
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after binary delete: %v", err)
		}

		if binary.FilePath != "" {
			_ = os.Remove(binary.FilePath)
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
		})
	}
}

// HandleCreateSession creates a new emulator session
// maxAnonSessions is the maximum number of concurrent sessions allowed for anonymous callers.
const maxAnonSessions = 2

func HandleCreateSession(mgr ContainerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Method not allowed"})
			return
		}

		var req createSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Invalid request body: %v", err)})
			return
		}
		if req.BinaryID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "binary_id is required"})
			return
		}

		caller := GetIdentity(r)
		if caller.Type == OwnerTypeAnonymous {
			storeMu.RLock()
			anonCount := 0
			for _, s := range sessions {
				if s.OwnerType == string(OwnerTypeAnonymous) && s.OwnerID == caller.ID {
					anonCount++
				}
			}
			storeMu.RUnlock()
			if anonCount >= maxAnonSessions {
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Anonymous users are limited to %d concurrent sessions. Please log in for unlimited access.", maxAnonSessions)})
				return
			}
		}

		storeMu.RLock()
		_, ok := binaries[req.BinaryID]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Binary not found"})
			return
		}

		now := time.Now().UTC()
		if req.TimeoutSeconds <= 0 {
			req.TimeoutSeconds = 300
		}
		sessionID := fmt.Sprintf("session-%d", now.UnixMilli())

		// Generate PCAP file path for network capture.
		pcapDir := getPCAPBaseDir()
		pcapPath := filepath.Join(pcapDir, sessionID+".pcap")

		coverageDir := getCoverageBaseDir()
		sessionCoverageDir := filepath.Join(coverageDir, fmt.Sprintf("session-%d", now.UnixMilli()))
		_ = os.MkdirAll(sessionCoverageDir, 0755)

		sanitizerDir := getSanitizerBaseDir()
		sessionSanitizerDir := filepath.Join(sanitizerDir, fmt.Sprintf("session-%d", now.UnixMilli()))
		_ = os.MkdirAll(sessionSanitizerDir, 0755)

		session := types.Session{
			ID:              sessionID,
			BinaryID:        req.BinaryID,
			State:           types.SessionStateStopped,
			Seed:            req.Seed,
			UseRealTime:     req.UseRealTime,
			CreatedAt:       now,
			UpdatedAt:       now,
			TimeoutSeconds:  req.TimeoutSeconds,
			PCAPFilePath:    pcapPath,
			PCAPEnabled:     false, // Disabled by default, enabled via PATCH endpoint
			CoverageEnabled: false,
			CoverageDir:     sessionCoverageDir,
			AsanEnabled:     false,
			UbsanEnabled:    false,
			SanitizerDir:    sessionSanitizerDir,
			NetworkConfig:   &types.NetworkConfig{EnableIsolation: true},
			CanDevices:      []types.CanDeviceConfig{},
			TapInterfaces:   []types.TapConfig{},
			DebugConfig:     &types.DebugConfig{Enabled: false, Port: 3333, WaitForGDB: false},
		}

		session.OwnerType = string(caller.Type)
		session.OwnerID = caller.ID


		storeMu.Lock()
		sessions[session.ID] = session
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after session create: %v", err)
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    session,
		})
	}
}

// HandleListSessions lists all sessions
func HandleListSessions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		caller := GetIdentity(r)
		storeMu.RLock()
		list := make([]types.Session, 0, len(sessions))
		for _, s := range sessions {
			if canAccessSession(caller, s.OwnerType, s.OwnerID) {
				list = append(list, s)
			}
		}
		storeMu.RUnlock()

		sort.Slice(list, func(i, j int) bool {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		})

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    list,
		})
	}
}

// HandleGetSession retrieves a specific session
func HandleGetSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[id]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if requireSessionOwner(w, r, session.OwnerType, session.OwnerID) {
			return
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    session,
		})
	}
}

// HandleUpdateSession updates session configuration
func HandleUpdateSession(mgr ContainerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		var req updateSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Invalid request body: %v", err)})
			return
		}

		storeMu.Lock()
		session, ok := sessions[id]
		if !ok {
			storeMu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if req.Seed != nil {
			session.Seed = *req.Seed
		}
		if req.UseRealTime != nil {
			session.UseRealTime = *req.UseRealTime
		}
		if req.TimeoutSeconds != nil && *req.TimeoutSeconds > 0 {
			session.TimeoutSeconds = *req.TimeoutSeconds
		}

		// Update networking configuration.
		if req.PCAPEnabled != nil {
			session.PCAPEnabled = *req.PCAPEnabled
		}
		if req.CoverageEnabled != nil {
			session.CoverageEnabled = *req.CoverageEnabled
			if session.CoverageEnabled {
				dir := strings.TrimSpace(session.CoverageDir)
				if dir == "" {
					dir = filepath.Join(getCoverageBaseDir(), session.ID)
				}
				resolved, err := ensureWritableSessionDir(dir, filepath.Join(os.TempDir(), "coverage", session.ID))
				if err != nil {
					storeMu.Unlock()
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Failed to prepare coverage directory: %v", err)})
					return
				}
				session.CoverageDir = resolved
			}
		}
		if req.AsanEnabled != nil {
			session.AsanEnabled = *req.AsanEnabled
		}
		if req.UbsanEnabled != nil {
			session.UbsanEnabled = *req.UbsanEnabled
		}
		if session.AsanEnabled || session.UbsanEnabled {
			dir := strings.TrimSpace(session.SanitizerDir)
			if dir == "" {
				dir = filepath.Join(getSanitizerBaseDir(), session.ID)
			}
			resolved, err := ensureWritableSessionDir(dir, filepath.Join(os.TempDir(), "sanitizers", session.ID))
			if err != nil {
				storeMu.Unlock()
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Failed to prepare sanitizer directory: %v", err)})
				return
			}
			session.SanitizerDir = resolved
		}
		if req.CanDevices != nil {
			if err := validateCanDevices(*req.CanDevices); err != nil {
				storeMu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
				return
			}
			session.CanDevices = *req.CanDevices
		}
		if req.TapInterfaces != nil {
			if err := validateTapInterfaces(*req.TapInterfaces); err != nil {
				storeMu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
				return
			}
			if err := validatePastaModeRequirements(*req.TapInterfaces); err != nil {
				storeMu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
				return
			}
			session.TapInterfaces = *req.TapInterfaces
		}
		if req.BluetoothConfig != nil {
			if err := validateBluetoothConfig(req.BluetoothConfig); err != nil {
				storeMu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
				return
			}
			session.BluetoothConfig = req.BluetoothConfig
		}
		if req.UARTForwarding != nil {
			if err := validateUARTForwardingConfig(req.UARTForwarding); err != nil {
				storeMu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
				return
			}
			session.UARTForwarding = req.UARTForwarding
		}
		if req.DebugConfig != nil {
			if err := validateDebugConfig(req.DebugConfig); err != nil {
				storeMu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
				return
			}
			session.DebugConfig = req.DebugConfig
		}

		session.UpdatedAt = time.Now().UTC()
		sessions[id] = session
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after session update: %v", err)
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    session,
		})
	}
}

func validateCanDevices(devices []types.CanDeviceConfig) error {
	seen := map[string]struct{}{}
	for i, dev := range devices {
		name := strings.TrimSpace(dev.Name)
		host := strings.TrimSpace(dev.HostDevice)
		if name == "" {
			return fmt.Errorf("can_devices[%d].name is required", i)
		}
		if host == "" {
			return fmt.Errorf("can_devices[%d].host_device is required", i)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate CAN device name: %s", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateTapInterfaces(interfaces []types.TapConfig) error {
	seen := map[string]struct{}{}
	for i, tap := range interfaces {
		if tap.TunOverUART {
			if strings.TrimSpace(tap.UARTDevicePath) == "" {
				return fmt.Errorf("tap_interfaces[%d].uart_device_path is required when tun_over_uart is true", i)
			}
			if tap.EnableBridge || tap.PastaMode {
				return fmt.Errorf("tap_interfaces[%d].tun_over_uart is mutually exclusive with enable_bridge and pasta_mode", i)
			}
			continue
		}

		hostIf := strings.TrimSpace(tap.HostInterface)
		if hostIf == "" {
			return fmt.Errorf("tap_interfaces[%d].host_interface is required", i)
		}
		if _, exists := seen[hostIf]; exists {
			return fmt.Errorf("duplicate TAP host_interface: %s", hostIf)
		}
		seen[hostIf] = struct{}{}

		if tap.EnableBridge && strings.TrimSpace(tap.BridgeInterface) == "" {
			return fmt.Errorf("tap_interfaces[%d].bridge_interface is required when enable_bridge is true", i)
		}

		if (tap.IPAddress == "") != (tap.Netmask == "") {
			return fmt.Errorf("tap_interfaces[%d] requires both ip_address and netmask when either is set", i)
		}

		if tap.IPAddress != "" && net.ParseIP(tap.IPAddress) == nil {
			return fmt.Errorf("tap_interfaces[%d].ip_address is invalid", i)
		}
	}
	return nil
}

func validateBluetoothConfig(cfg *types.BluetoothConfig) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}

	transport := strings.TrimSpace(cfg.Transport)
	if transport == "" {
		transport = "hci"
	}
	if transport != "hci" && transport != "hci_uart" {
		return fmt.Errorf("bluetooth_config.transport must be one of: hci, hci_uart")
	}

	if transport == "hci_uart" {
		if strings.TrimSpace(cfg.UARTDevicePath) == "" {
			return fmt.Errorf("bluetooth_config.uart_device_path is required when transport is hci_uart")
		}
		if cfg.UARTBaudRate <= 0 {
			cfg.UARTBaudRate = 115200
		}
		return nil
	}

	hciDevice := strings.TrimSpace(cfg.HciDevice)
	hostPath := strings.TrimSpace(cfg.HostDevicePath)
	if hciDevice == "" && hostPath == "" {
		return fmt.Errorf("bluetooth_config.hci_device or bluetooth_config.host_device_path is required when bluetooth is enabled")
	}
	return nil
}

func validateUARTForwardingConfig(cfg *types.UARTForwardingConfig) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}

	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		mode = "tun"
	}
	if mode != "tun" {
		return fmt.Errorf("uart_forwarding.mode must be 'tun'")
	}
	if strings.TrimSpace(cfg.HostDevicePath) == "" {
		return fmt.Errorf("uart_forwarding.host_device_path is required when uart forwarding is enabled")
	}
	if cfg.BaudRate <= 0 {
		cfg.BaudRate = 115200
	}
	if strings.TrimSpace(cfg.ContainerDevicePath) == "" {
		cfg.ContainerDevicePath = "/dev/ttyTUN0"
	}

	return nil
}

func validateDebugConfig(cfg *types.DebugConfig) error {
	if cfg == nil {
		return nil
	}
	if cfg.Port == 0 {
		cfg.Port = 3333
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("debug_config.port must be in range 1-65535")
	}
	return nil
}

// validatePastaModeRequirements checks pasta mode configuration compatibility
func validatePastaModeRequirements(interfaces []types.TapConfig) error {
	for i, tap := range interfaces {
		if tap.PastaMode {
			// Pasta mode can't be combined with bridge mode (they're mutually exclusive)
			if tap.EnableBridge {
				return fmt.Errorf("tap_interfaces[%d]: pasta_mode and enable_bridge are mutually exclusive", i)
			}

			// Warn that pasta requires user namespaces on the host, but don't fail
			// The actual check happens at runtime if the host doesn't support user namespaces
		}
	}
	return nil
}

// HandleDeleteSession deletes a session
func HandleDeleteSession(mgr ContainerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.Lock()
		session, ok := sessions[id]
		if !ok {
			storeMu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}
		storeMu.Unlock()

		if requireSessionOwner(w, r, session.OwnerType, session.OwnerID) {
			return
		}

		storeMu.Lock()
		delete(sessions, id)
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after session delete: %v", err)
		}

		if session.ContainerID != "" {
			unregisterSessionUARTBackends(id)
			_ = mgr.RemoveContainer(context.Background(), session.ContainerID)
		}
		if err := container.CleanupTapInterfaces(&session); err != nil {
			log.Printf("cleanup TAP interfaces failed after session delete: %v", err)
		}
		cleanupPCAPArtifact(session.PCAPFilePath)

		json.NewEncoder(w).Encode(Response{
			Success: true,
		})
	}
}

// HandleStartSession starts a stopped session
func HandleStartSession(mgr ContainerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[id]
		binary, binaryOK := binaries[session.BinaryID]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}
		if !binaryOK {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Binary not found"})
			return
		}

		if requireSessionOwner(w, r, session.OwnerType, session.OwnerID) {
			return
		}

		if session.State == types.SessionStateRunning {
			json.NewEncoder(w).Encode(Response{Success: true, Data: session})
			return
		}
		if session.State == types.SessionStatePaused {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session is paused; use resume instead of start"})
			return
		}

		if session.ContainerID == "" {
			containerID, err := mgr.CreateContainer(context.Background(), &session, &binary)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Create container failed: %v", err)})
				return
			}
			session.ContainerID = containerID
		}

		if err := mgr.StartContainer(context.Background(), session.ContainerID); err != nil {
			if isContainerNotFoundError(err) {
				containerID, createErr := mgr.CreateContainer(context.Background(), &session, &binary)
				if createErr != nil {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Recreate container failed: %v", createErr)})
					return
				}
				session.ContainerID = containerID
				if retryErr := mgr.StartContainer(context.Background(), session.ContainerID); retryErr != nil {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Start container failed after recreate: %v", retryErr)})
					return
				}
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Start container failed: %v", err)})
				return
			}
		}

		// Some binaries fail immediately due to unsupported CLI flags.
		// Confirm the container is actually running before reporting success.
		time.Sleep(200 * time.Millisecond)
		status, err := mgr.GetContainerStatus(context.Background(), session.ContainerID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Start verification failed: %v", err)})
			return
		}
		if status != types.SessionStateRunning {
			session.State = types.SessionStateStopped
			session.UpdatedAt = time.Now().UTC()
			storeMu.Lock()
			sessions[id] = session
			storeMu.Unlock()
			if err := persistState(); err != nil {
				log.Printf("persist state failed after session start failure: %v", err)
			}

			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{
				Success: false,
				Error:   "Session process exited immediately. The uploaded binary may not support one or more runtime flags (for example --seed).",
			})
			return
		}

		session.State = types.SessionStateRunning
		session.UpdatedAt = time.Now().UTC()
		if err := registerSessionUARTBackends(&session); err != nil {
			log.Printf("register UART backends failed for session %s: %v", session.ID, err)
		}
		storeMu.Lock()
		sessions[id] = session
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after session start: %v", err)
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    session,
		})
	}
}

// HandleStopSession stops a running session
func HandleStopSession(mgr ContainerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[id]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if requireSessionOwner(w, r, session.OwnerType, session.OwnerID) {
			return
		}

		if session.State == types.SessionStateStopped {
			unregisterSessionUARTBackends(id)
			json.NewEncoder(w).Encode(Response{Success: true, Data: session})
			return
		}

		if session.ContainerID != "" {
			if err := mgr.StopContainer(context.Background(), session.ContainerID); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Stop container failed: %v", err)})
				return
			}
		}
		unregisterSessionUARTBackends(id)

		if err := container.CleanupTapInterfaces(&session); err != nil {
			log.Printf("cleanup TAP interfaces failed after session stop: %v", err)
		}

		session.State = types.SessionStateStopped
		session.UpdatedAt = time.Now().UTC()
		storeMu.Lock()
		sessions[id] = session
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after session stop: %v", err)
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    session,
		})
	}
}

// HandlePauseSession pauses a session
func HandlePauseSession(mgr ContainerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[id]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if requireSessionOwner(w, r, session.OwnerType, session.OwnerID) {
			return
		}

		if session.State == types.SessionStatePaused {
			json.NewEncoder(w).Encode(Response{Success: true, Data: session})
			return
		}
		if session.State != types.SessionStateRunning {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session must be running to pause"})
			return
		}

		if session.ContainerID == "" {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session has no container"})
			return
		}

		if err := mgr.PauseContainer(context.Background(), session.ContainerID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Pause container failed: %v", err)})
			return
		}

		session.State = types.SessionStatePaused
		session.UpdatedAt = time.Now().UTC()
		storeMu.Lock()
		sessions[id] = session
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after session pause: %v", err)
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    session,
		})
	}
}

// HandleResumeSession resumes a paused session
func HandleResumeSession(mgr ContainerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[id]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if requireSessionOwner(w, r, session.OwnerType, session.OwnerID) {
			return
		}

		if session.State == types.SessionStateRunning {
			json.NewEncoder(w).Encode(Response{Success: true, Data: session})
			return
		}
		if session.State != types.SessionStatePaused {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session must be paused to resume"})
			return
		}

		if session.ContainerID == "" {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session has no container"})
			return
		}

		if err := mgr.ResumeContainer(context.Background(), session.ContainerID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Resume container failed: %v", err)})
			return
		}

		session.State = types.SessionStateRunning
		session.UpdatedAt = time.Now().UTC()
		storeMu.Lock()
		sessions[id] = session
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after session resume: %v", err)
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    session,
		})
	}
}

// HandleRestoreSession restores a session from a snapshot
func HandleRestoreSession(mgr ContainerManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if requireAuthenticated(w, r) {
			return
		}

		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[id]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if requireSessionOwner(w, r, session.OwnerType, session.OwnerID) {
			return
		}

		clone := session
		clone.ID = fmt.Sprintf("session-%d", time.Now().UTC().UnixMilli())
		clone.ContainerID = ""
		clone.State = types.SessionStateStopped
		clone.CreatedAt = time.Now().UTC()
		clone.UpdatedAt = clone.CreatedAt

		if err := cloneSessionRuntimeState(r.Context(), session.ID, clone.ID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Restore runtime state failed: %v", err)})
			return
		}

		storeMu.Lock()
		sessions[clone.ID] = clone
		storeMu.Unlock()
		if err := persistState(); err != nil {
			log.Printf("persist state failed after session restore: %v", err)
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data:    clone,
		})
	}
}

// HandleSSE provides server-sent events for real-time updates
func HandleSSE(mgr ContainerManager, mux *uart.Multiplexer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("session")
		if sessionID == "" {
			http.Error(w, "Missing session parameter", http.StatusBadRequest)
			return
		}

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		storeMu.RLock()
		session, sessionOK := sessions[sessionID]
		storeMu.RUnlock()

		if !sessionOK {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}

		// Prefer container stdout/stderr logs first. This avoids stalled terminals when FIFO backends
		// are configured but the binary emits output on stdout/stderr.
		if mgr != nil && session.ContainerID != "" {
			logs, err := mgr.StreamContainerLogs(r.Context(), session.ContainerID)
			if err == nil {
				defer logs.Close()

				reader := io.Reader(logs)
				if tty, ttyErr := mgr.IsContainerTTY(r.Context(), session.ContainerID); ttyErr == nil && !tty {
					r, w := io.Pipe()
					go func() {
						_, copyErr := stdcopy.StdCopy(w, w, logs)
						if copyErr != nil {
							_ = w.CloseWithError(copyErr)
							return
						}
						_ = w.Close()
					}()
					reader = r
				}

				scanner := bufio.NewScanner(reader)
				scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
				for scanner.Scan() {
					event := types.UARTDataEvent{SessionID: sessionID, UARTIdx: 0, Data: scanner.Text()}
					data, _ := json.Marshal(event)
					fmt.Fprintf(w, "data: %s\n\n", string(data))
					flusher.Flush()
				}
				return
			}
		}

		// If container log streaming is unavailable, try explicit UART FIFO stream.
		if mux != nil && mux.HasSessionBackends(sessionID) {
			events := mux.Subscribe(sessionID)
			defer mux.Unsubscribe(sessionID, events)
			for {
				select {
				case <-r.Context().Done():
					return
				case event, ok := <-events:
					if !ok {
						return
					}
					data, _ := json.Marshal(event)
					fmt.Fprintf(w, "data: %s\n\n", string(data))
					flusher.Flush()
				}
			}
		}

		if mux == nil {
			return
		}

		// Last-resort stream: in-memory UART multiplexer events (default session).
		events := mux.Subscribe(sessionID)
		defer mux.Unsubscribe(sessionID, events)
		for {
			select {
			case <-r.Context().Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				data, _ := json.Marshal(event)
				fmt.Fprintf(w, "data: %s\n\n", string(data))
				flusher.Flush()
			}
		}
	}
}

// HandleDownloadPCAP downloads the PCAP capture file for a session.
func HandleDownloadPCAP() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requireAuthenticated(w, r) {
			return
		}
		sessionID := r.PathValue("id")
		if sessionID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[sessionID]
		storeMu.RUnlock()

		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		// Check if PCAP file exists
		if session.PCAPFilePath == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "No PCAP file configured for this session"})
			return
		}

		// Verify file exists
		fileInfo, err := os.Stat(session.PCAPFilePath)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("PCAP file not found: %v", err)})
			return
		}

		// Open file
		file, err := os.Open(session.PCAPFilePath)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Failed to open PCAP file: %v", err)})
			return
		}
		defer file.Close()

		// Set headers for download
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.pcap", sessionID))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

		// Stream file to response
		if _, err := io.Copy(w, file); err != nil {
			log.Printf("Error streaming PCAP file: %v", err)
		}
	}
}

// HandleDownloadCoverage downloads collected code coverage artifacts as .tar.gz.
func HandleDownloadCoverage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requireAuthenticated(w, r) {
			return
		}
		sessionID := r.PathValue("id")
		if sessionID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[sessionID]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if !session.CoverageEnabled {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Coverage is not enabled for this session"})
			return
		}

		if strings.TrimSpace(session.CoverageDir) == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "No coverage directory configured"})
			return
		}

		if _, err := os.Stat(session.CoverageDir); err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Coverage directory not found: %v", err)})
			return
		}

		if err := writeDirectoryTarGz(w, session.CoverageDir, fmt.Sprintf("%s-coverage.tar.gz", sessionID)); err != nil {
			log.Printf("Error streaming coverage archive: %v", err)
		}
	}
}

// HandleDownloadSanitizers downloads collected ASan/UBSan reports as .tar.gz.
func HandleDownloadSanitizers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requireAuthenticated(w, r) {
			return
		}
		sessionID := r.PathValue("id")
		if sessionID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[sessionID]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if !session.AsanEnabled && !session.UbsanEnabled {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "ASan/UBSan is not enabled for this session"})
			return
		}

		if strings.TrimSpace(session.SanitizerDir) == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "No sanitizer directory configured"})
			return
		}

		if _, err := os.Stat(session.SanitizerDir); err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Sanitizer directory not found: %v", err)})
			return
		}

		if err := writeDirectoryTarGz(w, session.SanitizerDir, fmt.Sprintf("%s-sanitizers.tar.gz", sessionID)); err != nil {
			log.Printf("Error streaming sanitizer archive: %v", err)
		}
	}
}

// HandleGetSanitizerReport parses ASan/UBSan outputs and returns structured findings.
func HandleGetSanitizerReport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requireAuthenticated(w, r) {
			return
		}
		sessionID := r.PathValue("id")
		if sessionID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[sessionID]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if !session.AsanEnabled && !session.UbsanEnabled {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "ASan/UBSan is not enabled for this session"})
			return
		}

		if strings.TrimSpace(session.SanitizerDir) == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "No sanitizer directory configured"})
			return
		}

		findings, err := parseSanitizerFindings(session.SanitizerDir)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Failed to parse sanitizer reports: %v", err)})
			return
		}

		toolFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tool")))
		searchFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

		filtered := make([]sanitizerFinding, 0, len(findings))
		for _, finding := range findings {
			if toolFilter != "" && finding.Tool != toolFilter {
				continue
			}
			if searchFilter != "" {
				haystack := strings.ToLower(strings.Join([]string{finding.Summary, finding.File, finding.Raw}, " "))
				if !strings.Contains(haystack, searchFilter) {
					continue
				}
			}
			filtered = append(filtered, finding)
		}

		if limitRaw := strings.TrimSpace(r.URL.Query().Get("limit")); limitRaw != "" {
			limit, convErr := strconv.Atoi(limitRaw)
			if convErr == nil && limit > 0 && len(filtered) > limit {
				filtered = filtered[:limit]
			}
		}

		counts := map[string]int{}
		for _, finding := range filtered {
			counts[finding.Tool]++
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data: sanitizerReportResponse{
				SessionID: sessionID,
				Total:     len(filtered),
				ByTool:    counts,
				Findings:  filtered,
				Filters: map[string]string{
					"tool": toolFilter,
					"q":    searchFilter,
				},
				Generated: time.Now().UTC(),
			},
		})
	}
}

var (
	ubsanRuntimeErrorRegex = regexp.MustCompile(`^(.+?):(\d+):(\d+):\s*runtime error:\s*(.+)$`)
	ubsanRuntimeNoColumn   = regexp.MustCompile(`^(.+?):(\d+):\s*runtime error:\s*(.+)$`)
	asanSummaryRegex       = regexp.MustCompile(`AddressSanitizer:\s*(.+)$`)
	asanFrameRegex         = regexp.MustCompile(`#\d+\s+0x[0-9a-fA-F]+\s+in\s+.+\s+(.+?):(\d+)(?::(\d+))?$`)
)

func parseSanitizerFindings(sanitizerDir string) ([]sanitizerFinding, error) {
	entries, err := os.ReadDir(sanitizerDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []sanitizerFinding{}, nil
		}
		return nil, err
	}

	results := []sanitizerFinding{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filePath := filepath.Join(sanitizerDir, entry.Name())
		content, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return nil, readErr
		}

		tool := detectSanitizerTool(entry.Name(), string(content))
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}

			if finding, ok := parseSanitizerLine(tool, entry.Name(), trimmed); ok {
				results = append(results, finding)
			}
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Tool == results[j].Tool {
			if results[i].File == results[j].File {
				return results[i].Line < results[j].Line
			}
			return results[i].File < results[j].File
		}
		return results[i].Tool < results[j].Tool
	})

	return results, nil
}

func detectSanitizerTool(filename, content string) string {
	lowerName := strings.ToLower(filename)
	lowerContent := strings.ToLower(content)
	if strings.Contains(lowerName, "asan") || strings.Contains(lowerContent, "addresssanitizer") {
		return "asan"
	}
	if strings.Contains(lowerName, "ubsan") || strings.Contains(lowerContent, "runtime error:") {
		return "ubsan"
	}
	return "unknown"
}

func parseSanitizerLine(tool, source, line string) (sanitizerFinding, bool) {
	if match := ubsanRuntimeErrorRegex.FindStringSubmatch(line); len(match) == 5 {
		lineNum, _ := strconv.Atoi(match[2])
		colNum, _ := strconv.Atoi(match[3])
		return sanitizerFinding{
			Tool:    "ubsan",
			File:    match[1],
			Line:    lineNum,
			Column:  colNum,
			Summary: strings.TrimSpace(match[4]),
			Source:  source,
			Raw:     line,
		}, true
	}

	if match := ubsanRuntimeNoColumn.FindStringSubmatch(line); len(match) == 4 {
		lineNum, _ := strconv.Atoi(match[2])
		return sanitizerFinding{
			Tool:    "ubsan",
			File:    match[1],
			Line:    lineNum,
			Summary: strings.TrimSpace(match[3]),
			Source:  source,
			Raw:     line,
		}, true
	}

	if match := asanSummaryRegex.FindStringSubmatch(line); len(match) == 2 {
		return sanitizerFinding{
			Tool:    "asan",
			Summary: strings.TrimSpace(match[1]),
			Source:  source,
			Raw:     line,
		}, true
	}

	if match := asanFrameRegex.FindStringSubmatch(line); len(match) == 4 {
		lineNum, _ := strconv.Atoi(match[2])
		colNum := 0
		if strings.TrimSpace(match[3]) != "" {
			colNum, _ = strconv.Atoi(match[3])
		}
		return sanitizerFinding{
			Tool:    "asan",
			File:    match[1],
			Line:    lineNum,
			Column:  colNum,
			Summary: "stack frame",
			Source:  source,
			Raw:     line,
		}, true
	}

	if strings.Contains(strings.ToLower(line), "runtime error:") {
		return sanitizerFinding{
			Tool:    "ubsan",
			Summary: line,
			Source:  source,
			Raw:     line,
		}, true
	}
	if strings.Contains(strings.ToLower(line), "addresssanitizer") {
		return sanitizerFinding{
			Tool:    "asan",
			Summary: line,
			Source:  source,
			Raw:     line,
		}, true
	}

	if tool == "asan" || tool == "ubsan" {
		return sanitizerFinding{
			Tool:    tool,
			Summary: line,
			Source:  source,
			Raw:     line,
		}, true
	}

	return sanitizerFinding{}, false
}

func writeDirectoryTarGz(w http.ResponseWriter, sourceDir, filename string) error {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return err
		}

		return nil
	})
}

func getCoverageBaseDir() string {
	const preferredDir = "/data/coverage"
	if err := os.MkdirAll(preferredDir, 0755); err == nil {
		return preferredDir
	}

	fallbackDir := filepath.Join(os.TempDir(), "coverage")
	_ = os.MkdirAll(fallbackDir, 0755)
	return fallbackDir
}

func getPCAPBaseDir() string {
	if configured := strings.TrimSpace(os.Getenv("PCAP_BASE_DIR")); configured != "" {
		if err := os.MkdirAll(configured, 0755); err == nil {
			return configured
		}
	}

	const preferredDir = "/data/pcaps"
	if err := os.MkdirAll(preferredDir, 0755); err == nil {
		return preferredDir
	}

	fallbackDir := filepath.Join(os.TempDir(), "pcaps")
	_ = os.MkdirAll(fallbackDir, 0755)
	return fallbackDir
}

func cleanupPCAPArtifact(pcapPath string) {
	path := strings.TrimSpace(pcapPath)
	if path == "" {
		return
	}

	if !strings.EqualFold(filepath.Ext(path), ".pcap") {
		log.Printf("skip pcap cleanup for non-pcap path: %s", path)
		return
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("cleanup pcap artifact failed for %s: %v", path, err)
	}
}

func getSanitizerBaseDir() string {
	const preferredDir = "/data/sanitizers"
	if err := os.MkdirAll(preferredDir, 0755); err == nil {
		return preferredDir
	}

	fallbackDir := filepath.Join(os.TempDir(), "sanitizers")
	_ = os.MkdirAll(fallbackDir, 0755)
	return fallbackDir
}

func ensureWritableSessionDir(preferredDir, fallbackDir string) (string, error) {
	if strings.TrimSpace(preferredDir) != "" {
		if err := os.MkdirAll(preferredDir, 0755); err == nil {
			return preferredDir, nil
		}
	}

	if strings.TrimSpace(fallbackDir) == "" {
		return "", fmt.Errorf("no writable directory available")
	}

	if err := os.MkdirAll(fallbackDir, 0755); err != nil {
		return "", err
	}

	return fallbackDir, nil
}

// HandleGetDebugTarget returns GDB connection details for a session.
func HandleGetDebugTarget() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requireAuthenticated(w, r) {
			return
		}
		sessionID := r.PathValue("id")
		if sessionID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[sessionID]
		storeMu.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if session.DebugConfig == nil {
			session.DebugConfig = &types.DebugConfig{Enabled: false, Port: 3333, WaitForGDB: false}
		}

		state := "configured"
		if session.State != types.SessionStateRunning {
			state = "session_not_running"
		}

		json.NewEncoder(w).Encode(Response{
			Success: true,
			Data: debugTargetResponse{
				Enabled:   session.DebugConfig.Enabled,
				Host:      "127.0.0.1",
				Port:      session.DebugConfig.Port,
				State:     state,
				Container: session.ContainerID,
			},
		})
	}
}

// HandleDebugWebSocketProxy bridges websocket frames to a session's gdbserver TCP socket.
func HandleDebugWebSocketProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requireAuthenticated(w, r) {
			w.Header().Set("Content-Type", "application/json")
			return
		}
		sessionID := r.PathValue("id")
		if sessionID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing session id"})
			return
		}

		storeMu.RLock()
		session, ok := sessions[sessionID]
		storeMu.RUnlock()
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session not found"})
			return
		}

		if session.DebugConfig == nil || !session.DebugConfig.Enabled {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Debugging is not enabled for this session"})
			return
		}

		if session.State != types.SessionStateRunning {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Session must be running before opening a debug websocket"})
			return
		}

		targetAddr := resolveDebugTCPAddress(session)
		backendConn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Failed to connect to gdbserver: %v", err)})
			return
		}

		wsConn, err := debugWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			_ = backendConn.Close()
			return
		}

		defer wsConn.Close()
		defer backendConn.Close()

		errCh := make(chan error, 2)

		go func() {
			buf := make([]byte, 64*1024)
			for {
				n, readErr := backendConn.Read(buf)
				if n > 0 {
					if writeErr := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
						errCh <- writeErr
						return
					}
				}
				if readErr != nil {
					errCh <- readErr
					return
				}
			}
		}()

		go func() {
			for {
				msgType, payload, readErr := wsConn.ReadMessage()
				if readErr != nil {
					errCh <- readErr
					return
				}
				if msgType != websocket.BinaryMessage && msgType != websocket.TextMessage {
					continue
				}
				if len(payload) == 0 {
					continue
				}
				if _, writeErr := backendConn.Write(payload); writeErr != nil {
					errCh <- writeErr
					return
				}
			}
		}()

		<-errCh
	}
}

// HandleListDebugBreakpoints returns breakpoints configured on the session debug target.
func HandleListDebugBreakpoints() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requireAuthenticated(w, r) {
			return
		}
		session, binary, targetAddr, statusCode, err := resolveDebugSessionContext(r.PathValue("id"))
		if err != nil {
			w.WriteHeader(statusCode)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		output, runErr := runGDBBatch(binary.FilePath, targetAddr, []string{"info break"})
		if runErr != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("%v\n%s", runErr, output)})
			return
		}

		json.NewEncoder(w).Encode(Response{Success: true, Data: map[string]interface{}{
			"session_id":  session.ID,
			"breakpoints": parseBreakpoints(output),
			"raw":         output,
			"target":      targetAddr,
		}})
	}
}

// HandleAddDebugBreakpoint adds a breakpoint on the remote target.
func HandleAddDebugBreakpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requireAuthenticated(w, r) {
			return
		}
		session, binary, targetAddr, statusCode, err := resolveDebugSessionContext(r.PathValue("id"))
		if err != nil {
			w.WriteHeader(statusCode)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		var req addBreakpointRequest
		if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("Invalid request body: %v", decodeErr)})
			return
		}
		loc := strings.TrimSpace(req.Location)
		if loc == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "location is required"})
			return
		}
		if !gdbLocationRe.MatchString(loc) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "invalid breakpoint location: only alphanumeric, _ . : / * - characters are allowed"})
			return
		}

		output, runErr := runGDBBatch(binary.FilePath, targetAddr, []string{fmt.Sprintf("break %s", loc), "info break"})
		if runErr != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("%v\n%s", runErr, output)})
			return
		}

		json.NewEncoder(w).Encode(Response{Success: true, Data: map[string]interface{}{
			"session_id":  session.ID,
			"breakpoints": parseBreakpoints(output),
			"raw":         output,
			"target":      targetAddr,
		}})
	}
}

// HandleDeleteDebugBreakpoint removes a breakpoint by breakpoint number.
func HandleDeleteDebugBreakpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requireAuthenticated(w, r) {
			return
		}
		session, binary, targetAddr, statusCode, err := resolveDebugSessionContext(r.PathValue("id"))
		if err != nil {
			w.WriteHeader(statusCode)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		number := strings.TrimSpace(r.PathValue("number"))
		if number == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "Missing breakpoint number"})
			return
		}
		if !gdbBreakpointNumberRe.MatchString(number) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(Response{Success: false, Error: "invalid breakpoint number: must be a positive integer"})
			return
		}

		output, runErr := runGDBBatch(binary.FilePath, targetAddr, []string{fmt.Sprintf("delete %s", number), "info break"})
		if runErr != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("%v\n%s", runErr, output)})
			return
		}

		json.NewEncoder(w).Encode(Response{Success: true, Data: map[string]interface{}{
			"session_id":  session.ID,
			"breakpoints": parseBreakpoints(output),
			"raw":         output,
			"target":      targetAddr,
		}})
	}
}

// HandleGetDebugStackTrace returns current stack backtrace for the target.
func HandleGetDebugStackTrace() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requireAuthenticated(w, r) {
			return
		}
		session, binary, targetAddr, statusCode, err := resolveDebugSessionContext(r.PathValue("id"))
		if err != nil {
			w.WriteHeader(statusCode)
			json.NewEncoder(w).Encode(Response{Success: false, Error: err.Error()})
			return
		}

		output, runErr := runGDBBatch(binary.FilePath, targetAddr, []string{"bt"})
		if runErr != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(Response{Success: false, Error: fmt.Sprintf("%v\n%s", runErr, output)})
			return
		}

		json.NewEncoder(w).Encode(Response{Success: true, Data: map[string]interface{}{
			"session_id": session.ID,
			"frames":     parseStackFrames(output),
			"raw":        output,
			"target":     targetAddr,
		}})
	}
}

func resolveDebugSessionContext(sessionID string) (types.Session, types.Binary, string, int, error) {
	if strings.TrimSpace(sessionID) == "" {
		return types.Session{}, types.Binary{}, "", http.StatusBadRequest, fmt.Errorf("Missing session id")
	}

	storeMu.RLock()
	session, sessionOK := sessions[sessionID]
	binary, binaryOK := binaries[session.BinaryID]
	storeMu.RUnlock()

	if !sessionOK {
		return types.Session{}, types.Binary{}, "", http.StatusNotFound, fmt.Errorf("Session not found")
	}
	if !binaryOK {
		return types.Session{}, types.Binary{}, "", http.StatusNotFound, fmt.Errorf("Binary not found")
	}
	if session.DebugConfig == nil || !session.DebugConfig.Enabled {
		return types.Session{}, types.Binary{}, "", http.StatusConflict, fmt.Errorf("Debugging is not enabled for this session")
	}
	if session.State != types.SessionStateRunning {
		return types.Session{}, types.Binary{}, "", http.StatusConflict, fmt.Errorf("Session must be running")
	}

	return session, binary, resolveDebugTCPAddress(session), http.StatusOK, nil
}

func parseBreakpoints(output string) []debugBreakpoint {
	linePattern := regexp.MustCompile(`(?m)^\s*([0-9]+)\s+breakpoint\s+\S+\s+([yn])\s+.*$`)
	locPattern := regexp.MustCompile(`\s+at\s+(.+)$`)
	lines := strings.Split(output, "\n")
	results := make([]debugBreakpoint, 0)

	for _, line := range lines {
		matches := linePattern.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}
		bp := debugBreakpoint{Number: matches[1], Enabled: strings.EqualFold(matches[2], "y")}
		if loc := locPattern.FindStringSubmatch(line); len(loc) > 1 {
			bp.Location = strings.TrimSpace(loc[1])
		} else {
			bp.Location = strings.TrimSpace(line)
		}
		results = append(results, bp)
	}

	return results
}

func parseStackFrames(output string) []stackFrame {
	pattern := regexp.MustCompile(`(?m)^#([0-9]+)\s+(.+)$`)
	frames := make([]stackFrame, 0)
	for _, m := range pattern.FindAllStringSubmatch(output, -1) {
		if len(m) < 3 {
			continue
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		rest := strings.TrimSpace(m[2])
		fn := rest
		loc := ""
		if at := strings.SplitN(rest, " at ", 2); len(at) == 2 {
			fn = strings.TrimSpace(at[0])
			loc = strings.TrimSpace(at[1])
		}
		frames = append(frames, stackFrame{Index: idx, Function: fn, Location: loc})
	}
	return frames
}

func resolveDebugTCPAddress(session types.Session) string {
	port := 3333
	if session.DebugConfig != nil && session.DebugConfig.Port > 0 {
		port = session.DebugConfig.Port
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}
