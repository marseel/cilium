// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package features

import (
	"fmt"
	"testing"

	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/option"

	"github.com/stretchr/testify/assert"
)

type mockFeaturesParams struct {
	TunnelConfig    string
	CNIChainingMode string
	MutualAuth      bool
}

func (m mockFeaturesParams) TunnelProtocol() string {
	return m.TunnelConfig
}

func (m mockFeaturesParams) GetChainingMode() string {
	return m.CNIChainingMode
}

func (m mockFeaturesParams) IsMutualAuthEnabled() bool {
	return m.MutualAuth
}

func TestUpdateNetworkMode(t *testing.T) {
	tests := []struct {
		name         string
		tunnelMode   string
		tunnelProto  string
		expectedMode string
	}{
		{
			name:         "Direct routing mode",
			tunnelMode:   option.RoutingModeNative,
			expectedMode: networkModeDirectRouting,
		},
		{
			name:         "Overlay VXLAN mode",
			tunnelMode:   option.RoutingModeTunnel,
			tunnelProto:  option.TunnelVXLAN,
			expectedMode: networkModeOverlayVXLAN,
		},
		{
			name:         "Overlay Geneve mode",
			tunnelMode:   option.RoutingModeTunnel,
			tunnelProto:  option.TunnelGeneve,
			expectedMode: networkModeOverlayGENEVE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				DatapathMode:           defaults.DatapathMode,
				IPAM:                   defaultIPAMModes[0],
				RoutingMode:            tt.tunnelMode,
				EnableIPv4:             true,
				IdentityAllocationMode: defaultIdentityAllocationModes[0],
			}

			params := mockFeaturesParams{
				TunnelConfig:    tt.tunnelProto,
				CNIChainingMode: defaultChainingModes[0],
			}

			metrics.update(params, config)

			// Check that only the expected mode's counter is incremented
			for _, mode := range defaultNetworkModes {
				counter, err := metrics.DPMode.GetMetricWithLabelValues(mode)
				assert.NoError(t, err)

				counterValue := counter.Get()
				if mode == tt.expectedMode {
					assert.Equal(t, float64(1), counterValue, "Expected mode %s to be incremented", mode)
				} else {
					assert.Equal(t, float64(0), counterValue, "Expected mode %s to remain at 0", mode)
				}
			}
		})
	}
}

func TestUpdateIPAMMode(t *testing.T) {
	type testCase struct {
		name         string
		IPAMMode     string
		expectedMode string
	}
	var tests []testCase
	for _, mode := range defaultIPAMModes {
		tests = append(tests, testCase{
			name:         fmt.Sprintf("IPAM %s mode", mode),
			IPAMMode:     mode,
			expectedMode: mode,
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				DatapathMode:           defaults.DatapathMode,
				IPAM:                   tt.IPAMMode,
				EnableIPv4:             true,
				IdentityAllocationMode: defaultIdentityAllocationModes[0],
			}

			params := mockFeaturesParams{
				CNIChainingMode: defaultChainingModes[0],
			}

			metrics.update(params, config)

			// Check that only the expected mode's counter is incremented
			for _, mode := range defaultIPAMModes {
				counter, err := metrics.DPIPAM.GetMetricWithLabelValues(mode)
				assert.NoError(t, err)

				counterValue := counter.Get()
				if mode == tt.expectedMode {
					assert.Equal(t, float64(1), counterValue, "Expected mode %s to be incremented", mode)
				} else {
					assert.Equal(t, float64(0), counterValue, "Expected mode %s to remain at 0", mode)
				}
			}
		})
	}
}

func TestUpdateCNIChainingMode(t *testing.T) {
	type testCase struct {
		name         string
		chainingMode string
		expectedMode string
	}
	var tests []testCase
	for _, mode := range defaultChainingModes {
		tests = append(tests, testCase{
			name:         fmt.Sprintf("CNI mode %s", mode),
			chainingMode: mode,
			expectedMode: mode,
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				DatapathMode:           defaults.DatapathMode,
				IPAM:                   defaultIPAMModes[0],
				EnableIPv4:             true,
				IdentityAllocationMode: defaultIdentityAllocationModes[0],
			}

			params := mockFeaturesParams{
				CNIChainingMode: tt.chainingMode,
			}

			metrics.update(params, config)

			// Check that only the expected mode's counter is incremented
			for _, mode := range defaultChainingModes {
				counter, err := metrics.DPChaining.GetMetricWithLabelValues(mode)
				assert.NoError(t, err)

				counterValue := counter.Get()
				if mode == tt.expectedMode {
					assert.Equal(t, float64(1), counterValue, "Expected mode %s to be incremented", mode)
				} else {
					assert.Equal(t, float64(0), counterValue, "Expected mode %s to remain at 0", mode)
				}
			}
		})
	}
}

func TestUpdateInternetProtocol(t *testing.T) {
	tests := []struct {
		name             string
		enableIPv4       bool
		enableIPv6       bool
		expectedProtocol string
	}{
		{
			name:             "IPv4-only",
			enableIPv4:       true,
			expectedProtocol: networkIPv4,
		},
		{
			name:             "IPv6-only",
			enableIPv6:       true,
			expectedProtocol: networkIPv6,
		},
		{
			name:             "IPv4-IPv6-dual-stack",
			enableIPv4:       true,
			enableIPv6:       true,
			expectedProtocol: networkDualStack,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				DatapathMode:           defaults.DatapathMode,
				IPAM:                   defaultIPAMModes[0],
				EnableIPv4:             tt.enableIPv4,
				EnableIPv6:             tt.enableIPv6,
				IdentityAllocationMode: defaultIdentityAllocationModes[0],
			}

			params := mockFeaturesParams{
				CNIChainingMode: defaultChainingModes[0],
			}

			metrics.update(params, config)

			// Check that only the expected mode's counter is incremented
			for _, mode := range defaultChainingModes {
				counter, err := metrics.DPIP.GetMetricWithLabelValues(mode)
				assert.NoError(t, err)

				counterValue := counter.Get()
				if mode == tt.expectedProtocol {
					assert.Equal(t, float64(1), counterValue, "Expected mode %s to be incremented", mode)
				} else {
					assert.Equal(t, float64(0), counterValue, "Expected mode %s to remain at 0", mode)
				}
			}
		})
	}
}

func TestUpdateIdentityAllocationMode(t *testing.T) {
	type testCase struct {
		name                   string
		identityAllocationMode string
		expectedMode           string
	}
	var tests []testCase
	for _, mode := range defaultIdentityAllocationModes {
		tests = append(tests, testCase{
			name:                   fmt.Sprintf("Identity Allocation mode %s", mode),
			identityAllocationMode: mode,
			expectedMode:           mode,
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				DatapathMode:           defaults.DatapathMode,
				IPAM:                   defaultIPAMModes[0],
				EnableIPv4:             true,
				IdentityAllocationMode: tt.identityAllocationMode,
			}

			params := mockFeaturesParams{
				CNIChainingMode: defaultChainingModes[0],
			}

			metrics.update(params, config)

			// Check that only the expected mode's counter is incremented
			for _, mode := range defaultIdentityAllocationModes {
				counter, err := metrics.DPIdentityAllocation.GetMetricWithLabelValues(mode)
				assert.NoError(t, err)

				counterValue := counter.Get()
				if mode == tt.expectedMode {
					assert.Equal(t, float64(1), counterValue, "Expected mode %s to be incremented", mode)
				} else {
					assert.Equal(t, float64(0), counterValue, "Expected mode %s to remain at 0", mode)
				}
			}
		})
	}
}

func TestUpdateCiliumEndpointSlices(t *testing.T) {
	tests := []struct {
		name      string
		enableCES bool
		expected  float64
	}{
		{
			name:      "Enable CES",
			enableCES: true,
			expected:  1,
		},
		{
			name:      "Disable CES",
			enableCES: false,
			expected:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				DatapathMode:              defaults.DatapathMode,
				IPAM:                      defaultIPAMModes[0],
				EnableIPv4:                true,
				IdentityAllocationMode:    defaultIdentityAllocationModes[0],
				EnableCiliumEndpointSlice: tt.enableCES,
			}

			params := mockFeaturesParams{
				CNIChainingMode: defaultChainingModes[0],
			}

			metrics.update(params, config)

			counterValue := metrics.DPCiliumEndpointSlicesEnabled.Get()

			assert.Equal(t, tt.expected, counterValue, "Expected value to be %.f for enabled: %t, got %.f", tt.expected, tt.enableCES, counterValue)
		})
	}
}

func TestUpdateDeviceMode(t *testing.T) {
	type testCase struct {
		name         string
		deviceMode   string
		expectedMode string
	}
	var tests []testCase
	for _, mode := range defaultDeviceModes {
		tests = append(tests, testCase{
			name:         fmt.Sprintf("Device %s mode", mode),
			deviceMode:   mode,
			expectedMode: mode,
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				IPAM:                   defaultIPAMModes[0],
				EnableIPv4:             true,
				IdentityAllocationMode: defaultIdentityAllocationModes[0],
				DatapathMode:           tt.deviceMode,
			}

			params := mockFeaturesParams{
				CNIChainingMode: defaultChainingModes[0],
			}

			metrics.update(params, config)

			// Check that only the expected mode's counter is incremented
			for _, mode := range defaultDeviceModes {
				counter, err := metrics.DPDeviceMode.GetMetricWithLabelValues(mode)
				assert.NoError(t, err)

				counterValue := counter.Get()
				if mode == tt.expectedMode {
					assert.Equal(t, float64(1), counterValue, "Expected mode %s to be incremented", mode)
				} else {
					assert.Equal(t, float64(0), counterValue, "Expected mode %s to remain at 0", mode)
				}
			}
		})
	}
}

func TestUpdateHostFirewall(t *testing.T) {
	tests := []struct {
		name               string
		enableHostFirewall bool
		expected           float64
	}{
		{
			name:               "Host firewall enabled",
			enableHostFirewall: true,
			expected:           1,
		},
		{
			name:               "Host firewall disabled",
			enableHostFirewall: false,
			expected:           0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				IPAM:                   defaultIPAMModes[0],
				EnableIPv4:             true,
				IdentityAllocationMode: defaultIdentityAllocationModes[0],
				DatapathMode:           defaultDeviceModes[0],
				EnableHostFirewall:     tt.enableHostFirewall,
			}

			params := mockFeaturesParams{
				CNIChainingMode: defaultChainingModes[0],
			}

			metrics.update(params, config)

			counterValue := metrics.NPHostFirewallEnabled.Get()
			assert.Equal(t, tt.expected, counterValue, "Expected value to be %.f for enabled: %t, got %.f", tt.expected, tt.enableHostFirewall, counterValue)
		})
	}
}

func TestUpdateLocalRedirectPolicies(t *testing.T) {
	tests := []struct {
		name      string
		enableLRP bool
		expected  float64
	}{
		{
			name:      "LRP enabled",
			enableLRP: true,
			expected:  1,
		},
		{
			name:      "LRP disabled",
			enableLRP: false,
			expected:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				IPAM:                      defaultIPAMModes[0],
				EnableIPv4:                true,
				IdentityAllocationMode:    defaultIdentityAllocationModes[0],
				DatapathMode:              defaultDeviceModes[0],
				EnableLocalRedirectPolicy: tt.enableLRP,
			}

			params := mockFeaturesParams{
				CNIChainingMode: defaultChainingModes[0],
			}

			metrics.update(params, config)

			counterValue := metrics.NPLocalRedirectPolicyEnabled.Get()
			assert.Equal(t, tt.expected, counterValue, "Expected value to be %.f for enabled: %t, got %.f", tt.expected, tt.enableLRP, counterValue)
		})
	}
}

func TestUpdateMutualAuth(t *testing.T) {
	tests := []struct {
		name             string
		enableMutualAuth bool
		expected         float64
	}{
		{
			name:             "MutualAuth enabled",
			enableMutualAuth: true,
			expected:         1,
		},
		{
			name:             "MutualAuth disabled",
			enableMutualAuth: false,
			expected:         0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				IPAM:                   defaultIPAMModes[0],
				EnableIPv4:             true,
				IdentityAllocationMode: defaultIdentityAllocationModes[0],
				DatapathMode:           defaultDeviceModes[0],
			}

			params := mockFeaturesParams{
				CNIChainingMode: defaultChainingModes[0],
				MutualAuth:      tt.enableMutualAuth,
			}

			metrics.update(params, config)

			counterValue := metrics.NPMutualAuthEnabled.Get()
			assert.Equal(t, tt.expected, counterValue, "Expected value to be %.f for enabled: %t, got %.f", tt.expected, tt.enableMutualAuth, counterValue)
		})
	}
}

func TestUpdateEncryptionMode(t *testing.T) {
	tests := []struct {
		name                      string
		enableIPSec               bool
		enableWireguard           bool
		enableNode2NodeEncryption bool

		expectEncryptionMode      string
		expectNode2NodeEncryption string
	}{
		{
			name:                      "IPSec enabled",
			enableIPSec:               true,
			expectEncryptionMode:      advConnNetEncIPSec,
			expectNode2NodeEncryption: "false",
		},
		{
			name:                      "IPSec disabled",
			enableIPSec:               false,
			expectEncryptionMode:      "",
			expectNode2NodeEncryption: "",
		},
		{
			name:                      "IPSec enabled w/ node2node",
			enableIPSec:               true,
			enableNode2NodeEncryption: true,
			expectEncryptionMode:      advConnNetEncIPSec,
			expectNode2NodeEncryption: "true",
		},
		{
			name:                      "Wireguard enabled",
			enableWireguard:           true,
			expectEncryptionMode:      advConnNetEncWireGuard,
			expectNode2NodeEncryption: "false",
		},
		{
			name:                      "Wireguard enabled w/ node2node",
			enableWireguard:           true,
			enableNode2NodeEncryption: true,
			expectEncryptionMode:      advConnNetEncWireGuard,
			expectNode2NodeEncryption: "true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := NewMetrics(true)
			config := &option.DaemonConfig{
				IPAM:                   defaultIPAMModes[0],
				EnableIPv4:             true,
				IdentityAllocationMode: defaultIdentityAllocationModes[0],
				DatapathMode:           defaultDeviceModes[0],
				EnableIPSec:            tt.enableIPSec,
				EnableWireguard:        tt.enableWireguard,
				EncryptNode:            tt.enableNode2NodeEncryption,
			}

			params := mockFeaturesParams{
				CNIChainingMode: defaultChainingModes[0],
			}

			metrics.update(params, config)

			// Check that only the expected mode's counter is incremented
			for _, encMode := range defaultEncryptionModes {
				for _, node2node := range []string{"true", "false"} {
					counter, err := metrics.ACLBTransparentEncryption.GetMetricWithLabelValues(encMode, node2node)
					assert.NoError(t, err)

					counterValue := counter.Get()
					if encMode == tt.expectEncryptionMode && node2node == tt.expectNode2NodeEncryption {
						assert.Equal(t, float64(1), counterValue, "Expected mode %s to be incremented", encMode)
					} else {
						assert.Equal(t, float64(0), counterValue, "Expected mode %s to remain at 0", encMode)
					}
				}
			}
		})
	}
}
