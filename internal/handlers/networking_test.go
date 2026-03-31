package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

var mockTime = time.Now().UTC()

// MockContainerManager is a mock implementation for testing
type MockContainerManager struct {
	createFunc func(context.Context, *types.Session, *types.Binary) (string, error)
	startFunc  func(context.Context, string) error
	stopFunc   func(context.Context, string) error
}

func (m *MockContainerManager) CreateContainer(ctx context.Context, s *types.Session, b *types.Binary) (string, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, s, b)
	}
	return "", nil
}

func (m *MockContainerManager) StartContainer(ctx context.Context, id string) error {
	if m.startFunc != nil {
		return m.startFunc(ctx, id)
	}
	return nil
}

func (m *MockContainerManager) StopContainer(ctx context.Context, id string) error {
	if m.stopFunc != nil {
		return m.stopFunc(ctx, id)
	}
	return nil
}

func (m *MockContainerManager) PauseContainer(ctx context.Context, id string) error {
	return nil
}

func (m *MockContainerManager) ResumeContainer(ctx context.Context, id string) error {
	return nil
}

func (m *MockContainerManager) RemoveContainer(ctx context.Context, id string) error {
	return nil
}

func (m *MockContainerManager) GetContainerStatus(ctx context.Context, id string) (types.SessionState, error) {
	return types.SessionStateStopped, nil
}

func (m *MockContainerManager) StreamContainerLogs(ctx context.Context, id string) (io.ReadCloser, error) {
	return nil, nil
}

func (m *MockContainerManager) IsContainerTTY(ctx context.Context, id string) (bool, error) {
	return false, nil
}

// marshalJSON is a helper function to marshal an interface to JSON
func marshalJSON(obj interface{}) []byte {
	data, err := json.Marshal(obj)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return data
}

// TestAdvancedNetworkingIntegration tests advanced networking features.
func TestAdvancedNetworkingIntegration(t *testing.T) {
	// Create mock container manager
	mockMgr := &MockContainerManager{
		createFunc: func(ctx context.Context, s *types.Session, b *types.Binary) (string, error) {
			return fmt.Sprintf("container-%s", s.ID), nil
		},
		startFunc: func(ctx context.Context, containerID string) error {
			return nil
		},
	}

	t.Run("Create session with networking config", func(t *testing.T) {
		// Reset global state
		ResetSessionStore()

		// Create binary first
		binary := types.Binary{
			ID:            "test-binary-1",
			Name:          "test.elf",
			Bits:          32,
			IsStatic:      true,
			ZephyrVersion: "3.4.0",
			FilePath:      "/tmp/test.elf",
			FileSize:      1024,
		}
		storeMu.Lock()
		binaries["test-binary-1"] = binary
		storeMu.Unlock()

		// Create session
		handler := HandleCreateSession(mockMgr)
		req := httptest.NewRequest("POST", "/api/sessions", strings.NewReader(`{
			"binary_id": "test-binary-1",
			"seed": 12345,
			"use_real_time": false,
			"timeout_seconds": 300
		}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		var resp Response
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if !resp.Success {
			t.Fatalf("Expected success")
		}

		// Extract session ID
		data := resp.Data.(map[string]interface{})
		sessionID := data["id"].(string)

		// Verify session has networking config initialized
		storeMu.RLock()
		session, ok := sessions[sessionID]
		storeMu.RUnlock()

		if !ok {
			t.Fatalf("Session not found")
		}

		if session.NetworkConfig == nil {
			t.Fatalf("NetworkConfig should be initialized")
		}

		if session.PCAPFilePath == "" {
			t.Fatalf("PCAPFilePath should be generated")
		}

		if !strings.Contains(session.PCAPFilePath, ".pcap") {
			t.Fatalf("PCAP path should have .pcap extension")
		}
	})

	t.Run("Update session with SocketCAN devices", func(t *testing.T) {
		ResetSessionStore()

		// Create session
		session := types.Session{
			ID:        "session-can-test",
			BinaryID:  "binary-1",
			State:     types.SessionStateStopped,
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-can-test"] = session
		storeMu.Unlock()

		// Update with CAN devices
		handler := HandleUpdateSession(mockMgr)
		canDeviceJSON := `{
			"can_devices": [
				{"name": "vcan0", "host_device": "/dev/vcan0", "bitrate": 500000},
				{"name": "vcan1", "host_device": "/dev/vcan1", "bitrate": 250000}
			]
		}`
		req := httptest.NewRequest("PATCH", "/api/sessions/session-can-test", strings.NewReader(canDeviceJSON))
		// Simulate path parameter
		req.SetPathValue("id", "session-can-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		var resp Response
		json.NewDecoder(rec.Body).Decode(&resp)

		storeMu.RLock()
		updatedSession, _ := sessions["session-can-test"]
		storeMu.RUnlock()

		if len(updatedSession.CanDevices) != 2 {
			t.Fatalf("Expected 2 CAN devices, got %d", len(updatedSession.CanDevices))
		}

		if updatedSession.CanDevices[0].Name != "vcan0" {
			t.Fatalf("Expected vcan0, got %s", updatedSession.CanDevices[0].Name)
		}

		if updatedSession.CanDevices[1].Bitrate != 250000 {
			t.Fatalf("Expected bitrate 250000, got %d", updatedSession.CanDevices[1].Bitrate)
		}
	})

	t.Run("Update session with TAP interfaces", func(t *testing.T) {
		ResetSessionStore()

		session := types.Session{
			ID:        "session-tap-test",
			BinaryID:  "binary-1",
			State:     types.SessionStateStopped,
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-tap-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		tapJSON := `{
			"tap_interfaces": [
				{"name": "tap0", "host_interface": "tap0", "ip_address": "192.168.1.100", "netmask": "255.255.255.0"}
			]
		}`
		req := httptest.NewRequest("PATCH", "/api/sessions/session-tap-test", strings.NewReader(tapJSON))
		req.SetPathValue("id", "session-tap-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		storeMu.RLock()
		updatedSession, _ := sessions["session-tap-test"]
		storeMu.RUnlock()

		if len(updatedSession.TapInterfaces) != 1 {
			t.Fatalf("Expected 1 TAP interface, got %d", len(updatedSession.TapInterfaces))
		}

		if updatedSession.TapInterfaces[0].IPAddress != "192.168.1.100" {
			t.Fatalf("Expected IP 192.168.1.100, got %s", updatedSession.TapInterfaces[0].IPAddress)
		}
	})

	t.Run("Clear networking arrays with explicit empty payloads", func(t *testing.T) {
		ResetSessionStore()

		session := types.Session{
			ID:       "session-clear-net-test",
			BinaryID: "binary-1",
			State:    types.SessionStateStopped,
			CanDevices: []types.CanDeviceConfig{
				{Name: "vcan0", HostDevice: "/dev/vcan0"},
			},
			TapInterfaces: []types.TapConfig{
				{HostInterface: "tap0"},
			},
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-clear-net-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		req := httptest.NewRequest("PATCH", "/api/sessions/session-clear-net-test", strings.NewReader(`{
			"can_devices": [],
			"tap_interfaces": []
		}`))
		req.SetPathValue("id", "session-clear-net-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		storeMu.RLock()
		updatedSession, _ := sessions["session-clear-net-test"]
		storeMu.RUnlock()

		if len(updatedSession.CanDevices) != 0 {
			t.Fatalf("Expected CAN devices to be cleared, got %d", len(updatedSession.CanDevices))
		}
		if len(updatedSession.TapInterfaces) != 0 {
			t.Fatalf("Expected TAP interfaces to be cleared, got %d", len(updatedSession.TapInterfaces))
		}
	})

	t.Run("Reject TAP bridge config without bridge interface", func(t *testing.T) {
		ResetSessionStore()

		session := types.Session{
			ID:        "session-invalid-bridge-test",
			BinaryID:  "binary-1",
			State:     types.SessionStateStopped,
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-invalid-bridge-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		req := httptest.NewRequest("PATCH", "/api/sessions/session-invalid-bridge-test", strings.NewReader(`{
			"tap_interfaces": [
				{"host_interface": "tap0", "enable_bridge": true}
			]
		}`))
		req.SetPathValue("id", "session-invalid-bridge-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("Expected 400, got %d", rec.Code)
		}

		var resp Response
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if !strings.Contains(resp.Error, "bridge_interface") {
			t.Fatalf("Expected bridge_interface validation error, got %q", resp.Error)
		}
	})

	t.Run("Update session with Bluetooth HCI", func(t *testing.T) {
		ResetSessionStore()

		session := types.Session{
			ID:        "session-bt-test",
			BinaryID:  "binary-1",
			State:     types.SessionStateStopped,
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-bt-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		btJSON := `{
			"bluetooth_config": {
				"enabled": true,
				"hci_device": "/dev/hci0",
				"hci_device_index": 0,
				"host_device_path": "/dev/hci0",
				"advertising_mode": "connectable"
			}
		}`
		req := httptest.NewRequest("PATCH", "/api/sessions/session-bt-test", strings.NewReader(btJSON))
		req.SetPathValue("id", "session-bt-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		storeMu.RLock()
		updatedSession, _ := sessions["session-bt-test"]
		storeMu.RUnlock()

		if updatedSession.BluetoothConfig == nil {
			t.Fatalf("Expected BluetoothConfig to be set")
		}

		if !updatedSession.BluetoothConfig.Enabled {
			t.Fatalf("Expected Bluetooth to be enabled")
		}

		if updatedSession.BluetoothConfig.HciDevice != "/dev/hci0" {
			t.Fatalf("Expected HCI device /dev/hci0, got %s", updatedSession.BluetoothConfig.HciDevice)
		}
	})

	t.Run("Update session with Bluetooth HCI-over-UART", func(t *testing.T) {
		ResetSessionStore()

		session := types.Session{
			ID:        "session-bt-uart-test",
			BinaryID:  "binary-1",
			State:     types.SessionStateStopped,
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-bt-uart-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		btJSON := `{
			"bluetooth_config": {
				"enabled": true,
				"transport": "hci_uart",
				"uart_device_path": "/dev/ttyUSB0",
				"uart_baud_rate": 1000000,
				"advertising_mode": "connectable"
			}
		}`
		req := httptest.NewRequest("PATCH", "/api/sessions/session-bt-uart-test", strings.NewReader(btJSON))
		req.SetPathValue("id", "session-bt-uart-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		storeMu.RLock()
		updatedSession, _ := sessions["session-bt-uart-test"]
		storeMu.RUnlock()

		if updatedSession.BluetoothConfig == nil || updatedSession.BluetoothConfig.Transport != "hci_uart" {
			t.Fatalf("Expected hci_uart transport to be configured")
		}
		if updatedSession.BluetoothConfig.UARTDevicePath != "/dev/ttyUSB0" {
			t.Fatalf("Expected UART path /dev/ttyUSB0, got %s", updatedSession.BluetoothConfig.UARTDevicePath)
		}
	})

	t.Run("Reject Bluetooth HCI-over-UART without UART device", func(t *testing.T) {
		ResetSessionStore()

		session := types.Session{
			ID:        "session-bt-uart-invalid-test",
			BinaryID:  "binary-1",
			State:     types.SessionStateStopped,
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-bt-uart-invalid-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		btJSON := `{
			"bluetooth_config": {
				"enabled": true,
				"transport": "hci_uart"
			}
		}`
		req := httptest.NewRequest("PATCH", "/api/sessions/session-bt-uart-invalid-test", strings.NewReader(btJSON))
		req.SetPathValue("id", "session-bt-uart-invalid-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("Expected 400, got %d", rec.Code)
		}
	})

	t.Run("Update session with UART forwarding", func(t *testing.T) {
		ResetSessionStore()

		session := types.Session{
			ID:        "session-uart-forward-test",
			BinaryID:  "binary-1",
			State:     types.SessionStateStopped,
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-uart-forward-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		uartJSON := `{
			"uart_forwarding": {
				"enabled": true,
				"mode": "tun",
				"host_device_path": "/dev/ttyUSB1",
				"container_device_path": "/dev/ttyTUN0",
				"baud_rate": 921600
			}
		}`
		req := httptest.NewRequest("PATCH", "/api/sessions/session-uart-forward-test", strings.NewReader(uartJSON))
		req.SetPathValue("id", "session-uart-forward-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		storeMu.RLock()
		updatedSession, _ := sessions["session-uart-forward-test"]
		storeMu.RUnlock()

		if updatedSession.UARTForwarding == nil || !updatedSession.UARTForwarding.Enabled {
			t.Fatalf("Expected UART forwarding to be enabled")
		}
	})

	t.Run("Reject UART forwarding without host path", func(t *testing.T) {
		ResetSessionStore()

		session := types.Session{
			ID:        "session-uart-forward-invalid-test",
			BinaryID:  "binary-1",
			State:     types.SessionStateStopped,
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-uart-forward-invalid-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		uartJSON := `{
			"uart_forwarding": {
				"enabled": true,
				"mode": "tun"
			}
		}`
		req := httptest.NewRequest("PATCH", "/api/sessions/session-uart-forward-invalid-test", strings.NewReader(uartJSON))
		req.SetPathValue("id", "session-uart-forward-invalid-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("Expected 400, got %d", rec.Code)
		}
	})

	t.Run("Update TAP interface with TUN-over-UART", func(t *testing.T) {
		ResetSessionStore()

		session := types.Session{
			ID:        "session-tap-uart-test",
			BinaryID:  "binary-1",
			State:     types.SessionStateStopped,
			CreatedAt: mockTime,
		}
		storeMu.Lock()
		sessions["session-tap-uart-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		tapJSON := `{
			"tap_interfaces": [
				{"name": "tap-uart0", "tun_over_uart": true, "uart_device_path": "/dev/ttyUSB2", "uart_baud_rate": 460800}
			]
		}`
		req := httptest.NewRequest("PATCH", "/api/sessions/session-tap-uart-test", strings.NewReader(tapJSON))
		req.SetPathValue("id", "session-tap-uart-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		storeMu.RLock()
		updatedSession, _ := sessions["session-tap-uart-test"]
		storeMu.RUnlock()

		if len(updatedSession.TapInterfaces) != 1 || !updatedSession.TapInterfaces[0].TunOverUART {
			t.Fatalf("Expected tap interface to be configured for tun_over_uart")
		}
	})

	t.Run("Enable PCAP capture", func(t *testing.T) {
		ResetSessionStore()

		tmpDir := t.TempDir()
		os.Setenv("STATE_FILE_PATH", tmpDir+"/state.json")

		session := types.Session{
			ID:           "session-pcap-test",
			BinaryID:     "binary-1",
			State:        types.SessionStateStopped,
			PCAPFilePath: tmpDir + "/session.pcap",
			PCAPEnabled:  false,
			CreatedAt:    mockTime,
		}
		storeMu.Lock()
		sessions["session-pcap-test"] = session
		storeMu.Unlock()

		handler := HandleUpdateSession(mockMgr)
		req := httptest.NewRequest("PATCH", "/api/sessions/session-pcap-test", strings.NewReader(`{
			"pcap_enabled": true
		}`))
		req.SetPathValue("id", "session-pcap-test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		storeMu.RLock()
		updatedSession, _ := sessions["session-pcap-test"]
		storeMu.RUnlock()

		if !updatedSession.PCAPEnabled {
			t.Fatalf("Expected PCAP to be enabled")
		}
	})

	t.Run("Download PCAP file", func(t *testing.T) {
		ResetSessionStore()

		tmpDir := t.TempDir()
		pcapFile := tmpDir + "/test.pcap"

		// Create test PCAP file
		testData := []byte("This is test PCAP data")
		if err := os.WriteFile(pcapFile, testData, 0644); err != nil {
			t.Fatalf("Failed to create test PCAP file: %v", err)
		}

		session := types.Session{
			ID:           "session-download-test",
			PCAPFilePath: pcapFile,
			PCAPEnabled:  true,
		}
		storeMu.Lock()
		sessions["session-download-test"] = session
		storeMu.Unlock()

		handler := HandleDownloadPCAP()
		req := httptest.NewRequest("GET", "/api/sessions/session-download-test/pcap", nil)
		req.SetPathValue("id", "session-download-test")
		req = withUserIdentity(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected 200, got %d", rec.Code)
		}

		// Verify content type
		if rec.Header().Get("Content-Type") != "application/octet-stream" {
			t.Fatalf("Expected application/octet-stream, got %s", rec.Header().Get("Content-Type"))
		}

		// Verify content
		if string(rec.Body.Bytes()) != string(testData) {
			t.Fatalf("Expected PCAP content to match")
		}
	})
}

// TestPastaIntegration tests pasta-mode TAP interface lifecycle
func TestPastaIntegration(t *testing.T) {
	t.Run("Pasta mode validation rejects conflicting bridge config", func(t *testing.T) {
		// Test the validatePastaModeRequirements function directly
		interfaces := []types.TapConfig{
			{
				Name:            "tap0",
				HostInterface:   "tap0",
				PastaMode:       true,
				EnableBridge:    true, // Conflict!
				BridgeInterface: "br0",
			},
		}

		err := validatePastaModeRequirements(interfaces)
		if err == nil {
			t.Fatalf("Expected validation error for conflicting pasta and bridge modes")
		}
		if !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("Expected error about mutually exclusive modes, got: %s", err.Error())
		}
	})

	t.Run("Pasta mode validation accepts valid config", func(t *testing.T) {
		// Valid pasta mode config
		interfaces := []types.TapConfig{
			{
				Name:          "tap0",
				HostInterface: "tap0",
				PastaMode:     true,
				EnableBridge:  false, // Not enabled
			},
		}

		err := validatePastaModeRequirements(interfaces)
		if err != nil {
			t.Fatalf("Expected validation to pass for valid pasta config, got: %v", err)
		}
	})

	t.Run("Pasta mode allows multiple TAP interfaces", func(t *testing.T) {
		// Multiple TAP interfaces in pasta mode
		interfaces := []types.TapConfig{
			{
				Name:          "tap0",
				HostInterface: "tap0",
				PastaMode:     true,
			},
			{
				Name:          "tap1",
				HostInterface: "tap1",
				PastaMode:     true,
			},
		}

		err := validatePastaModeRequirements(interfaces)
		if err != nil {
			t.Fatalf("Expected validation to pass for multiple pasta interfaces, got: %v", err)
		}
	})

	t.Run("Mixed bridge and pasta modes in same session", func(t *testing.T) {
		// Mixing bridge and pasta modes (valid - they just use different TAP configs)
		interfaces := []types.TapConfig{
			{
				Name:            "tap0",
				HostInterface:   "tap0",
				PastaMode:       true,
				EnableBridge:    false,
			},
			{
				Name:            "tap1",
				HostInterface:   "tap1",
				PastaMode:       false,
				EnableBridge:    true,
				BridgeInterface: "br0",
			},
		}

		err := validatePastaModeRequirements(interfaces)
		if err != nil {
			t.Fatalf("Expected validation to pass for mixed modes (different TAPs), got: %v", err)
		}
	})
}

// Helper to reset session store
func ResetSessionStore() {
	storeMu.Lock()
	defer storeMu.Unlock()
	sessions = map[string]types.Session{}
}
