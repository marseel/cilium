// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package features

import (
	"github.com/cilium/cilium/daemon/cmd/cni"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/promise"
)

// Cell will retrieve information from all other cells /
// configuration to describe, in form of prometheus metrics, which
// features are enabled on the agent.
var Cell = cell.Module(
	"enabled-features",
	"Exports prometheus metrics describing which features are enabled in cilium-agent",

	cell.Invoke(newAgentConfigMetricOnStart),
	cell.Provide(
		func(m Metrics) featureMetrics {
			return m
		},
	),
	cell.Metric(func() Metrics {
		return NewMetrics(true)
	}),
)

type featuresParams struct {
	cell.In

	JobRegistry      job.Registry
	Health           cell.Health
	Lifecycle        cell.Lifecycle
	ConfigPromise    promise.Promise[*option.DaemonConfig]
	Metrics          featureMetrics
	CNIConfigManager cni.CNIConfigManager
}

func (fp *featuresParams) TunnelProtocol() string {
	return option.Config.TunnelProtocol
}

func (fp *featuresParams) GetChainingMode() string {
	return fp.CNIConfigManager.GetChainingMode()
}

type enabledFeatures interface {
	TunnelProtocol() string
	GetChainingMode() string
}
