package container

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// Manager handles Docker container lifecycle for emulator sessions
type Manager struct {
	client       *client.Client
	flagsBuilder *FlagsBuilder
	analyzer     *BinaryAnalyzer
	baseImageURL string
	runtimeName  string
}

// NewManager creates a new container manager
func NewManager(baseImageURL, runtimeName string) (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client init: %w", err)
	}

	return &Manager{
		client:       cli,
		flagsBuilder: NewFlagsBuilder(),
		analyzer:     NewBinaryAnalyzer(),
		baseImageURL: baseImageURL,
		runtimeName:  runtimeName, // "runsc" for gVisor
	}, nil
}

// CreateContainer creates a new Docker container for the emulator session
func (m *Manager) CreateContainer(ctx context.Context, session *types.Session, binary *types.Binary) (string, error) {
	// Generate CLI flags
	flags := m.flagsBuilder.BuildForSession(session, binary)

	// Create volume mounts for persistence
	mounts, err := m.createVolumeMounts(session)
	if err != nil {
		return "", fmt.Errorf("create volume mounts: %w", err)
	}

	// Add SocketCAN device mounts.
	canMounts, err := m.createCanDeviceMounts(session)
	if err != nil {
		return "", fmt.Errorf("create CAN device mounts: %w", err)
	}
	mounts = append(mounts, canMounts...)

	// Add TAP interface mounts.
	tapMounts, err := m.createTapMounts(session)
	if err != nil {
		return "", fmt.Errorf("create TAP interface mounts: %w", err)
	}
	mounts = append(mounts, tapMounts...)

	// Add Bluetooth HCI device mounts.
	btMounts, err := m.createBluetoothMounts(session)
	if err != nil {
		return "", fmt.Errorf("create Bluetooth device mounts: %w", err)
	}
	mounts = append(mounts, btMounts...)

	// Add UART forwarding device mounts.
	uartForwardMounts, err := m.createUARTForwardingMounts(session)
	if err != nil {
		return "", fmt.Errorf("create UART forwarding mounts: %w", err)
	}
	mounts = append(mounts, uartForwardMounts...)

	// Create named pipes for UART backends
	uartFIFOs, err := m.createUARTFIFOs(session)
	defer func() {
		if err != nil {
			// Cleanup FIFOs on failure
			for _, fifo := range uartFIFOs {
				os.Remove(fifo)
			}
			// Cleanup TAP interfaces on failure
			CleanupTapInterfaces(session)
		}
	}()
	if err != nil {
		return "", fmt.Errorf("create UART FIFOs: %w", err)
	}
	session.UARTBins = append([]string(nil), uartFIFOs...)

	// Determine binary name and path inside container
	binaryName := filepath.Base(binary.FilePath)
	containerBinaryPath := filepath.Join("/emu", binaryName)

	// Build command, potentially wrapping with pasta for transparent forwarding
	cmd := []string{containerBinaryPath}
	cmd = append(cmd, flags...)

	// Wrap emulator command with gdbserver when debugging is enabled.
	if session.DebugConfig != nil && session.DebugConfig.Enabled {
		debugPort := session.DebugConfig.Port
		if debugPort == 0 {
			debugPort = 3333
		}
		gdbArgs := []string{"gdbserver"}
		if !session.DebugConfig.WaitForGDB {
			gdbArgs = append(gdbArgs, "--once")
		}
		gdbArgs = append(gdbArgs, fmt.Sprintf(":%d", debugPort))
		gdbArgs = append(gdbArgs, cmd...)
		cmd = gdbArgs
	}

	// Check if pasta mode is enabled for any TAP interface
	if session.TapInterfaces != nil && len(session.TapInterfaces) > 0 {
		isPastaMode := false
		var pastaInterfaces []string
		for _, tapConfig := range session.TapInterfaces {
			if tapConfig.PastaMode {
				isPastaMode = true
				pastaInterfaces = append(pastaInterfaces, tapConfig.HostInterface)
			}
		}

		// If pasta mode, wrap command with pasta for transparent TCP/UDP forwarding
		if isPastaMode {
			pastaCmd := []string{"pasta"}
			// Add each TAP interface as a forwarding point
			for _, ifName := range pastaInterfaces {
				pastaCmd = append(pastaCmd, "-I", ifName)
			}
			pastaCmd = append(pastaCmd, "--")
			pastaCmd = append(pastaCmd, cmd...)
			cmd = pastaCmd
		}
	}

	// Container config (separate from host config)
	containerConfig := &container.Config{
		Image:      m.baseImageURL,
		Cmd:        cmd,
		WorkingDir: "/tmp",
	}
	if session.CoverageEnabled {
		containerConfig.Env = append(containerConfig.Env,
			"GCOV_PREFIX=/coverage",
			"GCOV_PREFIX_STRIP=0",
		)
	}
	if session.AsanEnabled {
		containerConfig.Env = append(containerConfig.Env,
			"ASAN_OPTIONS=log_path=/sanitizers/asan:abort_on_error=0",
		)
	}
	if session.UbsanEnabled {
		containerConfig.Env = append(containerConfig.Env,
			"UBSAN_OPTIONS=log_path=/sanitizers/ubsan:print_stacktrace=1",
		)
	}
	if session.BluetoothConfig != nil && session.BluetoothConfig.Enabled && session.BluetoothConfig.Transport == "hci_uart" {
		baud := session.BluetoothConfig.UARTBaudRate
		if baud <= 0 {
			baud = 115200
		}
		containerConfig.Env = append(containerConfig.Env,
			"BT_TRANSPORT=hci_uart",
			fmt.Sprintf("BT_UART_BAUD=%d", baud),
		)
	}
	if session.UARTForwarding != nil && session.UARTForwarding.Enabled {
		mode := session.UARTForwarding.Mode
		if strings.TrimSpace(mode) == "" {
			mode = "tun"
		}
		containerPath := session.UARTForwarding.ContainerDevicePath
		if strings.TrimSpace(containerPath) == "" {
			containerPath = "/dev/ttyTUN0"
		}
		baud := session.UARTForwarding.BaudRate
		if baud <= 0 {
			baud = 115200
		}
		containerConfig.Env = append(containerConfig.Env,
			fmt.Sprintf("NET_UART_MODE=%s", mode),
			fmt.Sprintf("NET_UART_DEVICE=%s", containerPath),
			fmt.Sprintf("NET_UART_BAUD=%d", baud),
		)
	}

	tapUARTDevices := make([]string, 0)
	for _, tapCfg := range session.TapInterfaces {
		if tapCfg.TunOverUART && strings.TrimSpace(tapCfg.ContainerPath) != "" {
			tapUARTDevices = append(tapUARTDevices, tapCfg.ContainerPath)
		}
	}
	if len(tapUARTDevices) > 0 {
		containerConfig.Env = append(containerConfig.Env,
			"TAP_TUN_UART_ENABLED=1",
			fmt.Sprintf("TAP_TUN_UART_DEVICES=%s", strings.Join(tapUARTDevices, ",")),
		)
	}

	// Host config (network isolation)
	hostConfig := &container.HostConfig{
		Runtime:        m.runtimeName,
		Mounts:         mounts,
		CapAdd:         []string{},
		CapDrop:        m.getCapabilityDrops(session),
		IpcMode:        "private",
		UTSMode:        "host",
		ReadonlyRootfs: false,
	}

	if session.DebugConfig != nil && session.DebugConfig.Enabled {
		debugPort := session.DebugConfig.Port
		if debugPort == 0 {
			debugPort = 3333
		}
		port := nat.Port(fmt.Sprintf("%d/tcp", debugPort))
		containerConfig.ExposedPorts = nat.PortSet{port: struct{}{}}
		hostConfig.PortBindings = nat.PortMap{
			port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: fmt.Sprintf("%d", debugPort)}},
		}
	}

	// Create the container
	resp, err := m.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, fmt.Sprintf("session-%s", session.ID))
	if err != nil {
		// Fallback when runsc isn't installed on the host daemon.
		if hostConfig.Runtime != "" && strings.Contains(err.Error(), "unknown or invalid runtime name") {
			hostConfig.Runtime = ""
			resp, err = m.client.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, fmt.Sprintf("session-%s", session.ID))
			if err == nil {
				// Copy binary into the container before returning
				if copyErr := m.copyBinaryIntoContainer(ctx, resp.ID, binary.FilePath, containerBinaryPath); copyErr != nil {
					m.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{})
					return "", fmt.Errorf("copy binary: %w", copyErr)
				}
				return resp.ID, nil
			}
		}
		return "", fmt.Errorf("docker create container: %w", err)
	}

	// Copy binary into the container
	if err := m.copyBinaryIntoContainer(ctx, resp.ID, binary.FilePath, containerBinaryPath); err != nil {
		m.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{})
		return "", fmt.Errorf("copy binary: %w", err)
	}

	return resp.ID, nil
}

// copyBinaryIntoContainer uses docker cp to copy a binary file into a container
func (m *Manager) copyBinaryIntoContainer(ctx context.Context, containerID, hostBinaryPath, containerBinaryPath string) error {
	// Open the binary file from the host filesystem (accessible to the backend container)
	srcFile, err := os.Open(hostBinaryPath)
	if err != nil {
		return fmt.Errorf("open binary: %w", err)
	}
	defer srcFile.Close()

	// Get file info for TAR header
	fileInfo, err := os.Stat(hostBinaryPath)
	if err != nil {
		return fmt.Errorf("stat binary: %w", err)
	}

	// Create a TAR stream containing the binary
	reader, writer := io.Pipe()
	go func() {
		tw := tar.NewWriter(writer)
		header := &tar.Header{
			Name: filepath.Base(hostBinaryPath),
			Size: fileInfo.Size(),
			Mode: 0755,
		}
		tw.WriteHeader(header)
		io.Copy(tw, srcFile)
		tw.Close()
		writer.Close()
	}()

	// Copy the TAR stream into the container
	err = m.client.CopyToContainer(ctx, containerID, "/emu", reader, container.CopyToContainerOptions{})
	if err != nil {
		reader.Close()
		return fmt.Errorf("copy to container: %w", err)
	}

	return nil
}

// StartContainer starts a previously created container
func (m *Manager) StartContainer(ctx context.Context, containerID string) error {
	return m.client.ContainerStart(ctx, containerID, container.StartOptions{})
}

// StopContainer stops a running container
func (m *Manager) StopContainer(ctx context.Context, containerID string) error {
	timeout := int(10)
	return m.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
}

// PauseContainer pauses a running container
func (m *Manager) PauseContainer(ctx context.Context, containerID string) error {
	return m.client.ContainerPause(ctx, containerID)
}

// ResumeContainer resumes a paused container
func (m *Manager) ResumeContainer(ctx context.Context, containerID string) error {
	return m.client.ContainerUnpause(ctx, containerID)
}

// RemoveContainer removes a stopped container
func (m *Manager) RemoveContainer(ctx context.Context, containerID string) error {
	return m.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
}

// GetContainerStatus returns the current state of a container
func (m *Manager) GetContainerStatus(ctx context.Context, containerID string) (types.SessionState, error) {
	inspect, err := m.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("inspect container: %w", err)
	}

	if inspect.State.Paused {
		return types.SessionStatePaused, nil
	}
	if inspect.State.Running {
		return types.SessionStateRunning, nil
	}
	return types.SessionStateStopped, nil
}

// StreamContainerLogs streams stdout/stderr logs from a container.
func (m *Manager) StreamContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	return m.client.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       "200",
	})
}

// IsContainerTTY reports whether the container was created with TTY enabled.
func (m *Manager) IsContainerTTY(ctx context.Context, containerID string) (bool, error) {
	inspect, err := m.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, fmt.Errorf("inspect container: %w", err)
	}
	return inspect.Config.Tty, nil
}

// createVolumeMounts creates Docker volume mounts for the session
func (m *Manager) createVolumeMounts(session *types.Session) ([]mount.Mount, error) {
	volumeName := fmt.Sprintf("zephyr-session-%s", session.ID)
	tmpVolumeName := fmt.Sprintf("zephyr-session-tmp-%s", session.ID)

	mounts := []mount.Mount{
		{
			Type:     mount.TypeVolume,
			Source:   volumeName,
			Target:   "/emu",
			ReadOnly: false,
		},
		{
			Type:     mount.TypeVolume,
			Source:   tmpVolumeName,
			Target:   "/tmp",
			ReadOnly: false,
		},
	}

	if session.CoverageEnabled && strings.TrimSpace(session.CoverageDir) != "" {
		if err := os.MkdirAll(session.CoverageDir, 0755); err != nil {
			return nil, fmt.Errorf("prepare coverage dir: %w", err)
		}
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   session.CoverageDir,
			Target:   "/coverage",
			ReadOnly: false,
		})
	}

	if (session.AsanEnabled || session.UbsanEnabled) && strings.TrimSpace(session.SanitizerDir) != "" {
		if err := os.MkdirAll(session.SanitizerDir, 0755); err != nil {
			return nil, fmt.Errorf("prepare sanitizer dir: %w", err)
		}
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   session.SanitizerDir,
			Target:   "/sanitizers",
			ReadOnly: false,
		})
	}

	if session.PCAPEnabled && strings.TrimSpace(session.PCAPFilePath) != "" {
		pcapDir := filepath.Dir(session.PCAPFilePath)
		if err := os.MkdirAll(pcapDir, 0755); err != nil {
			return nil, fmt.Errorf("prepare pcap dir: %w", err)
		}
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   pcapDir,
			Target:   "/pcap",
			ReadOnly: false,
		})
	}

	return mounts, nil
}

// createUARTFIFOs creates named pipes for UART backends
func (m *Manager) createUARTFIFOs(session *types.Session) ([]string, error) {
	fifoDir := filepath.Join("/tmp", fmt.Sprintf("session-%s-uart", session.ID))
	if err := os.MkdirAll(fifoDir, 0755); err != nil {
		return nil, fmt.Errorf("create FIFO dir: %w", err)
	}

	fifos := []string{}
	// Create UART0 and UART1 FIFO paths
	for i := 0; i < 2; i++ {
		fifoPath := filepath.Join(fifoDir, fmt.Sprintf("uart%d.fifo", i))
		// FIFO creation is deferred to container startup
		fifos = append(fifos, fifoPath)
	}

	return fifos, nil
}

// createCanDeviceMounts creates device mounts for SocketCAN interfaces.
func (m *Manager) createCanDeviceMounts(session *types.Session) ([]mount.Mount, error) {
	mounts := []mount.Mount{}

	if session.CanDevices == nil || len(session.CanDevices) == 0 {
		return mounts, nil
	}

	// Mount each CAN device into the container
	for _, canDevice := range session.CanDevices {
		// Verify host device exists
		if _, err := os.Stat(canDevice.HostDevice); err != nil {
			return nil, fmt.Errorf("CAN device not accessible: %s: %w", canDevice.HostDevice, err)
		}

		// Create device mount (bind mount from host device)
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   canDevice.HostDevice,
			Target:   "/dev/" + canDevice.Name, // e.g., /dev/vcan0
			ReadOnly: false,
		})
	}

	return mounts, nil
}

// createBluetoothMounts creates device mounts for Bluetooth HCI.
func (m *Manager) createBluetoothMounts(session *types.Session) ([]mount.Mount, error) {
	mounts := []mount.Mount{}

	if session.BluetoothConfig == nil || !session.BluetoothConfig.Enabled {
		return mounts, nil
	}

	transport := strings.TrimSpace(session.BluetoothConfig.Transport)
	if transport == "" {
		transport = "hci"
	}

	if transport == "hci_uart" {
		uartDevice := strings.TrimSpace(session.BluetoothConfig.UARTDevicePath)
		if uartDevice == "" {
			return nil, fmt.Errorf("bluetooth UART device path is required for hci_uart transport")
		}
		if _, err := os.Stat(uartDevice); err != nil {
			return nil, fmt.Errorf("Bluetooth UART device not accessible: %s: %w", uartDevice, err)
		}
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   uartDevice,
			Target:   "/dev/ttyBT0",
			ReadOnly: false,
		})
	} else {
		// Mount HCI device
		hciDevice := session.BluetoothConfig.HostDevicePath
		if hciDevice == "" {
			// Auto-generate from index (e.g., index 0 -> /dev/hci0)
			hciDevice = fmt.Sprintf("/dev/hci%d", session.BluetoothConfig.HciDeviceIndex)
		}

		// Verify HCI device exists
		if _, err := os.Stat(hciDevice); err != nil {
			return nil, fmt.Errorf("HCI device not accessible: %s: %w", hciDevice, err)
		}

		// Mount HCI device
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   hciDevice,
			Target:   "/dev/hci0", // Map to hci0 inside container
			ReadOnly: false,
		})
	}

	// Also mount /dev/net/bnep for BNEP (Bluetooth Network Encapsulation)
	bnepDevice := "/dev/net/bnep"
	if _, err := os.Stat(bnepDevice); err == nil {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   bnepDevice,
			Target:   bnepDevice,
			ReadOnly: false,
		})
	}

	return mounts, nil
}

// createUARTForwardingMounts mounts host UART devices into the container for TUN-over-UART forwarding.
func (m *Manager) createUARTForwardingMounts(session *types.Session) ([]mount.Mount, error) {
	mounts := []mount.Mount{}

	if session.UARTForwarding == nil || !session.UARTForwarding.Enabled {
		return mounts, nil
	}

	hostPath := strings.TrimSpace(session.UARTForwarding.HostDevicePath)
	if hostPath == "" {
		return nil, fmt.Errorf("uart_forwarding.host_device_path is required")
	}
	if _, err := os.Stat(hostPath); err != nil {
		return nil, fmt.Errorf("UART forwarding host device not accessible: %s: %w", hostPath, err)
	}

	targetPath := strings.TrimSpace(session.UARTForwarding.ContainerDevicePath)
	if targetPath == "" {
		targetPath = "/dev/ttyTUN0"
	}

	mounts = append(mounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   hostPath,
		Target:   targetPath,
		ReadOnly: false,
	})

	return mounts, nil
}

// createPastaMounts creates the necessary mounts for pasta-mode TAP interfaces
func (m *Manager) createPastaMounts(session *types.Session) ([]mount.Mount, error) {
	mounts := []mount.Mount{}

	// Pasta requires /dev/net/tun just like standard TAP
	devicePath := "/dev/net/tun"
	if _, err := os.Stat(devicePath); err != nil {
		return nil, fmt.Errorf("TUN/TAP device not accessible for pasta mode: %s: %w", devicePath, err)
	}

	mounts = append(mounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   devicePath,
		Target:   "/dev/net/tun",
		ReadOnly: false,
	})

	// Note: proc and sysfs are typically already available in containers
	// No need to explicitly mount them for pasta to work

	return mounts, nil
}

// Close closes the Docker client
func (m *Manager) Close() error {
	return m.client.Close()
}

// getCapabilityDrops determines which capabilities should be dropped based on session networking config
// This allows networking features (TAP, SocketCAN, Bluetooth) to work while still maintaining security
func (m *Manager) getCapabilityDrops(session *types.Session) []string {
	drops := []string{
		"CAP_NET_ADMIN", // Required for TAP, SocketCAN
		"CAP_NET_RAW",   // Required for SocketCAN, raw sockets
		"CAP_SYS_ADMIN", // Required for eBPF, advanced networking
	}

	// If TAP interfaces are requested, don't drop CAP_NET_ADMIN
	if session.TapInterfaces != nil && len(session.TapInterfaces) > 0 {
		drops = removeCapability(drops, "CAP_NET_ADMIN")
	}

	// If CAN devices are requested, don't drop CAP_NET_ADMIN and CAP_NET_RAW
	if session.CanDevices != nil && len(session.CanDevices) > 0 {
		drops = removeCapability(drops, "CAP_NET_ADMIN")
		drops = removeCapability(drops, "CAP_NET_RAW")
	}

	// If Bluetooth is enabled, may need CAP_SYS_ADMIN (depending on mode)
	if session.BluetoothConfig != nil && session.BluetoothConfig.Enabled {
		// Keep at least CAP_NET_RAW for HCI operations
		drops = removeCapability(drops, "CAP_NET_RAW")
	}

	return drops
}

// removeCapability removes a capability from the drop list
func removeCapability(drops []string, cap string) []string {
	for i, c := range drops {
		if c == cap {
			return append(drops[:i], drops[i+1:]...)
		}
	}
	return drops
}
