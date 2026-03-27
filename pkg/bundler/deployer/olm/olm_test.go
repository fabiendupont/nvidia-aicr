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

package olm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/aicr/pkg/recipe"
)

func TestGenerator_Generate_SingleComponent(t *testing.T) {
	dir := t.TempDir()
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "gpu-operator"},
			},
			DeploymentOrder: []string{"gpu-operator"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"gpu-operator": {
				Name:             "gpu-operator",
				Package:          "gpu-operator-certified",
				Channel:          "v25.10",
				Source:           "certified-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "nvidia-gpu-operator",
				ApprovalPolicy:   "Manual",
				HasOperatorGroup: true,
			},
		},
		Version:          "test",
		IncludeChecksums: false,
	}

	output, err := generator.Generate(context.Background(), input, dir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if len(output.Files) == 0 {
		t.Fatal("Generate() produced no files")
	}

	// Verify expected files exist
	expectedFiles := []string{
		"gpu-operator/namespace.yaml",
		"gpu-operator/operatorgroup.yaml",
		"gpu-operator/subscription.yaml",
		"deploy.sh",
		"undeploy.sh",
		"README.md",
	}
	for _, expected := range expectedFiles {
		path := filepath.Join(dir, expected)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s does not exist", expected)
		}
	}

	// Verify subscription.yaml content
	subContent, err := os.ReadFile(filepath.Join(dir, "gpu-operator/subscription.yaml"))
	if err != nil {
		t.Fatalf("failed to read subscription.yaml: %v", err)
	}
	subStr := string(subContent)

	if !strings.Contains(subStr, "gpu-operator-certified") {
		t.Error("subscription.yaml missing package name")
	}
	if !strings.Contains(subStr, "channel: v25.10") {
		t.Error("subscription.yaml missing channel")
	}
	if !strings.Contains(subStr, "certified-operators") {
		t.Error("subscription.yaml missing source")
	}
	if !strings.Contains(subStr, "installPlanApproval: Manual") {
		t.Error("subscription.yaml missing approval policy")
	}
}

func TestGenerator_Generate_MultipleComponents(t *testing.T) {
	dir := t.TempDir()
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "cert-manager"},
				{Name: "gpu-operator"},
			},
			DeploymentOrder: []string{"cert-manager", "gpu-operator"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"cert-manager": {
				Name:             "cert-manager",
				Package:          "cert-manager",
				Channel:          "stable",
				Source:           "community-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "cert-manager",
				ApprovalPolicy:   "Automatic",
				HasOperatorGroup: true,
			},
			"gpu-operator": {
				Name:             "gpu-operator",
				Package:          "gpu-operator-certified",
				Channel:          "v25.10",
				Source:           "certified-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "nvidia-gpu-operator",
				ApprovalPolicy:   "Manual",
				HasOperatorGroup: true,
			},
		},
		Version: "test",
	}

	output, err := generator.Generate(context.Background(), input, dir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Should have files for both components
	certDir := filepath.Join(dir, "cert-manager")
	gpuDir := filepath.Join(dir, "gpu-operator")

	if _, err := os.Stat(certDir); os.IsNotExist(err) {
		t.Error("cert-manager directory not created")
	}
	if _, err := os.Stat(gpuDir); os.IsNotExist(err) {
		t.Error("gpu-operator directory not created")
	}

	if output.TotalSize == 0 {
		t.Error("TotalSize should be > 0")
	}
}

func TestGenerator_Generate_WithCustomResources(t *testing.T) {
	dir := t.TempDir()
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "gpu-operator"},
			},
			DeploymentOrder: []string{"gpu-operator"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"gpu-operator": {
				Name:             "gpu-operator",
				Package:          "gpu-operator-certified",
				Channel:          "v25.10",
				Source:           "certified-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "nvidia-gpu-operator",
				ApprovalPolicy:   "Manual",
				HasOperatorGroup: true,
				HasCustomResources: true,
				CustomResources: []CustomResourceData{
					{
						APIVersion: "nvidia.com/v1",
						Kind:       "ClusterPolicy",
						Name:       "gpu-cluster-policy",
						Filename:   "gpu-cluster-policy.yaml",
					},
				},
			},
		},
		Version: "test",
	}

	_, err := generator.Generate(context.Background(), input, dir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Verify custom resource file exists
	crPath := filepath.Join(dir, "gpu-operator/custom-resources/gpu-cluster-policy.yaml")
	if _, err := os.Stat(crPath); os.IsNotExist(err) {
		t.Error("custom resource file not created")
	}

	// Verify CR content
	crContent, err := os.ReadFile(crPath)
	if err != nil {
		t.Fatalf("failed to read CR file: %v", err)
	}
	crStr := string(crContent)

	if !strings.Contains(crStr, "nvidia.com/v1") {
		t.Error("CR missing apiVersion")
	}
	if !strings.Contains(crStr, "ClusterPolicy") {
		t.Error("CR missing kind")
	}
	if !strings.Contains(crStr, "gpu-cluster-policy") {
		t.Error("CR missing name")
	}

	// Verify deploy.sh references custom resources
	deployContent, err := os.ReadFile(filepath.Join(dir, "deploy.sh"))
	if err != nil {
		t.Fatalf("failed to read deploy.sh: %v", err)
	}
	if !strings.Contains(string(deployContent), "custom-resources") {
		t.Error("deploy.sh should reference custom-resources directory")
	}
}

func TestGenerator_Generate_NilInput(t *testing.T) {
	generator := NewGenerator()

	_, err := generator.Generate(context.Background(), nil, t.TempDir())
	if err == nil {
		t.Error("Generate(nil) should return error")
	}
}

func TestGenerator_Generate_NilRecipeResult(t *testing.T) {
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: nil,
	}
	_, err := generator.Generate(context.Background(), input, t.TempDir())
	if err == nil {
		t.Error("Generate(nil recipe) should return error")
	}
}

func TestGenerator_Generate_EmptyOLMData(t *testing.T) {
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "gpu-operator"},
			},
			DeploymentOrder: []string{"gpu-operator"},
		},
		ComponentOLMData: map[string]*OLMComponentData{},
		Version:          "test",
	}

	_, err := generator.Generate(context.Background(), input, t.TempDir())
	if err == nil {
		t.Error("Generate with empty OLM data should return error")
	}
}

func TestGenerator_Generate_MissingOLMData(t *testing.T) {
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "gpu-operator"},
				{Name: "unknown-component"},
			},
			DeploymentOrder: []string{"gpu-operator", "unknown-component"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"gpu-operator": {
				Name:             "gpu-operator",
				Package:          "gpu-operator-certified",
				Channel:          "v25.10",
				Source:           "certified-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "nvidia-gpu-operator",
				ApprovalPolicy:   "Manual",
			},
		},
		Version: "test",
	}

	_, err := generator.Generate(context.Background(), input, t.TempDir())
	if err == nil {
		t.Error("Generate with missing OLM data for a component should return error")
	}
	if !strings.Contains(err.Error(), "unknown-component") {
		t.Errorf("error should mention the missing component, got: %v", err)
	}
}

func TestGenerator_Generate_PathSafety(t *testing.T) {
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "../escape"},
			},
			DeploymentOrder: []string{"../escape"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"../escape": {
				Name:             "../escape",
				Package:          "bad",
				Channel:          "stable",
				Source:           "test",
				SourceNamespace:  "test",
				InstallNamespace: "test",
				ApprovalPolicy:   "Automatic",
			},
		},
		Version: "test",
	}

	_, err := generator.Generate(context.Background(), input, t.TempDir())
	if err == nil {
		t.Error("Generate with path traversal component name should return error")
	}
}

func TestGenerator_Generate_Checksums(t *testing.T) {
	dir := t.TempDir()
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "gpu-operator"},
			},
			DeploymentOrder: []string{"gpu-operator"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"gpu-operator": {
				Name:             "gpu-operator",
				Package:          "gpu-operator-certified",
				Channel:          "v25.10",
				Source:           "certified-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "nvidia-gpu-operator",
				ApprovalPolicy:   "Manual",
				HasOperatorGroup: true,
			},
		},
		Version:          "test",
		IncludeChecksums: true,
	}

	output, err := generator.Generate(context.Background(), input, dir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Verify checksums.txt exists
	checksumPath := filepath.Join(dir, "checksums.txt")
	if _, err := os.Stat(checksumPath); os.IsNotExist(err) {
		t.Error("checksums.txt not created")
	}

	// Verify checksums.txt is in the file list
	found := false
	for _, f := range output.Files {
		if strings.HasSuffix(f, "checksums.txt") {
			found = true
			break
		}
	}
	if !found {
		t.Error("checksums.txt not in output file list")
	}
}

func TestGenerator_Generate_ContextCancellation(t *testing.T) {
	generator := NewGenerator()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "gpu-operator"},
			},
			DeploymentOrder: []string{"gpu-operator"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"gpu-operator": {
				Name:             "gpu-operator",
				Package:          "gpu-operator-certified",
				Channel:          "v25.10",
				Source:           "certified-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "nvidia-gpu-operator",
				ApprovalPolicy:   "Manual",
				HasOperatorGroup: true,
			},
		},
		Version: "test",
	}

	_, err := generator.Generate(ctx, input, t.TempDir())
	if err == nil {
		t.Error("Generate with cancelled context should return error")
	}
}

func TestGenerator_Generate_DeployScriptExecutable(t *testing.T) {
	dir := t.TempDir()
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "gpu-operator"},
			},
			DeploymentOrder: []string{"gpu-operator"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"gpu-operator": {
				Name:             "gpu-operator",
				Package:          "gpu-operator-certified",
				Channel:          "v25.10",
				Source:           "certified-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "nvidia-gpu-operator",
				ApprovalPolicy:   "Manual",
				HasOperatorGroup: true,
			},
		},
		Version: "test",
	}

	_, err := generator.Generate(context.Background(), input, dir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Verify deploy.sh is executable
	info, err := os.Stat(filepath.Join(dir, "deploy.sh"))
	if err != nil {
		t.Fatalf("stat deploy.sh: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Error("deploy.sh should be executable")
	}

	// Verify undeploy.sh is executable
	info, err = os.Stat(filepath.Join(dir, "undeploy.sh"))
	if err != nil {
		t.Fatalf("stat undeploy.sh: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Error("undeploy.sh should be executable")
	}
}

func TestGenerator_Generate_StartingCSV(t *testing.T) {
	dir := t.TempDir()
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "gpu-operator"},
			},
			DeploymentOrder: []string{"gpu-operator"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"gpu-operator": {
				Name:             "gpu-operator",
				Package:          "gpu-operator-certified",
				Channel:          "v25.10",
				Source:           "certified-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "nvidia-gpu-operator",
				ApprovalPolicy:   "Manual",
				StartingCSV:      "gpu-operator-certified.v25.10.1",
				HasOperatorGroup: true,
			},
		},
		Version: "test",
	}

	_, err := generator.Generate(context.Background(), input, dir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Verify startingCSV is in subscription.yaml
	subContent, err := os.ReadFile(filepath.Join(dir, "gpu-operator/subscription.yaml"))
	if err != nil {
		t.Fatalf("failed to read subscription.yaml: %v", err)
	}
	if !strings.Contains(string(subContent), "startingCSV: gpu-operator-certified.v25.10.1") {
		t.Error("subscription.yaml should contain startingCSV when specified")
	}
}

func TestGenerator_Generate_NoOperatorGroup(t *testing.T) {
	dir := t.TempDir()
	generator := NewGenerator()

	input := &GeneratorInput{
		RecipeResult: &recipe.RecipeResult{
			Kind:       "RecipeResult",
			APIVersion: "aicr.nvidia.com/v1alpha1",
			ComponentRefs: []recipe.ComponentRef{
				{Name: "gpu-operator"},
			},
			DeploymentOrder: []string{"gpu-operator"},
		},
		ComponentOLMData: map[string]*OLMComponentData{
			"gpu-operator": {
				Name:             "gpu-operator",
				Package:          "gpu-operator-certified",
				Channel:          "v25.10",
				Source:           "certified-operators",
				SourceNamespace:  "openshift-marketplace",
				InstallNamespace: "nvidia-gpu-operator",
				ApprovalPolicy:   "Manual",
				HasOperatorGroup: false,
			},
		},
		Version: "test",
	}

	_, err := generator.Generate(context.Background(), input, dir)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Verify operatorgroup.yaml is NOT created
	ogPath := filepath.Join(dir, "gpu-operator/operatorgroup.yaml")
	if _, err := os.Stat(ogPath); !os.IsNotExist(err) {
		t.Error("operatorgroup.yaml should not be created when HasOperatorGroup is false")
	}
}
