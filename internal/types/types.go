package types

import (
	"time"
)

// Binary represents an uploaded Zephyr native_sim binary
type Binary struct {
	ID            string    `json:"id" db:"id"`
	Name          string    `json:"name" db:"name"`
	Bits          int       `json:"bits" db:"bits"` // 32 or 64
	IsStatic      bool      `json:"is_static" db:"is_static"`
	ZephyrVersion string    `json:"zephyr_version" db:"zephyr_version"` // extracted from ELF notes
	UploadedAt    time.Time `json:"uploaded_at" db:"uploaded_at"`
	FilePath      string    `json:"file_path" db:"file_path"` // absolute path on host
	FileSize      int64     `json:"file_size" db:"file_size"`
	Checksum      string    `json:"checksum" db:"checksum"` // SHA256 for integrity
}

// Session represents an active/inactive emulator session
type Session struct {
	ID              string            `json:"id" db:"id"`
	BinaryID        string            `json:"binary_id" db:"binary_id"`
	Binary          *Binary           `json:"-" db:"-"`         // populated on retrieval
	State           SessionState      `json:"state" db:"state"` // running|paused|stopped
	Seed            uint64            `json:"seed" db:"seed"`
	UseRealTime     bool              `json:"use_real_time" db:"use_real_time"`
	ContainerID     string            `json:"container_id" db:"container_id"` // Docker container ID
	CreatedAt       time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at" db:"updated_at"`
	SnapshotData    string            `json:"snapshot_data" db:"snapshot_data"` // JSON blob
	TimeoutSeconds  int               `json:"timeout_seconds" db:"timeout_seconds"`
	Uptime          int64             `json:"uptime" db:"uptime"` // seconds running (excluding paused)
	UARTBins        []string          `json:"uart_bins" db:"-"` // FIFO backend paths for UART streaming
	PCAPFilePath    string            `json:"pcap_file_path" db:"pcap_file_path"`
	PCAPEnabled     bool              `json:"pcap_enabled" db:"pcap_enabled"`
	CoverageEnabled bool              `json:"coverage_enabled" db:"coverage_enabled"`
	CoverageDir     string            `json:"coverage_dir" db:"coverage_dir"`
	AsanEnabled     bool              `json:"asan_enabled" db:"asan_enabled"`
	UbsanEnabled    bool              `json:"ubsan_enabled" db:"ubsan_enabled"`
	SanitizerDir    string            `json:"sanitizer_dir" db:"sanitizer_dir"`
	// Ownership fields for auth/RBAC.
	OwnerType string `json:"owner_type" db:"owner_type"` // "anonymous" | "user"
	OwnerID   string `json:"owner_id" db:"owner_id"`     // anon UUID or PocketBase user ID
	NetworkConfig   *NetworkConfig    `json:"network_config" db:"-"`   // Networking config (not persisted to DB)
	CanDevices      []CanDeviceConfig `json:"can_devices" db:"-"`      // CAN interfaces
	TapInterfaces   []TapConfig       `json:"tap_interfaces" db:"-"`   // TAP interfaces
	BluetoothConfig *BluetoothConfig  `json:"bluetooth_config" db:"-"` // Bluetooth HCI
	UARTForwarding  *UARTForwardingConfig `json:"uart_forwarding" db:"-"` // UART-based network forwarding
	DebugConfig     *DebugConfig      `json:"debug_config" db:"-"`     // gdbserver configuration
}

type SessionState string

const (
	SessionStateRunning SessionState = "running"
	SessionStatePaused  SessionState = "paused"
	SessionStateStopped SessionState = "stopped"
)

// Snapshot captures the full state of a session for restoration
type Snapshot struct {
	SessionID   string                 `json:"session_id"`
	BinaryID    string                 `json:"binary_id"`
	State       SessionState           `json:"state"`
	Seed        uint64                 `json:"seed"`
	UseRealTime bool                   `json:"use_real_time"`
	CreatedAt   time.Time              `json:"created_at"`
	Flags       map[string]interface{} `json:"flags"` // all emulator flags
	Volumes     SnapshotVolumes        `json:"volumes"`
}

// SnapshotVolumes describes volume locations for restoration
type SnapshotVolumes struct {
	FlashPath  string `json:"flash_path"`
	EEPROMPath string `json:"eeprom_path"`
}

// UARTLog represents a terminal output event
type UARTLog struct {
	ID        string    `json:"id" db:"id"`
	SessionID string    `json:"session_id" db:"session_id"`
	UARTIdx   int       `json:"uart_idx" db:"uart_idx"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	Data      string    `json:"data" db:"data"`
}

// Config stores system-wide settings
type Config struct {
	ID                    string `json:"id" db:"id"`
	DefaultTimeoutSec     int    `json:"default_timeout_sec" db:"default_timeout_sec"`
	DefaultSeed           uint64 `json:"default_seed" db:"default_seed"`
	PCAPEnabledDefault    bool   `json:"pcap_enabled_default" db:"pcap_enabled_default"`
	MaxConcurrentSessions int    `json:"max_concurrent_sessions" db:"max_concurrent_sessions"`
}

// SSE Event types for real-time updates
type SSEEventType string

const (
	EventContainerStateChange SSEEventType = "container_state_change"
	EventUARTData             SSEEventType = "uart_data"
	EventSessionUpdated       SSEEventType = "session_updated"
	EventError                SSEEventType = "error"
)

type SSEEvent struct {
	Type      SSEEventType `json:"type"`
	Timestamp time.Time    `json:"timestamp"`
	Data      interface{}  `json:"data"`
}

type ContainerStateChangeEvent struct {
	SessionID string       `json:"session_id"`
	OldState  SessionState `json:"old_state"`
	NewState  SessionState `json:"new_state"`
	Message   string       `json:"message"`
}

type UARTDataEvent struct {
	SessionID string `json:"session_id"`
	UARTIdx   int    `json:"uart_idx"`
	Data      string `json:"data"`
}

// FlagConfig describes the configuration for native_sim CLI flags
type FlagConfig struct {
	Seed            uint64
	UseRealTime     bool
	UARTBins        []string // paths to FIFO backends
	PCAPPath        string   // if non-empty, enables PCAP
	Verbose         bool
	CanDevices      []string // CAN device names
	TapInterfaces   []string // TAP interface names
	BluetoothDevice string   // Bluetooth HCI device path
}

// Networking configuration types.

// CanDeviceConfig describes a SocketCAN interface for the session
type CanDeviceConfig struct {
	Name          string `json:"name"`           // e.g., "vcan0"
	HostDevice    string `json:"host_device"`    // Host device path (e.g., "/dev/vcan0")
	ContainerPath string `json:"container_path"` // Container path
	Bitrate       uint32 `json:"bitrate"`        // Optional bitrate in bps
}

// TapConfig describes a TAP interface for the session
type TapConfig struct {
	Name              string `json:"name"`                          // TAP interface name
	HostInterface     string `json:"host_interface"`                // Host side interface (e.g., "tap0")
	ContainerPath     string `json:"container_path"`                // Container filesystem path if needed
	IPAddress         string `json:"ip_address"`                    // Optional container IP
	Netmask           string `json:"netmask"`                       // Optional netmask
	EnableBridge      bool   `json:"enable_bridge"`                 // Bridge to physical interface
	BridgeInterface   string `json:"bridge_interface"`              // Physical interface to bridge to
	BridgeAutoCreated bool   `json:"bridge_auto_created,omitempty"` // Internal flag: bridge created by server
	PastaMode         bool   `json:"pasta_mode"`                    // Use pasta for transparent TCP/UDP forwarding instead of bridge
	TunOverUART       bool   `json:"tun_over_uart"`                 // Use UART transport instead of host TAP interface
	UARTDevicePath    string `json:"uart_device_path"`              // Host UART device path for TUN-over-UART
	UARTBaudRate      int    `json:"uart_baud_rate"`                // UART baud rate when tun_over_uart is true
}

// BluetoothConfig describes Bluetooth HCI configuration for the session
type BluetoothConfig struct {
	Enabled         bool   `json:"enabled"`
	Transport       string `json:"transport"`        // "hci" (default) or "hci_uart"
	HciDevice       string `json:"hci_device"`       // e.g., "/dev/hci0"
	HciDeviceIndex  int    `json:"hci_device_index"` // e.g., 0 (for hci0)
	HostDevicePath  string `json:"host_device_path"` // Full host device path
	UARTDevicePath  string `json:"uart_device_path"` // UART device path when transport is hci_uart
	UARTBaudRate    int    `json:"uart_baud_rate"`   // UART baud rate when transport is hci_uart
	AdvertisingMode string `json:"advertising_mode"` // "connectable", "scannable", "non_connectable"
}

// UARTForwardingConfig describes generic UART forwarding for networking transports.
type UARTForwardingConfig struct {
	Enabled             bool   `json:"enabled"`
	Mode                string `json:"mode"`                   // "tun" currently supported
	HostDevicePath      string `json:"host_device_path"`       // Host UART device path
	ContainerDevicePath string `json:"container_device_path"`  // Device path in container, default /dev/ttyTUN0
	BaudRate            int    `json:"baud_rate"`              // UART baud rate
	MTU                 int    `json:"mtu"`                    // Optional MTU hint for forwarding stack
}

// DebugConfig describes gdbserver configuration for a session.
type DebugConfig struct {
	Enabled    bool `json:"enabled"`
	Port       int  `json:"port"`         // Host/container port for gdbserver
	WaitForGDB bool `json:"wait_for_gdb"` // If true, block target start until debugger attaches
}

// NetworkConfig describes overall networking configuration for a session
type NetworkConfig struct {
	EnableIsolation    bool     `json:"enable_isolation"`     // Isolate from host network
	NetNamespacePath   string   `json:"net_namespace_path"`   // Optional custom network namespace
	DNSServers         []string `json:"dns_servers"`          // Custom DNS servers
	ExtraHosts         []string `json:"extra_hosts"`          // Extra /etc/hosts entries
	EnableUSBEmulation bool     `json:"enable_usb_emulation"` // Future: USB device simulation
}
