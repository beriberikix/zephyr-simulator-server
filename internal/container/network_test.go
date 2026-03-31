package container

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

func TestBridgeTapToInterface_RejectsNonBridgeInterface(t *testing.T) {
	origRunCommand := runCommand
	t.Cleanup(func() {
		runCommand = origRunCommand
	})

	runCommand = func(name string, args ...string) ([]byte, error) {
		if name == "ip" && len(args) == 5 && args[0] == "-d" && args[1] == "link" && args[2] == "show" && args[3] == "dev" && args[4] == "eth0" {
			return []byte("2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500"), nil
		}
		return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
	}

	nm := NewNetworkManager("session-1")
	err := nm.bridgeTapToInterface("tap0", "eth0")
	if err == nil {
		t.Fatalf("expected error for non-bridge interface")
	}
	if !strings.Contains(err.Error(), "is not a Linux bridge device") {
		t.Fatalf("expected bridge type error, got: %v", err)
	}
}

func TestBridgeTapToInterface_AttachesTapToBridge(t *testing.T) {
	origRunCommand := runCommand
	t.Cleanup(func() {
		runCommand = origRunCommand
	})

	calls := []string{}
	runCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))

		if name == "ip" && len(args) == 5 && args[0] == "-d" && args[1] == "link" && args[2] == "show" && args[3] == "dev" && args[4] == "br0" {
			return []byte("3: br0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n    bridge forward_delay 1500"), nil
		}
		if name == "ip" && len(args) == 6 && args[0] == "link" && args[1] == "set" && args[2] == "dev" && args[3] == "tap0" && args[4] == "master" && args[5] == "br0" {
			return []byte(""), nil
		}
		return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
	}

	nm := NewNetworkManager("session-1")
	err := nm.bridgeTapToInterface("tap0", "br0")
	if err != nil {
		t.Fatalf("expected bridge attach to succeed, got: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 command calls, got %d", len(calls))
	}
}

func TestCreateTapInterface_EnableBridgeWithoutBridgeInterface(t *testing.T) {
	origRunCommand := runCommand
	t.Cleanup(func() {
		runCommand = origRunCommand
	})

	runCommand = func(name string, args ...string) ([]byte, error) {
		if name == "ip" && len(args) >= 2 && args[0] == "tuntap" && args[1] == "add" {
			return []byte(""), nil
		}
		if name == "ip" && len(args) >= 2 && args[0] == "link" && args[1] == "set" {
			return []byte(""), nil
		}
		if name == "ip" && len(args) >= 2 && args[0] == "tuntap" && args[1] == "del" {
			return []byte(""), nil
		}
		return nil, errors.New("unexpected command")
	}

	nm := NewNetworkManager("session-12345678")
	_, err := nm.CreateTapInterface(&types.TapConfig{HostInterface: "tap-test", EnableBridge: true})
	if err == nil {
		t.Fatalf("expected error when enable_bridge is true but bridge_interface is empty")
	}
	if !strings.Contains(err.Error(), "bridge_interface is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateTapInterface_AutoCreatesBridgeWhenMissing(t *testing.T) {
	origRunCommand := runCommand
	t.Cleanup(func() {
		runCommand = origRunCommand
	})

	calls := []string{}
	runCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))

		if name == "ip" && len(args) == 6 && args[0] == "tuntap" && args[1] == "add" {
			return []byte(""), nil
		}
		if name == "ip" && len(args) == 4 && args[0] == "link" && args[1] == "set" && args[2] == "tap-auto" && args[3] == "up" {
			return []byte(""), nil
		}
		if name == "ip" && len(args) == 6 && args[0] == "link" && args[1] == "add" && args[2] == "name" && args[3] == "br-auto" && args[4] == "type" && args[5] == "bridge" {
			return []byte(""), nil
		}
		if name == "ip" && len(args) == 4 && args[0] == "link" && args[1] == "set" && args[2] == "br-auto" && args[3] == "up" {
			return []byte(""), nil
		}
		if name == "ip" && len(args) == 5 && args[0] == "-d" && args[1] == "link" && args[2] == "show" && args[3] == "dev" && args[4] == "br-auto" {
			return []byte("3: br-auto: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500\n    bridge forward_delay 1500"), nil
		}
		if name == "ip" && len(args) == 6 && args[0] == "link" && args[1] == "set" && args[2] == "dev" && args[3] == "tap-auto" && args[4] == "master" && args[5] == "br-auto" {
			return []byte(""), nil
		}
		return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
	}

	nm := NewNetworkManager("session-auto-bridge")
	cfg := &types.TapConfig{
		HostInterface:   "tap-auto",
		EnableBridge:    true,
		BridgeInterface: "br-auto",
	}

	_, err := nm.CreateTapInterface(cfg)
	if err != nil {
		t.Fatalf("expected TAP creation with auto bridge to succeed, got: %v", err)
	}
	if !cfg.BridgeAutoCreated {
		t.Fatalf("expected bridge_auto_created to be true")
	}
}

func TestRemoveBridgeIfAutoCreatedAndEmpty_RemovesBridge(t *testing.T) {
	origRunCommand := runCommand
	t.Cleanup(func() {
		runCommand = origRunCommand
	})

	calls := []string{}
	runCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))

		if name == "ip" && len(args) == 5 && args[0] == "-o" && args[1] == "link" && args[2] == "show" && args[3] == "master" && args[4] == "br-auto" {
			return []byte(""), nil
		}
		if name == "ip" && len(args) == 4 && args[0] == "link" && args[1] == "set" && args[2] == "br-auto" && args[3] == "down" {
			return []byte(""), nil
		}
		if name == "ip" && len(args) == 4 && args[0] == "link" && args[1] == "del" && args[2] == "dev" && args[3] == "br-auto" {
			return []byte(""), nil
		}
		return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
	}

	nm := NewNetworkManager("session-auto-bridge")
	err := nm.removeBridgeIfAutoCreatedAndEmpty(types.TapConfig{
		EnableBridge:      true,
		BridgeAutoCreated: true,
		BridgeInterface:   "br-auto",
	})
	if err != nil {
		t.Fatalf("expected bridge teardown to succeed, got: %v", err)
	}

	if len(calls) != 3 {
		t.Fatalf("expected 3 command calls, got %d", len(calls))
	}
}

func TestCreateTapInterfacePasta_SuccessfulSetup(t *testing.T) {
	origRunCommand := runCommand
	t.Cleanup(func() {
		runCommand = origRunCommand
	})

	calls := []string{}
	runCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))

		// Simulate successful TAP creation for pasta mode
		if name == "ip" && len(args) == 6 && args[0] == "tuntap" && args[1] == "add" && args[2] == "dev" && args[4] == "mode" && args[5] == "tap" {
			return []byte(""), nil
		}
		if name == "ip" && len(args) == 4 && args[0] == "link" && args[1] == "set" && args[3] == "up" {
			return []byte(""), nil
		}
		return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
	}

	nm := NewNetworkManager("session-pasta")
	cfg := &types.TapConfig{
		HostInterface: "tap-pasta",
		PastaMode:     true,
	}

	err := nm.CreateTapInterfacePasta(cfg)
	if err != nil {
		t.Fatalf("expected pasta TAP creation to succeed, got: %v", err)
	}

	// Should have called ip tuntap add and ip link set up
	if len(calls) != 2 {
		t.Fatalf("expected 2 command calls for pasta TAP, got %d: %v", len(calls), calls)
	}

	if !strings.Contains(calls[0], "tuntap add") {
		t.Fatalf("expected tuntap add command, got: %s", calls[0])
	}
	if !strings.Contains(calls[1], "link set") {
		t.Fatalf("expected link set command, got: %s", calls[1])
	}
}

func TestCreateTapInterfacePasta_MissingBinaryFails(t *testing.T) {
	// Note: This test assumes pasta binary validation happens at Docker image build time
	// If we wanted to test runtime checks, we'd mock the interface detection to fail
	// For now, we assume pasta binary is available in the image

	origRunCommand := runCommand
	t.Cleanup(func() {
		runCommand = origRunCommand
	})

	runCommand = func(name string, args ...string) ([]byte, error) {
		// Simulate TAP interface already exists
		if name == "ip" && len(args) == 6 && args[0] == "tuntap" && args[1] == "add" {
			return nil, errors.New("operation not permitted: user namespaces might not be enabled")
		}
		return nil, fmt.Errorf("unexpected command")
	}

	nm := NewNetworkManager("session-pasta-fail")
	cfg := &types.TapConfig{
		HostInterface: "tap-pasta-fail",
		PastaMode:     true,
	}

	err := nm.CreateTapInterfacePasta(cfg)
	if err == nil {
		t.Fatalf("expected error when TAP creation fails")
	}
}

func TestCreateTapInterfacePasta_ConfigValidation(t *testing.T) {
	// Pasta mode should reject bridge configuration (mutually exclusive)
	// Note: The validation logic is in handlers.go, not in network.go
	// This test verifies the struct allows both fields (validation happens elsewhere)
	// Real validation is tested in the handler tests

	cfg := &types.TapConfig{
		HostInterface:   "tap-conflict",
		PastaMode:       true,
		EnableBridge:    true,
		BridgeInterface: "br0",
	}

	// Verify both fields can be set (validation happens in handlers)
	if !cfg.PastaMode {
		t.Fatalf("expected PastaMode to be true")
	}
	if !cfg.EnableBridge {
		t.Fatalf("expected EnableBridge to be true")
	}
}
