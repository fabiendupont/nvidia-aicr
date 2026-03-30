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
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/NVIDIA/aicr/pkg/bundler/checksum"
	"github.com/NVIDIA/aicr/pkg/bundler/deployer/shared"
	"github.com/NVIDIA/aicr/pkg/errors"
	"github.com/NVIDIA/aicr/pkg/recipe"
)

//go:embed templates/subscription.yaml.tmpl
var subscriptionTemplate string

//go:embed templates/operatorgroup.yaml.tmpl
var operatorGroupTemplate string

//go:embed templates/namespace.yaml.tmpl
var namespaceTemplate string

//go:embed templates/custom-resource.yaml.tmpl
var customResourceTemplate string

//go:embed templates/deploy.sh.tmpl
var deployScriptTemplate string

//go:embed templates/undeploy.sh.tmpl
var undeployScriptTemplate string

//go:embed templates/README.md.tmpl
var readmeTemplate string

// OLMComponentData holds per-component OLM metadata for template rendering.
type OLMComponentData struct {
	// Name is the component name (e.g., "gpu-operator").
	Name string

	// Package is the OLM package name (e.g., "gpu-operator-certified").
	Package string

	// Channel is the OLM subscription channel (e.g., "v25.10").
	Channel string

	// Source is the CatalogSource name (e.g., "certified-operators").
	Source string

	// SourceNamespace is the CatalogSource namespace (e.g., "openshift-marketplace").
	SourceNamespace string

	// InstallNamespace is the namespace to install the operator into.
	InstallNamespace string

	// TargetNamespaces lists namespaces the operator watches (empty = all).
	TargetNamespaces []string

	// ApprovalPolicy is "Manual" or "Automatic".
	ApprovalPolicy string

	// StartingCSV pins to a specific CSV version (optional).
	StartingCSV string

	// CustomResources are post-install CRs to create.
	CustomResources []CustomResourceData

	// HasCustomResources is true when CustomResources is non-empty (for templates).
	HasCustomResources bool

	// HasOperatorGroup is true when an OperatorGroup should be generated.
	HasOperatorGroup bool
}

// CustomResourceData holds a post-install CR for template rendering.
type CustomResourceData struct {
	// APIVersion is the CR API version (e.g., "nvidia.com/v1").
	APIVersion string

	// Kind is the CR kind (e.g., "ClusterPolicy").
	Kind string

	// Name is the CR metadata.name.
	Name string

	// Namespace is the CR namespace (empty for cluster-scoped).
	Namespace string

	// Filename is the generated filename for this CR.
	Filename string
}

// GeneratorInput contains all data needed to generate an OLM bundle.
type GeneratorInput struct {
	// RecipeResult contains the recipe metadata and component references.
	RecipeResult *recipe.RecipeResult

	// ComponentOLMData maps component names to their OLM metadata.
	ComponentOLMData map[string]*OLMComponentData

	// Version is the bundler version.
	Version string

	// IncludeChecksums indicates whether to generate a checksums.txt file.
	IncludeChecksums bool
}

// GeneratorOutput contains the result of OLM bundle generation.
type GeneratorOutput struct {
	// Files contains the paths of generated files.
	Files []string

	// TotalSize is the total size of all generated files.
	TotalSize int64

	// Duration is the time taken to generate the bundle.
	Duration time.Duration

	// DeploymentSteps contains ordered deployment instructions.
	DeploymentSteps []string

	// DeploymentNotes contains additional notes for the user.
	DeploymentNotes []string
}

// Generator creates OLM deployment bundles from recipe results.
type Generator struct{}

// NewGenerator creates a new OLM bundle generator.
func NewGenerator() *Generator {
	return &Generator{}
}

// Generate creates an OLM deployment bundle from the given input.
func (g *Generator) Generate(ctx context.Context, input *GeneratorInput, outputDir string) (*GeneratorOutput, error) {
	start := time.Now()

	output := &GeneratorOutput{
		Files: make([]string, 0),
	}

	if input == nil || input.RecipeResult == nil {
		return nil, errors.New(errors.ErrCodeInvalidRequest, "input and recipe result are required")
	}

	if len(input.ComponentOLMData) == 0 {
		return nil, errors.New(errors.ErrCodeInvalidRequest, "no OLM component data provided")
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			"failed to create output directory", err)
	}

	// Build sorted component list
	components, err := g.buildSortedComponents(input)
	if err != nil {
		return nil, err
	}

	// Generate per-component directories
	for _, comp := range components {
		select {
		case <-ctx.Done():
			return nil, errors.Wrap(errors.ErrCodeInternal, "context cancelled", ctx.Err())
		default:
		}

		files, size, genErr := g.generateComponentFiles(comp, outputDir)
		if genErr != nil {
			return nil, errors.Wrap(errors.ErrCodeInternal,
				fmt.Sprintf("failed to generate files for %s", comp.Name), genErr)
		}
		output.Files = append(output.Files, files...)
		output.TotalSize += size
	}

	// Generate deploy.sh
	deployData := struct {
		Version    string
		RecipeName string
		Components []OLMComponentData
	}{
		Version:    input.Version,
		RecipeName: g.recipeName(input),
		Components: components,
	}
	deployPath, deploySize, err := shared.GenerateFromTemplate(deployScriptTemplate, deployData, outputDir, "deploy.sh")
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, "failed to generate deploy.sh", err)
	}
	if chmodErr := os.Chmod(deployPath, 0755); chmodErr != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, "failed to set deploy.sh permissions", chmodErr)
	}
	output.Files = append(output.Files, deployPath)
	output.TotalSize += deploySize

	// Generate undeploy.sh (reverse order)
	reversed := make([]OLMComponentData, len(components))
	for i, comp := range components {
		reversed[len(components)-1-i] = comp
	}
	undeployData := struct {
		Version    string
		RecipeName string
		Components []OLMComponentData
	}{
		Version:    input.Version,
		RecipeName: g.recipeName(input),
		Components: reversed,
	}
	undeployPath, undeploySize, err := shared.GenerateFromTemplate(undeployScriptTemplate, undeployData, outputDir, "undeploy.sh")
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, "failed to generate undeploy.sh", err)
	}
	if chmodErr := os.Chmod(undeployPath, 0755); chmodErr != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, "failed to set undeploy.sh permissions", chmodErr)
	}
	output.Files = append(output.Files, undeployPath)
	output.TotalSize += undeploySize

	// Generate README.md
	readmeData := struct {
		Version    string
		RecipeName string
		Components []OLMComponentData
	}{
		Version:    input.Version,
		RecipeName: g.recipeName(input),
		Components: components,
	}
	readmePath, readmeSize, err := shared.GenerateFromTemplate(readmeTemplate, readmeData, outputDir, "README.md")
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, "failed to generate README.md", err)
	}
	output.Files = append(output.Files, readmePath)
	output.TotalSize += readmeSize

	// Generate checksums.txt if requested
	if input.IncludeChecksums {
		if checksumErr := checksum.GenerateChecksums(ctx, outputDir, output.Files); checksumErr != nil {
			return nil, errors.Wrap(errors.ErrCodeInternal,
				"failed to generate checksums", checksumErr)
		}
		checksumPath := checksum.GetChecksumFilePath(outputDir)
		info, statErr := os.Stat(checksumPath)
		if statErr == nil {
			output.Files = append(output.Files, checksumPath)
			output.TotalSize += info.Size()
		}
	}

	output.Duration = time.Since(start)

	output.DeploymentSteps = []string{
		fmt.Sprintf("cd %s", outputDir),
		"chmod +x deploy.sh",
		"./deploy.sh",
	}

	output.DeploymentNotes = []string{
		"Requires OpenShift 4.18+ with oc CLI configured",
		"Operators are installed via OLM — manual install plan approval may be required",
	}

	slog.Debug("olm bundle generated",
		"files", len(output.Files),
		"total_size", output.TotalSize,
		"duration", output.Duration,
	)

	return output, nil
}

// buildSortedComponents returns OLM component data sorted by deployment order.
func (g *Generator) buildSortedComponents(input *GeneratorInput) ([]OLMComponentData, error) {
	sorted := shared.SortComponentRefsByDeploymentOrder(
		input.RecipeResult.ComponentRefs,
		input.RecipeResult.DeploymentOrder,
	)

	components := make([]OLMComponentData, 0, len(sorted))
	for _, ref := range sorted {
		if !shared.IsSafePathComponent(ref.Name) {
			return nil, errors.New(errors.ErrCodeInvalidRequest,
				fmt.Sprintf("invalid component name %q: must not contain path separators or parent directory references", ref.Name))
		}

		olmData, ok := input.ComponentOLMData[ref.Name]
		if !ok {
			slog.Debug("skipping non-OLM component in OLM deployer",
				slog.String("component", ref.Name))
			continue
		}

		components = append(components, *olmData)
	}

	return components, nil
}

// ComponentOutput contains the result of generating OLM manifests for a single component.
type ComponentOutput struct {
	// Files contains the paths of generated files.
	Files []string

	// TotalSize is the total size of all generated files.
	TotalSize int64
}

// GenerateComponent creates OLM manifests for a single component in the given directory.
// This is used by the ArgoCD deployer to embed OLM manifests in Git-synced directories.
func (g *Generator) GenerateComponent(comp *OLMComponentData, outputDir string) (*ComponentOutput, error) {
	files, size, err := g.generateComponentFiles(*comp, outputDir)
	if err != nil {
		return nil, err
	}
	return &ComponentOutput{Files: files, TotalSize: size}, nil
}

// generateComponentFiles creates the per-component directory with OLM manifests.
func (g *Generator) generateComponentFiles(comp OLMComponentData, outputDir string) ([]string, int64, error) {
	componentDir, err := shared.SafeJoin(outputDir, comp.Name)
	if err != nil {
		return nil, 0, err
	}
	if mkdirErr := os.MkdirAll(componentDir, 0755); mkdirErr != nil {
		return nil, 0, errors.Wrap(errors.ErrCodeInternal,
			fmt.Sprintf("failed to create directory for %s", comp.Name), mkdirErr)
	}

	var files []string
	var totalSize int64

	// Generate namespace.yaml
	nsPath, nsSize, nsErr := shared.GenerateFromTemplate(namespaceTemplate, comp, componentDir, "namespace.yaml")
	if nsErr != nil {
		return nil, 0, nsErr
	}
	files = append(files, nsPath)
	totalSize += nsSize

	// Generate operatorgroup.yaml (if applicable)
	if comp.HasOperatorGroup {
		ogPath, ogSize, ogErr := shared.GenerateFromTemplate(operatorGroupTemplate, comp, componentDir, "operatorgroup.yaml")
		if ogErr != nil {
			return nil, 0, ogErr
		}
		files = append(files, ogPath)
		totalSize += ogSize
	}

	// Generate subscription.yaml
	subPath, subSize, subErr := shared.GenerateFromTemplate(subscriptionTemplate, comp, componentDir, "subscription.yaml")
	if subErr != nil {
		return nil, 0, subErr
	}
	files = append(files, subPath)
	totalSize += subSize

	// Generate custom resources
	if comp.HasCustomResources {
		crDir, crDirErr := shared.SafeJoin(componentDir, "custom-resources")
		if crDirErr != nil {
			return nil, 0, crDirErr
		}
		if mkdirErr := os.MkdirAll(crDir, 0755); mkdirErr != nil {
			return nil, 0, errors.Wrap(errors.ErrCodeInternal,
				"failed to create custom-resources directory", mkdirErr)
		}

		for _, cr := range comp.CustomResources {
			crPath, crSize, crErr := shared.GenerateFromTemplate(customResourceTemplate, cr, crDir, cr.Filename)
			if crErr != nil {
				return nil, 0, crErr
			}
			files = append(files, crPath)
			totalSize += crSize
		}
	}

	return files, totalSize, nil
}

// recipeName extracts a human-readable recipe name from the input.
func (g *Generator) recipeName(input *GeneratorInput) string {
	if input.RecipeResult.Criteria != nil {
		return input.RecipeResult.Criteria.String()
	}
	return "OCP recipe"
}
