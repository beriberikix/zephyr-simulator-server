package container

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
	"github.com/docker/docker/api/types/mount"
)

var runCommand = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// NetworkManager handles creation and lifecycle of network interfaces for emulator sessions
type NetworkManager struct {
	sessionID string
}

type HostNetworkSetupItem struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Detail   string `json:"detail,omitempty"`
}

type HostNetworkSetupResult struct {
	Items []HostNetworkSetupItem `json:"items"`
}

// NewNetworkManager creates a network manager for a session
func NewNetworkManager(sessionID string) *NetworkManager {
	return &NetworkManager{sessionID: sessionID}
}

// CreateTapInterface creates a TAP interface on the host
// Returns the host interface name and any error
func (nm *NetworkManager) CreateTapInterface(config *types.TapConfig) (string, error) {
	ifName := config.HostInterface
	if ifName == "" {
		// Auto-generate name if not specified
		ifName = fmt.Sprintf("tap-%s", nm.sessionID[:8])
		config.HostInterface = ifName
	}

	// Check if interface already exists
	if _, err := net.InterfaceByName(ifName); err == nil {
		return "", fmt.Errorf("TAP interface %s already exists", ifName)
	}

	// Create TAP interface using ip tool
	if out, err := runCommand("ip", "tuntap", "add", "dev", ifName, "mode", "tap"); err != nil {
		return "", fmt.Errorf("failed to create TAP interface: %s: %w", string(out), err)
	}

	// Bring interface up
	if out, err := runCommand("ip", "link", "set", ifName, "up"); err != nil {
		// Attempt cleanup
		_, _ = runCommand("ip", "tuntap", "del", "dev", ifName, "mode", "tap")
		return "", fmt.Errorf("failed to bring up TAP interface: %s: %w", string(out), err)
	}

	// Set IP address if provided
	if config.IPAddress != "" && config.Netmask != "" {
		cidr := fmt.Sprintf("%s/%s", config.IPAddress, nm.maskToCIDR(config.Netmask))
		if out, err := runCommand("ip", "addr", "add", cidr, "dev", ifName); err != nil {
			// Attempt cleanup
			_, _ = runCommand("ip", "link", "set", ifName, "down")
			_, _ = runCommand("ip", "tuntap", "del", "dev", ifName, "mode", "tap")
			return "", fmt.Errorf("failed to set IP on TAP interface: %s: %w", string(out), err)
		}
	}

	// Bridge to physical interface if requested
	if config.EnableBridge {
		if config.BridgeInterface == "" {
			_, _ = runCommand("ip", "link", "set", ifName, "down")
			_, _ = runCommand("ip", "tuntap", "del", "dev", ifName, "mode", "tap")
			return "", fmt.Errorf("bridge_interface is required when enable_bridge is true")
		}
		autoCreatedBridge, err := nm.ensureBridgeInterface(config.BridgeInterface)
		if err != nil {
			_, _ = runCommand("ip", "link", "set", ifName, "down")
			_, _ = runCommand("ip", "tuntap", "del", "dev", ifName, "mode", "tap")
			return "", fmt.Errorf("failed to ensure bridge interface: %w", err)
		}
		config.BridgeAutoCreated = autoCreatedBridge

		if err := nm.bridgeTapToInterface(ifName, config.BridgeInterface); err != nil {
			// Attempt cleanup
			_, _ = runCommand("ip", "link", "set", ifName, "down")
			_, _ = runCommand("ip", "tuntap", "del", "dev", ifName, "mode", "tap")
			return "", fmt.Errorf("failed to bridge TAP interface: %w", err)
		}
	}

	return ifName, nil
}

// RemoveTapInterface removes a TAP interface from the host
func (nm *NetworkManager) RemoveTapInterface(ifName string) error {
	// Bring interface down
	if out, err := runCommand("ip", "link", "set", ifName, "down"); err != nil {
		return fmt.Errorf("failed to bring down TAP interface: %s: %w", string(out), err)
	}

	// Remove interface
	if out, err := runCommand("ip", "tuntap", "del", "dev", ifName, "mode", "tap"); err != nil {
		return fmt.Errorf("failed to remove TAP interface: %s: %w", string(out), err)
	}

	return nil
}

// CreateTapDeviceMounts creates device mounts for TAP interfaces to be bound into the container
func (nm *NetworkManager) CreateTapDeviceMounts(tapConfigs []types.TapConfig) ([]mount.Mount, error) {
	mounts := []mount.Mount{}

	if len(tapConfigs) == 0 {
		return mounts, nil
	}

	// TAP device is typically at /dev/tap{N} or /dev/net/tun
	// We'll mount /dev/net/tun which is the standard TUN/TAP device
	devicePath := "/dev/net/tun"

	// Verify device exists
	if _, err := os.Stat(devicePath); err != nil {
		return nil, fmt.Errorf("TUN/TAP device not accessible: %s: %w", devicePath, err)
	}

	// Mount the device into the container
	mounts = append(mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: devicePath,
		Target: "/dev/net/tun",
		ReadOnly: false,
	})

	// Mount is added only once since all TAP interfaces use the same device
	return mounts, nil
}

// CreateTapMounts is a helper to integrate TAP interface creation with container mounts
func (m *Manager) createTapMounts(session *types.Session) ([]mount.Mount, error) {
	mounts := []mount.Mount{}

	if session.TapInterfaces == nil || len(session.TapInterfaces) == 0 {
		return mounts, nil
	}

	nm := NewNetworkManager(session.ID)
	isPastaMode := false
	hasKernelTap := false

	// Check if any TAP interface is in pasta mode
	for _, tapConfig := range session.TapInterfaces {
		if tapConfig.PastaMode {
			isPastaMode = true
			break
		}
	}

	// Create each TAP interface (creation is same for both modes)
	for i, tapConfig := range session.TapInterfaces {
		var err error
		var hostIfName string

		if tapConfig.TunOverUART {
			uartDevice := strings.TrimSpace(tapConfig.UARTDevicePath)
			if uartDevice == "" {
				return nil, fmt.Errorf("tap interface %d missing uart_device_path for tun_over_uart", i)
			}
			if _, statErr := os.Stat(uartDevice); statErr != nil {
				return nil, fmt.Errorf("TUN-over-UART device not accessible: %s: %w", uartDevice, statErr)
			}

			target := fmt.Sprintf("/dev/ttyTUN%d", i)
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeBind,
				Source:   uartDevice,
				Target:   target,
				ReadOnly: false,
			})
			session.TapInterfaces[i].ContainerPath = target
			continue
		}
		hasKernelTap = true

		if tapConfig.PastaMode {
			// Pasta mode: create TAP but don't bridge
			err = nm.CreateTapInterfacePasta(&tapConfig)
			hostIfName = tapConfig.HostInterface
		} else {
			// Standard bridge mode
			hostIfName, err = nm.CreateTapInterface(&tapConfig)
		}

		if err != nil {
			// Cleanup previously created interfaces on error
			for j := 0; j < i; j++ {
				nm.RemoveTapInterface(session.TapInterfaces[j].HostInterface)
			}
			return nil, fmt.Errorf("create TAP interface: %w", err)
		}
		session.TapInterfaces[i].HostInterface = hostIfName
	}

	// For pasta mode, we need different mounts
	if isPastaMode {
		return m.createPastaMounts(session)
	}

	if !hasKernelTap {
		return mounts, nil
	}

	// Add device mounts for TUN/TAP (standard bridge mode)
	tapMounts, err := nm.CreateTapDeviceMounts(session.TapInterfaces)
	if err != nil {
		// Cleanup created interfaces
		for _, config := range session.TapInterfaces {
			nm.RemoveTapInterface(config.HostInterface)
		}
		return nil, err
	}

	mounts = append(mounts, tapMounts...)
	return mounts, nil
}

// maskToCIDR converts netmask (e.g., "255.255.255.0") to CIDR bits (e.g., 24)
func (nm *NetworkManager) maskToCIDR(netmask string) string {
	ip := net.ParseIP(netmask)
	if ip == nil {
		return "24" // Default to /24 if parsing fails
	}

	mask := net.IPMask(ip.To4())
	if mask == nil {
		// Try IPv6
		mask = net.IPMask(ip.To16())
		if mask == nil {
			return "24"
		}
	}

	cidr, _ := mask.Size()
	return strconv.Itoa(cidr)
}

// bridgeTapToInterface bridges a TAP interface to a physical interface
func (nm *NetworkManager) bridgeTapToInterface(tapIf, physicalIf string) error {
	isBridge, err := nm.isBridgeInterface(physicalIf)
	if err != nil {
		return err
	}
	if !isBridge {
		return fmt.Errorf("bridge_interface %q is not a Linux bridge device", physicalIf)
	}

	if out, err := runCommand("ip", "link", "set", "dev", tapIf, "master", physicalIf); err != nil {
		return fmt.Errorf("attach TAP %s to bridge %s: %s: %w", tapIf, physicalIf, string(out), err)
	}

	return nil
}

// CreateTapInterfacePasta creates a TAP interface for pasta-mode forwarding
// In pasta mode, the TAP device handles all TCP/UDP traffic transparent to the container
func (nm *NetworkManager) CreateTapInterfacePasta(config *types.TapConfig) error {
	ifName := config.HostInterface
	if ifName == "" {
		// Auto-generate name if not specified
		ifName = fmt.Sprintf("tap-%s", nm.sessionID[:8])
		config.HostInterface = ifName
	}

	// Check if interface already exists
	if _, err := net.InterfaceByName(ifName); err == nil {
		return fmt.Errorf("TAP interface %s already exists", ifName)
	}

	// Create TAP interface (same as non-pasta mode)
	if out, err := runCommand("ip", "tuntap", "add", "dev", ifName, "mode", "tap"); err != nil {
		return fmt.Errorf("failed to create TAP interface: %s: %w", string(out), err)
	}

	// Bring TAP interface up
	if out, err := runCommand("ip", "link", "set", ifName, "up"); err != nil {
		runCommand("ip", "tuntap", "del", "dev", ifName, "mode", "tap")
		return fmt.Errorf("failed to bring up TAP interface: %s: %w", string(out), err)
	}

	// For pasta mode, we store the config but actual pasta invocation happens during container startup
	// The container will invoke pasta with -I {ifName} to handle transparent TCP/UDP forwarding
	return nil
}

func (nm *NetworkManager) ensureBridgeInterface(ifName string) (bool, error) {
	if _, err := net.InterfaceByName(ifName); err == nil {
		isBridge, bridgeErr := nm.isBridgeInterface(ifName)
		if bridgeErr != nil {
			return false, bridgeErr
		}
		if !isBridge {
			return false, fmt.Errorf("interface %q exists but is not a Linux bridge", ifName)
		}
		return false, nil
	}

	if out, err := runCommand("ip", "link", "add", "name", ifName, "type", "bridge"); err != nil {
		return false, fmt.Errorf("create bridge %s: %s: %w", ifName, string(out), err)
	}

	if out, err := runCommand("ip", "link", "set", ifName, "up"); err != nil {
		_, _ = runCommand("ip", "link", "del", "dev", ifName)
		return false, fmt.Errorf("bring bridge %s up: %s: %w", ifName, string(out), err)
	}

	return true, nil
}

func (nm *NetworkManager) removeBridgeIfAutoCreatedAndEmpty(config types.TapConfig) error {
	if !config.EnableBridge || !config.BridgeAutoCreated || config.BridgeInterface == "" {
		return nil
	}

	out, err := runCommand("ip", "-o", "link", "show", "master", config.BridgeInterface)
	if err != nil {
		return fmt.Errorf("inspect bridge members for %s: %s: %w", config.BridgeInterface, string(out), err)
	}

	if strings.TrimSpace(string(out)) != "" {
		return nil
	}

	if out, err := runCommand("ip", "link", "set", config.BridgeInterface, "down"); err != nil {
		return fmt.Errorf("bring bridge %s down: %s: %w", config.BridgeInterface, string(out), err)
	}
	if out, err := runCommand("ip", "link", "del", "dev", config.BridgeInterface); err != nil {
		return fmt.Errorf("delete bridge %s: %s: %w", config.BridgeInterface, string(out), err)
	}

	return nil
}

func (nm *NetworkManager) isBridgeInterface(ifName string) (bool, error) {
	out, err := runCommand("ip", "-d", "link", "show", "dev", ifName)
	if err != nil {
		return false, fmt.Errorf("inspect bridge interface %s: %s: %w", ifName, string(out), err)
	}

	// `ip -d link show` contains a `bridge` stanza for bridge devices.
	return strings.Contains(string(out), " bridge ") || strings.Contains(string(out), "\n    bridge "), nil
}

// CleanupTapInterfaces removes all TAP interfaces for a session
func CleanupTapInterfaces(session *types.Session) error {
	if session.TapInterfaces == nil || len(session.TapInterfaces) == 0 {
		return nil
	}

	nm := NewNetworkManager(session.ID)
	var errs []string

	for _, config := range session.TapInterfaces {
		if config.HostInterface != "" {
			if err := nm.RemoveTapInterface(config.HostInterface); err != nil {
				errs = append(errs, err.Error())
			}
		}

		if err := nm.removeBridgeIfAutoCreatedAndEmpty(config); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup TAP interfaces: %s", strings.Join(errs, "; "))
	}

	return nil
}

// SetupHostNetworking prepares host-side networking resources for advanced networking.
// It ensures requested vCAN interfaces are present/up and bridge interfaces exist/up when TAP bridge mode is enabled.
func SetupHostNetworking(canDevices []types.CanDeviceConfig, tapInterfaces []types.TapConfig) (*HostNetworkSetupResult, error) {
	if _, err := exec.LookPath("ip"); err != nil {
		return nil, fmt.Errorf("required host command 'ip' not found in PATH")
	}

	result := &HostNetworkSetupResult{Items: []HostNetworkSetupItem{}}
	var errs []string

	nm := NewNetworkManager("host-setup")

	for _, dev := range canDevices {
		ifName := strings.TrimSpace(dev.Name)
		if ifName == "" && strings.TrimSpace(dev.HostDevice) != "" {
			ifName = filepath.Base(dev.HostDevice)
		}
		if ifName == "" {
			errs = append(errs, "can device missing name/host_device")
			continue
		}

		if _, err := net.InterfaceByName(ifName); err != nil {
			if out, createErr := runCommand("ip", "link", "add", "dev", ifName, "type", "vcan"); createErr != nil {
				errs = append(errs, fmt.Sprintf("create vcan %s: %s: %v", ifName, strings.TrimSpace(string(out)), createErr))
				continue
			}
			result.Items = append(result.Items, HostNetworkSetupItem{Resource: ifName, Action: "created", Detail: "vcan interface created"})
		} else {
			result.Items = append(result.Items, HostNetworkSetupItem{Resource: ifName, Action: "reused", Detail: "vcan interface already exists"})
		}

		if out, upErr := runCommand("ip", "link", "set", ifName, "up"); upErr != nil {
			errs = append(errs, fmt.Sprintf("bring vcan %s up: %s: %v", ifName, strings.TrimSpace(string(out)), upErr))
			continue
		}
		result.Items = append(result.Items, HostNetworkSetupItem{Resource: ifName, Action: "up", Detail: "vcan interface is up"})
	}

	seenBridges := map[string]struct{}{}
	for _, tap := range tapInterfaces {
		if !tap.EnableBridge {
			continue
		}

		bridge := strings.TrimSpace(tap.BridgeInterface)
		if bridge == "" {
			errs = append(errs, "tap bridge mode requires bridge_interface")
			continue
		}
		if _, seen := seenBridges[bridge]; seen {
			continue
		}
		seenBridges[bridge] = struct{}{}

		autoCreated, err := nm.ensureBridgeInterface(bridge)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		action := "reused"
		detail := "bridge already exists"
		if autoCreated {
			action = "created"
			detail = "bridge created and brought up"
		}
		result.Items = append(result.Items, HostNetworkSetupItem{Resource: bridge, Action: action, Detail: detail})
	}

	if len(errs) > 0 {
		return result, fmt.Errorf("host network setup errors: %s", strings.Join(errs, "; "))
	}

	return result, nil
}
