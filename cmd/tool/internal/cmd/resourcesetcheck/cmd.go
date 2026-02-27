// Package resourcesetcheck implements the resource-set:check subcommand.
package resourcesetcheck

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/rancher/backup-restore-operator/cmd/tool/internal/chart"
	"sigs.k8s.io/yaml"
)

// Run implements the resource-set:check subcommand.
func Run(args []string) error {
	fs := flag.NewFlagSet("resource-set:check", flag.ContinueOnError)

	var (
		version      string
		chartPath    string
		resourceFlag string
		namespace    string
		apiVersion   string
		resourcePath string
		outputFmt    string
	)
	fs.StringVar(&version, "version", "", "BRO version to check against (e.g. v2.1.0); fetches chart from GitHub.")
	fs.StringVar(&chartPath, "path", "", "Path to a local rancher-backup helm chart directory or .tgz.")
	fs.StringVar(&resourceFlag, "resource", "", "Resource to check in kind/name format (e.g. Deployment/my-app).")
	fs.StringVar(&namespace, "namespace", "", "Namespace of the resource (for namespace-scoped resources).")
	fs.StringVar(&apiVersion, "api-version", "", "API version of the resource (e.g. apps/v1). Omit to match any.")
	fs.StringVar(&resourcePath, "resource-path", "", "Path to a YAML/JSON file describing the resource (provides kind, name, namespace, labels).")
	fs.StringVar(&outputFmt, "output", "table", "Output format: table or json.")

	if err := fs.Parse(args); err != nil {
		return err
	}

	chartDir, err := resolveChartPath(version, chartPath)
	if err != nil {
		fs.Usage()
		return err
	}

	res, err := resolveResource(resourceFlag, namespace, apiVersion, resourcePath)
	if err != nil {
		fs.Usage()
		return err
	}

	resourceSets, err := chart.LoadAndRenderResourceSets(chartDir)
	if err != nil {
		return fmt.Errorf("rendering ResourceSets: %w", err)
	}

	results, err := chart.Check(res, resourceSets)
	if err != nil {
		return fmt.Errorf("checking resource: %w", err)
	}

	if err := printResults(os.Stdout, res, results, outputFmt); err != nil {
		return err
	}

	if len(results) == 0 {
		// Non-zero exit so callers can detect "not covered" in scripts.
		os.Exit(1)
	}
	return nil
}

func resolveChartPath(version, path string) (string, error) {
	switch {
	case path != "" && version != "":
		return "", fmt.Errorf("--version and --path are mutually exclusive")
	case path != "":
		return path, nil
	case version != "":
		return chart.FetchChartByVersion(version)
	default:
		return "", fmt.Errorf("one of --version or --path is required")
	}
}

// resolveResource builds a ResourceInfo from the --resource / --resource-path flags.
// Either --resource or --resource-path must be provided, but not both.
func resolveResource(resourceFlag, namespace, apiVersion, resourcePath string) (chart.ResourceInfo, error) {
	switch {
	case resourceFlag != "" && resourcePath != "":
		return chart.ResourceInfo{}, fmt.Errorf("--resource and --resource-path are mutually exclusive")
	case resourceFlag != "":
		return parseResourceFlag(resourceFlag, namespace, apiVersion)
	case resourcePath != "":
		return parseResourceFile(resourcePath, namespace, apiVersion)
	default:
		return chart.ResourceInfo{}, fmt.Errorf("one of --resource or --resource-path is required")
	}
}

// parseResourceFlag parses "kind/name" (e.g. "Deployment/my-app") into a ResourceInfo.
func parseResourceFlag(s, namespace, apiVersion string) (chart.ResourceInfo, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return chart.ResourceInfo{}, fmt.Errorf("--resource must be in kind/name format (e.g. Deployment/my-app), got %q", s)
	}
	return chart.ResourceInfo{
		APIVersion: apiVersion,
		Kind:       parts[0],
		Name:       parts[1],
		Namespace:  namespace,
	}, nil
}

// k8sObject is a minimal struct for reading kind/name/namespace/labels from a resource file.
type k8sObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
}

// parseResourceFile reads a YAML/JSON resource file and extracts ResourceInfo from it.
// If --namespace or --api-version are provided they override what's in the file.
func parseResourceFile(path, nsOverride, avOverride string) (chart.ResourceInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return chart.ResourceInfo{}, fmt.Errorf("reading resource file %q: %w", path, err)
	}

	var obj k8sObject
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return chart.ResourceInfo{}, fmt.Errorf("parsing resource file %q: %w", path, err)
	}

	if obj.Kind == "" {
		return chart.ResourceInfo{}, fmt.Errorf("resource file %q: missing 'kind' field", path)
	}
	if obj.Metadata.Name == "" {
		return chart.ResourceInfo{}, fmt.Errorf("resource file %q: missing 'metadata.name' field", path)
	}

	av := obj.APIVersion
	if avOverride != "" {
		av = avOverride
	}
	ns := obj.Metadata.Namespace
	if nsOverride != "" {
		ns = nsOverride
	}

	return chart.ResourceInfo{
		APIVersion: av,
		Kind:       obj.Kind,
		Name:       obj.Metadata.Name,
		Namespace:  ns,
		Labels:     obj.Metadata.Labels,
	}, nil
}

func printResults(w io.Writer, res chart.ResourceInfo, results []chart.MatchResult, format string) error {
	switch format {
	case "json":
		return printJSON(w, results)
	case "table":
		return printTable(w, res, results)
	default:
		return fmt.Errorf("unknown output format %q (want: table, json)", format)
	}
}

func printJSON(w io.Writer, results []chart.MatchResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(results)
}

func printTable(w io.Writer, res chart.ResourceInfo, results []chart.MatchResult) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()

	// Summary header.
	desc := res.Kind + "/" + res.Name
	if res.Namespace != "" {
		desc = res.Namespace + "/" + desc
	}
	if res.APIVersion != "" {
		desc = res.APIVersion + " " + desc
	}

	if len(results) == 0 {
		fmt.Fprintf(tw, "No matching ResourceSet rules found for: %s\n", desc)
		return nil
	}

	fmt.Fprintf(tw, "Matching rules for: %s\n\n", desc)
	fmt.Fprintf(tw, "  ResourceSet\t#\tSource\tAPIVersion\tKinds\tNames\tNamespaces\tCaveats\n")
	fmt.Fprintf(tw, "  -----------\t-\t------\t----------\t-----\t-----\t----------\t-------\n")

	for _, m := range results {
		sel := m.Selector
		caveats := strings.Join(m.Caveats, "; ")
		fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.ResourceSetName,
			m.SelectorIndex,
			m.SelectorSource,
			sel.APIVersion,
			fmtSelector(sel.Kinds, sel.KindsRegexp),
			fmtSelector(sel.ResourceNames, sel.ResourceNameRegexp),
			fmtSelector(sel.Namespaces, sel.NamespaceRegexp),
			caveats,
		)
	}
	return nil
}

// fmtSelector formats a list/regexp pair for table display.
func fmtSelector(list []string, re string) string {
	var parts []string
	if len(list) > 0 {
		parts = append(parts, strings.Join(list, ","))
	}
	if re != "" {
		parts = append(parts, "~"+re)
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, " | ")
}
