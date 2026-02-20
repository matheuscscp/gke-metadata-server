// Copyright 2026 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

// Based on https://github.com/controlplaneio-fluxcd/flux-operator/blob/9b9218f1f189303c739f095e4e0ba5a85d1b69d5/internal/builder/preflight.go

// Copyright 2025 Stefan Prodan.
// SPDX-License-Identifier: AGPL-3.0

package preflight

import (
	"crypto/fips140"
	"fmt"
	"os"
	"strings"
)

// Option is a function that configures the preflight checks.
type Option func(*options)

type options struct {
	containerOSMap map[string]int
}

// WithContainerOS sets container OS requirements.
func WithContainerOS(osName string, minVersion int) Option {
	return func(opts *options) {
		if opts.containerOSMap == nil {
			opts.containerOSMap = make(map[string]int)
		}
		opts.containerOSMap[osName] = minVersion
	}
}

// Check verifies if the container image is compatible with the
// requirements. Checks only run when KUBERNETES_SERVICE_HOST is
// set (i.e. running in-cluster).
func Check(opts ...Option) error {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		return nil
	}

	// Perform FIPS 140-3 integrity check.
	if !fips140.Enabled() {
		return fmt.Errorf("FIPS 140-3 mode is not enabled")
	}

	// Verify that the container OS matches the expected distros.
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return fmt.Errorf("failed to read /etc/os-release: %w", err)
	}
	osInfo, err := ParseOSRelease(string(osRelease))
	if err != nil {
		return err
	}
	if !CheckOSMinimumVersion(o.containerOSMap, osInfo) {
		return fmt.Errorf("unsupported container OS version: %s", osInfo["VERSION"])
	}

	return nil
}

// ParseOSRelease returns a map of key-value pairs representing the OS information.
func ParseOSRelease(content string) (map[string]string, error) {
	result := make(map[string]string)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes from value if present.
		if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
			(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
			value = value[1 : len(value)-1]
		}

		result[key] = value
	}

	if _, exists := result["VERSION_ID"]; !exists {
		return nil, fmt.Errorf("missing VERSION_ID in OS release information")
	}

	return result, nil
}

// CheckOSMinimumVersion checks if the OS info matches the minimum requirements.
func CheckOSMinimumVersion(osVersions map[string]int, osInfo map[string]string) bool {
	var matchedVersion int
	nameMatches := false

	// Check if any OS name matches and get the corresponding minimum version.
	for osName, minVersion := range osVersions {
		if strings.EqualFold(osInfo["PRETTY_NAME"], osName) || strings.EqualFold(osInfo["ID"], osName) {
			nameMatches = true
			matchedVersion = minVersion
			break
		}
	}

	if !nameMatches {
		return false
	}

	versionID := osInfo["VERSION_ID"]
	if versionID == "" {
		return false
	}

	// Extract major version from VERSION_ID.
	var actualVersion int
	if _, err := fmt.Sscanf(versionID, "%d", &actualVersion); err != nil {
		return false
	}

	// Check if the minimum version is met.
	return actualVersion >= matchedVersion
}
