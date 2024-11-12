// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package features

import (
	"context"
	"runtime/pprof"

	"github.com/cilium/cilium/pkg/hive/job"
)

func newOperatorConfigMetricOnStart(params featuresParams, m featureMetrics) {
	jobGroup := params.JobRegistry.NewGroup(
		job.WithPprofLabels(pprof.Labels("cell", "features")),
	)

	jobGroup.Add(
		job.OneShot("update-config-metric", func(ctx context.Context) error {
			m.update(&params, params.OperatorConfig)
			return nil
		}),
	)

	params.Lifecycle.Append(jobGroup)
}
