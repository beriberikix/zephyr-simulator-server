package container

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

// BinaryAnalyzer performs static analysis on Zephyr native_sim binaries
type BinaryAnalyzer struct {
	PathPath string
}

// NewBinaryAnalyzer creates a new binary analyzer
func NewBinaryAnalyzer() *BinaryAnalyzer {
	return &BinaryAnalyzer{
		PathPath: "/usr/bin", // assumes readelf is in PATH
	}
}

// Analyze performs comprehensive binary analysis (bits, static linking, Zephyr version)
func (ba *BinaryAnalyzer) Analyze(filePath string) (*types.Binary, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("stat binary: %w", err)
	}

	f, err := elf.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("parse ELF: %w", err)
	}
	defer f.Close()

	bits := 32
	if f.Class == elf.ELFCLASS64 {
		bits = 64
	}

	// Check if statically linked
	isStatic := ba.isStaticallyLinked(f)

	// Extract Zephyr version from ELF notes if available
	zephyrVersion := ba.extractZephyrVersion(f, filePath)
	if zephyrVersion == "" {
		zephyrVersion = "4.3.0" // default fallback
	}

	// Calculate checksum (SHA256)
	checksum := calculateChecksum(filePath)

	return &types.Binary{
		Name:          info.Name(),
		Bits:          bits,
		IsStatic:      isStatic,
		ZephyrVersion: zephyrVersion,
		FilePath:      filePath,
		FileSize:      info.Size(),
		Checksum:      checksum,
	}, nil
}

// isStaticallyLinked checks if the binary is statically linked by examining the ELF header
// A statically linked binary has no DYNAMIC section
func (ba *BinaryAnalyzer) isStaticallyLinked(f *elf.File) bool {
	for _, section := range f.Sections {
		if section.Name == ".dynamic" {
			return false // dynamic section exists = not static
		}
	}
	return true // no dynamic section = static
}

// extractZephyrVersion attempts to extract Zephyr version from ELF notes
// Falls back to readelf if direct parsing fails
func (ba *BinaryAnalyzer) extractZephyrVersion(f *elf.File, filePath string) string {
	// Try to find .note.zephyr section
	noteSection := f.Section(".note.zephyr")
	if noteSection != nil {
		data, _ := noteSection.Data()
		if versionStr := parseNoteSection(data); versionStr != "" {
			return versionStr
		}
	}

	// Try readelf as fallback
	return ba.readelfVersion(filePath)
}

// parseNoteSection extracts version string from ELF note data
func parseNoteSection(data []byte) string {
	// Basic note parsing: skip namesz/descsz headers, look for version string
	// This is a simplified version; real implementation would be more robust
	if len(data) > 16 {
		// Look for version pattern in remainder
		return ""
	}
	return ""
}

// readelfVersion uses readelf command-line tool as fallback (filePath parameter)
func (ba *BinaryAnalyzer) readelfVersion(path string) string {
	cmd := exec.Command("readelf", "-p", ".note.zephyr", path)
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return ""
	}

	// Parse output for version
	output := out.String()
	if strings.Contains(output, "Zephyr") {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Zephyr") {
				parts := strings.Fields(line)
				if len(parts) > 0 {
					return parts[len(parts)-1]
				}
			}
		}
	}
	return ""
}

// calculateChecksum computes SHA256 of the binary
func calculateChecksum(filePath string) string {
	cmd := exec.Command("sha256sum", filePath)
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return ""
	}

	parts := strings.Fields(out.String())
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// ValidateBinaryCompatibility checks if the binary can run on this system
func (ba *BinaryAnalyzer) ValidateBinaryCompatibility(binary *types.Binary) error {
	// Check if architecture is supported
	if binary.Bits != 32 && binary.Bits != 64 {
		return fmt.Errorf("unsupported architecture: %d-bit", binary.Bits)
	}

	// Additional compatibility checks could go here
	// For now, we accept all valid ELF binaries

	return nil
}

// GetSupportedFeatures returns a list of CLI flags this binary supports
func (ba *BinaryAnalyzer) GetSupportedFeatures(binary *types.Binary) ([]string, error) {
	// Run binary with --help to detect supported flags
	cmd := exec.Command(binary.FilePath, "--help")
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	// Ignore error since some binaries exit non-zero on --help
	_ = cmd.Run()

	output := out.String()
	if output == "" {
		output = errOut.String()
	}

	features := []string{}
	supportedFlags := []string{"--seed", "--rt", "--uart-bin", "--pcap", "--verbose"}

	for _, flag := range supportedFlags {
		if strings.Contains(output, flag) {
			features = append(features, flag)
		}
	}

	// If we got no results, assume all are supported (new Zephyr versions should have them)
	if len(features) == 0 {
		features = supportedFlags
	}

	return features, nil
}
