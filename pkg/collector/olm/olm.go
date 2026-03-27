// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package olm collects information about OLM-installed operators on OpenShift.
package olm

import (
	"context"
	"log/slog"
	"strings"

	"github.com/NVIDIA/aicr/pkg/defaults"
	"github.com/NVIDIA/aicr/pkg/errors"
	"github.com/NVIDIA/aicr/pkg/k8s/client"
	"github.com/NVIDIA/aicr/pkg/measurement"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Collector collects information about OLM-installed operators.
// On non-OLM clusters (e.g., EKS), it returns an empty measurement gracefully.
type Collector struct {
	ClientSet  kubernetes.Interface
	RestConfig *rest.Config
}

// Collect retrieves OLM operator information from the cluster.
// Returns an empty measurement if OLM is not installed (non-OCP clusters).
func (o *Collector) Collect(ctx context.Context) (*measurement.Measurement, error) {
	slog.Info("collecting OLM operator information")

	ctx, cancel := context.WithTimeout(ctx, defaults.CollectorK8sTimeout)
	defer cancel()

	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(errors.ErrCodeTimeout, "OLM collector context cancelled", err)
	}

	if err := o.getClient(); err != nil {
		slog.Warn("kubernetes client unavailable - returning empty OLM measurement",
			slog.String("error", err.Error()))
		return emptyMeasurement(), nil
	}

	// Check if OLM is installed before attempting CSV collection
	installed := o.detectOLM(ctx)

	installedData := map[string]measurement.Reading{
		"olm": measurement.Str(boolStr(installed)),
	}

	// If OLM is not installed, return early with empty operators
	if !installed {
		slog.Debug("OLM not detected, skipping operator collection")
		return measurement.NewMeasurement(measurement.TypeOLM).
			WithSubtype(measurement.Subtype{Name: "installed", Data: installedData}).
			WithSubtype(measurement.Subtype{Name: "operators", Data: make(map[string]measurement.Reading)}).
			Build(), nil
	}

	// Collect operator versions from CSVs
	operators := collectSafe("operators", func(ctx context.Context) (map[string]measurement.Reading, error) {
		return o.collectOperators(ctx)
	}, ctx)

	res := measurement.NewMeasurement(measurement.TypeOLM).
		WithSubtype(measurement.Subtype{Name: "installed", Data: installedData}).
		WithSubtype(measurement.Subtype{Name: "operators", Data: operators}).
		Build()

	return res, nil
}

// getClient initializes the Kubernetes client if not already set.
func (o *Collector) getClient() error {
	if o.ClientSet != nil && o.RestConfig != nil {
		return nil
	}
	var err error
	o.ClientSet, o.RestConfig, err = client.GetKubeClient()
	if err != nil {
		return errors.Wrap(errors.ErrCodeInternal, "failed to get kubernetes client", err)
	}
	return nil
}

// detectOLM checks if OLM is installed by looking for ClusterServiceVersion resources.
func (o *Collector) detectOLM(ctx context.Context) bool {
	apiResourceLists, err := o.ClientSet.Discovery().ServerPreferredResources()
	if err != nil {
		slog.Debug("error discovering API resources for OLM detection",
			slog.String("error", err.Error()))
		return false
	}

	for _, apiResourceList := range apiResourceLists {
		if apiResourceList == nil {
			continue
		}
		for _, resource := range apiResourceList.APIResources {
			if resource.Kind == "ClusterServiceVersion" {
				return true
			}
		}
	}
	return false
}

// collectOperators retrieves operator names and versions from ClusterServiceVersions.
// Uses dynamic client to discover CSVs without requiring OLM API types as a dependency.
func (o *Collector) collectOperators(ctx context.Context) (map[string]measurement.Reading, error) {
	dynamicClient, err := dynamic.NewForConfig(o.RestConfig)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, "failed to create dynamic client", err)
	}

	// Discover CSV resource
	apiResourceLists, err := o.ClientSet.Discovery().ServerPreferredResources()
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, "failed to discover API resources", err)
	}

	operators := make(map[string]measurement.Reading)

	for _, apiResourceList := range apiResourceLists {
		if apiResourceList == nil {
			continue
		}

		gv, parseErr := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if parseErr != nil {
			continue
		}

		for _, resource := range apiResourceList.APIResources {
			if resource.Kind != "ClusterServiceVersion" {
				continue
			}

			gvr := schema.GroupVersionResource{
				Group:    gv.Group,
				Version:  gv.Version,
				Resource: resource.Name,
			}

			csvs, listErr := dynamicClient.Resource(gvr).Namespace("").List(ctx, v1.ListOptions{})
			if listErr != nil {
				slog.Debug("failed to list CSVs",
					slog.String("error", listErr.Error()))
				continue
			}

			for i := range csvs.Items {
				o.extractOperatorInfo(&csvs.Items[i], operators)
			}
		}
	}

	return operators, nil
}

// extractOperatorInfo extracts the operator package name and version from a CSV.
// The package name is derived from the operators.coreos.com/ label prefix.
func (o *Collector) extractOperatorInfo(csv *unstructured.Unstructured, operators map[string]measurement.Reading) {
	// Get phase — only include succeeded CSVs
	phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
	if phase != "Succeeded" {
		return
	}

	// Extract version from spec.version
	version, found, _ := unstructured.NestedString(csv.Object, "spec", "version")
	if !found || version == "" {
		return
	}

	// Extract package name from operators.coreos.com/<package>.<namespace> label
	labels := csv.GetLabels()
	for label := range labels {
		if !strings.HasPrefix(label, "operators.coreos.com/") {
			continue
		}

		// Label format: operators.coreos.com/<package>.<namespace>
		remainder := strings.TrimPrefix(label, "operators.coreos.com/")
		parts := strings.SplitN(remainder, ".", 2)
		if len(parts) >= 1 && parts[0] != "" {
			packageName := parts[0]
			operators[packageName] = measurement.Str(version)

			slog.Debug("found OLM operator",
				slog.String("package", packageName),
				slog.String("version", version),
			)
		}
	}
}

// collectSafe runs a named sub-collector, returning empty data on failure.
func collectSafe(name string, fn func(ctx context.Context) (map[string]measurement.Reading, error), ctx context.Context) map[string]measurement.Reading {
	data, err := fn(ctx)
	if err != nil {
		slog.Warn("failed to collect OLM "+name+" - skipping",
			slog.String("collector", name),
			slog.String("error", err.Error()))
		return make(map[string]measurement.Reading)
	}
	return data
}

// emptyMeasurement returns an OLM measurement with no data.
func emptyMeasurement() *measurement.Measurement {
	empty := make(map[string]measurement.Reading)
	return measurement.NewMeasurement(measurement.TypeOLM).
		WithSubtype(measurement.Subtype{Name: "installed", Data: map[string]measurement.Reading{
			"olm": measurement.Str("false"),
		}}).
		WithSubtype(measurement.Subtype{Name: "operators", Data: empty}).
		Build()
}

// boolStr converts a bool to "true" or "false" string.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
