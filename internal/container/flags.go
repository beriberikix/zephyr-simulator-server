package container

import (
	"fmt"
	"path/filepath"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

// FlagsBuilder translates FlagConfig into native_sim CLI arguments
type FlagsBuilder struct{}

// NewFlagsBuilder creates a new flags builder
func NewFlagsBuilder() *FlagsBuilder {
	return &FlagsBuilder{}
}

// Build generates the complete native_sim argument list
func (fb *FlagsBuilder) Build(config types.FlagConfig) []string {
	args := []string{}

	// Add real-time flag
	if config.UseRealTime {
		args = append(args, "--rt")
	}

	// Add UART backend binaries (for multi-UART support)
	for i, uartBin := range config.UARTBins {
		args = append(args, fmt.Sprintf("--uart-bin%d=%s", i, uartBin))
	}

	// Add PCAP capture (if enabled)
	if config.PCAPPath != "" {
		args = append(args, fmt.Sprintf("--pcap=%s", config.PCAPPath))
	}

	// Add SocketCAN devices.
	for _, canDevice := range config.CanDevices {
		args = append(args, fmt.Sprintf("--can-device=%s", canDevice))
	}

	// Add Bluetooth HCI device.
	if config.BluetoothDevice != "" {
		args = append(args, fmt.Sprintf("--hci-device=%s", config.BluetoothDevice))
	}

	// Add verbose flag
	if config.Verbose {
		args = append(args, "--verbose")
	}

	return args
}

// BuildForSession creates FlagConfig from a Session and returns the CLI args
func (fb *FlagsBuilder) BuildForSession(session *types.Session, binary *types.Binary) []string {
	config := types.FlagConfig{
		Seed:        session.Seed,
		UseRealTime: session.UseRealTime,
		UARTBins:    nil,
		Verbose:     false,
	}

	// Add PCAP path if enabled
	if session.PCAPEnabled && session.PCAPFilePath != "" {
		config.PCAPPath = filepath.Join("/pcap", filepath.Base(session.PCAPFilePath))
	}

	// Add CAN devices.
	for _, canDevice := range session.CanDevices {
		config.CanDevices = append(config.CanDevices, "/dev/"+canDevice.Name)
	}

	// Add Bluetooth device.
	if session.BluetoothConfig != nil && session.BluetoothConfig.Enabled {
		if session.BluetoothConfig.Transport == "hci_uart" {
			config.BluetoothDevice = "/dev/ttyBT0"
		} else {
			config.BluetoothDevice = session.BluetoothConfig.HciDevice
			if config.BluetoothDevice == "" {
				config.BluetoothDevice = "/dev/hci0"
			}
		}
	}

	return fb.Build(config)
}

// generateUARTBins creates FIFO paths for native_sim UART backends
// For MVP, we support UART0 and UART1
func (fb *FlagsBuilder) generateUARTBins(sessionID string) []string {
	return []string{
		// In a real implementation, these would be named pipes that the UART multiplexer watches
		filepath.Join("/tmp", fmt.Sprintf("session-%s-uart0.fifo", sessionID)),
		filepath.Join("/tmp", fmt.Sprintf("session-%s-uart1.fifo", sessionID)),
	}
}

// ValidateFlags checks if the given flags are valid for the binary
func (fb *FlagsBuilder) ValidateFlags(config types.FlagConfig, supportedFeatures []string) error {
	supportedMap := make(map[string]bool)
	for _, feature := range supportedFeatures {
		supportedMap[feature] = true
	}

	if config.Seed > 0 && !supportedMap["--seed"] {
		return fmt.Errorf("binary does not support --seed flag")
	}

	if config.UseRealTime && !supportedMap["--rt"] {
		return fmt.Errorf("binary does not support --rt flag")
	}

	if config.PCAPPath != "" && !supportedMap["--pcap"] {
		return fmt.Errorf("binary does not support --pcap flag")
	}

	if len(config.UARTBins) > 0 && !supportedMap["--uart-bin"] {
		return fmt.Errorf("binary does not support --uart-bin flag")
	}

	return nil
}
