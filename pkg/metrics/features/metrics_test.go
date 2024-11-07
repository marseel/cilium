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
	TunnelConfig string
}

func (m mockFeaturesParams) TunnelProtocol() string {
	return m.TunnelConfig
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
				DatapathMode: defaults.DatapathMode,
				IPAM:         defaultIPAMModes[0],
				RoutingMode:  tt.tunnelMode,
			}

			params := mockFeaturesParams{
				TunnelConfig: tt.tunnelProto,
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
				DatapathMode: defaults.DatapathMode,
				IPAM:         tt.IPAMMode,
			}

			params := mockFeaturesParams{}

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
