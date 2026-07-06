package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func baseControllerGenCmd() []string {
	return []string{
		"-modfile",
		"gotools/controller-gen/go.mod",
		"tool",
		"controller-gen",
	}
}

func main() {
	// Define controller-gen command and arguments
	cmdArgs := append(baseControllerGenCmd(), []string{
		"crd:generateEmbeddedObjectMeta=true,allowDangerousTypes=false",
		"paths=./pkg/apis/...",
		"output:crd:dir=./pkg/crds/yaml/generated",
	}...)

	fmt.Printf("Executing command: go %s\n", strings.Join(cmdArgs, " "))
	runControllerGen(cmdArgs)

	// Remove empty CRD
	cleanEmptyCRD("./pkg/crds/yaml/generated/_.yaml")

	// Copy generated CRDs to chart templates
	copyCRDsToChart()

	fmt.Println("controller-gen command executed successfully.")
}

func cleanEmptyCRD(emptyCRDPath string) {
	if _, err := os.Stat(emptyCRDPath); err == nil {
		fmt.Printf("Removing empty CRD: %s\n", emptyCRDPath)
		if err := os.Remove(emptyCRDPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing empty CRD %s: %v\n", emptyCRDPath, err)
			os.Exit(1)
		}
		fmt.Println("Empty CRD removed successfully.")
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error checking for empty CRD %s: %v\n", emptyCRDPath, err)
		os.Exit(1)
	} else {
		fmt.Println("No empty CRD found to remove.")
	}
}

func runControllerGen(cmdArgs []string) {
	cmd := exec.Command("go", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "GOWORK=off")

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "controller-gen command failed: %v\n", err)
		os.Exit(1)
	}
}

func copyCRDsToChart() {
	// Mapping of generated CRD files to chart template names
	crdMapping := map[string]string{
		"resources.cattle.io_backups.yaml":      "backup.yaml",
		"resources.cattle.io_resourcesets.yaml": "resourceset.yaml",
		"resources.cattle.io_restores.yaml":     "restore.yaml",
	}

	srcDir := "./pkg/crds/yaml/generated"
	dstDir := "./charts/rancher-backup-crd/templates"

	fmt.Println("Copying CRDs to chart templates...")

	for srcFile, dstFile := range crdMapping {
		srcPath := filepath.Join(srcDir, srcFile)
		dstPath := filepath.Join(dstDir, dstFile)

		if err := copyFile(srcPath, dstPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error copying %s to %s: %v\n", srcPath, dstPath, err)
			os.Exit(1)
		}

		fmt.Printf("  Copied %s -> %s\n", srcFile, dstFile)
	}

	fmt.Println("CRDs copied to chart templates successfully.")
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	return destFile.Sync()
}
