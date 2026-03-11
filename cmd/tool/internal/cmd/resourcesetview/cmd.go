// Package resourcesetview implements the resource-set:view subcommand.
package resourcesetview

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

// Run implements the resource-set:view subcommand.
func Run(args []string) error {
	fs := flag.NewFlagSet("resource-set:view", flag.ContinueOnError)

	var (
		version   string
		chartPath string
		outputFmt string
	)
	fs.StringVar(&version, "version", "", "BRO version to view (e.g. v2.1.0); fetches chart from GitHub.")
	fs.StringVar(&chartPath, "path", "", "Path to a local rancher-backup helm chart directory.")
	fs.StringVar(&outputFmt, "output", "table", "Output format: table, yaml, or json.")

	if err := fs.Parse(args); err != nil {
		return err
	}

	path, err := resolveChartPath(version, chartPath)
	if err != nil {
		fs.Usage()
		return err
	}

	resourceSets, err := chart.LoadAndRenderResourceSets(path)
	if err != nil {
		return fmt.Errorf("rendering ResourceSets: %w", err)
	}

	if len(resourceSets) == 0 {
		fmt.Fprintln(os.Stderr, "No ResourceSets found in chart.")
		return nil
	}

	return printResourceSets(os.Stdout, resourceSets, outputFmt)
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

func printResourceSets(w io.Writer, resourceSets []*chart.AnnotatedResourceSet, format string) error {
	switch format {
	case "yaml":
		return printYAML(w, resourceSets)
	case "json":
		return printJSON(w, resourceSets)
	case "table":
		return printTable(w, resourceSets)
	default:
		return fmt.Errorf("unknown output format %q (want: table, yaml, json)", format)
	}
}

func printYAML(w io.Writer, resourceSets []*chart.AnnotatedResourceSet) error {
	for i, ars := range resourceSets {
		if i > 0 {
			fmt.Fprintln(w, "---")
		}
		out, err := yaml.Marshal(ars.ResourceSet)
		if err != nil {
			return fmt.Errorf("marshaling ResourceSet %q: %w", ars.Name, err)
		}
		fmt.Fprint(w, string(out))
	}
	return nil
}

func printJSON(w io.Writer, resourceSets []*chart.AnnotatedResourceSet) error {
	out := make([]interface{}, len(resourceSets))
	for i, ars := range resourceSets {
		out[i] = ars.ResourceSet
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func printTable(w io.Writer, resourceSets []*chart.AnnotatedResourceSet) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()

	for _, ars := range resourceSets {
		fmt.Fprintf(tw, "ResourceSet: %s\n", ars.Name)
		fmt.Fprintf(tw, "  #\tSource\tAPIVersion\tKinds\tNames\tNamespaces\n")
		fmt.Fprintf(tw, "  -\t------\t----------\t-----\t-----\t----------\n")
		for i, sel := range ars.ResourceSelectors {
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\t%s\n",
				i+1,
				ars.SelectorSources[i],
				sel.APIVersion,
				fmtSelector(sel.Kinds, sel.KindsRegexp),
				fmtSelector(sel.ResourceNames, sel.ResourceNameRegexp),
				fmtSelector(sel.Namespaces, sel.NamespaceRegexp),
			)
		}
		fmt.Fprintln(tw)
	}
	return nil
}

// fmtSelector formats a list/regexp pair for table display.
// List items are joined with commas. A regexp is shown with a "~" prefix.
// If both are set, they are separated by " | ". Empty fields show "*".
func fmtSelector(list []string, regexp string) string {
	var parts []string
	if len(list) > 0 {
		parts = append(parts, strings.Join(list, ","))
	}
	if regexp != "" {
		parts = append(parts, "~"+regexp)
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, " | ")
}
