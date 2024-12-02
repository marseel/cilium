// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package features

import (
	"os"

	operatorOption "github.com/cilium/cilium/operator/option"
	"github.com/cilium/cilium/operator/pkg/lbipam"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
)

var (
	// withDefaults will set enable all default metrics in the operator.
	withDefaults = os.Getenv("CILIUM_FEATURE_METRICS_WITH_DEFAULTS")
)

// Cell will retrieve information from all other cells /
// configuration to describe, in form of prometheus metrics, which
// features are enabled on the operator.
var Cell = cell.Module(
	"enabled-features",
	"Exports prometheus metrics describing which features are enabled in operator",

	cell.Invoke(newOperatorConfigMetricOnStart),
	cell.Provide(
		func(m Metrics) featureMetrics {
			return m
		},
	),
	cell.Metric(func() Metrics {
		if withDefaults != "" {
			return NewMetrics(true)
		}
		return NewMetrics(false)
	}),
)

type featuresParams struct {
	cell.In

	JobRegistry job.Registry
	Health      cell.Health
	Lifecycle   cell.Lifecycle
	Metrics     featureMetrics

	OperatorConfig *operatorOption.OperatorConfig
	LBIPAM         lbipam.Config
}

func (p featuresParams) IsIngressControllerEnabled() bool {
	return operatorOption.Config.EnableIngressController
}

func (p featuresParams) IsLBIPAMEnabled() bool {
	return p.LBIPAM.IsEnabled()
}

func (p featuresParams) GetLoadBalancerL7() string {
	return operatorOption.Config.LoadBalancerL7
}

type enabledFeatures interface {
	IsIngressControllerEnabled() bool
	IsLBIPAMEnabled() bool
	GetLoadBalancerL7() string
}
