package container

import (
	"strings"
	"testing"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

func TestBuildForSession_DoesNotInjectUnsupportedFlagsByDefault(t *testing.T) {
	fb := NewFlagsBuilder()
	session := &types.Session{
		ID:          "session-1",
		Seed:        12345,
		UseRealTime: false,
	}
	binary := &types.Binary{ID: "bin-1"}

	args := fb.BuildForSession(session, binary)
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "--seed=") {
		t.Fatalf("expected no --seed flag, got args: %v", args)
	}
	if strings.Contains(joined, "--uart-bin") {
		t.Fatalf("expected no --uart-bin flags, got args: %v", args)
	}
	if strings.Contains(joined, "--verbose") {
		t.Fatalf("expected no --verbose flag, got args: %v", args)
	}
}

func TestBuildForSession_UsesContainerPCAPPath(t *testing.T) {
	fb := NewFlagsBuilder()
	session := &types.Session{
		ID:           "session-1",
		PCAPEnabled:  true,
		PCAPFilePath: "/data/pcaps/session-123.pcap",
	}
	binary := &types.Binary{ID: "bin-1"}

	args := fb.BuildForSession(session, binary)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--pcap=/pcap/session-123.pcap") {
		t.Fatalf("expected container pcap path in args, got: %v", args)
	}
	if strings.Contains(joined, "--pcap=/data/pcaps") {
		t.Fatalf("expected no host pcap path in args, got: %v", args)
	}
}
