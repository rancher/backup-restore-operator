package upgrade_metrics_test

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/backup-restore-operator/pkg/operator"
	"k8s.io/client-go/rest"
)

// ObjectTracker helps track and cleanup test resources
type ObjectTracker struct {
	mu  sync.Mutex
	arr []client.Object
}

func (o *ObjectTracker) Add(obj client.Object) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.arr = append(o.arr, obj)
}

func (o *ObjectTracker) DeleteAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, obj := range o.arr {
		_ = k8sClient.Delete(testCtx, obj)
	}
}

// installCRDChart installs the CRD chart at a specific version
func installCRDChart(version string) {
	// Remove 'v' prefix from version for chart filename
	chartVersion := version
	if chartVersion[0] == 'v' {
		chartVersion = chartVersion[1:]
	}
	chartFilename := fmt.Sprintf("rancher-backup-crd-%s.tgz", chartVersion)
	chartURL := fmt.Sprintf("https://github.com/rancher/backup-restore-operator/releases/download/%s/%s", version, chartFilename)

	// Download the chart with curl
	curlCmd := exec.Command("curl", "-L", "-o", chartFilename, chartURL)
	curlOutput, err := curlCmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Curl output: %s\n", string(curlOutput))
		Expect(err).NotTo(HaveOccurred(), "Failed to download CRD chart")
	}

	// Install the chart
	cmd := exec.Command("helm", "install", "rancher-backup-crd",
		chartFilename,
		"--namespace", ts.ChartNamespace,
		"--wait",
		"--timeout", "2m",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		fmt.Printf("Helm CRD install stdout: %s\n", stdout.String())
		fmt.Printf("Helm CRD install stderr: %s\n", stderr.String())
	}
	Expect(err).NotTo(HaveOccurred(), "Failed to install CRD chart via Helm")
}

// uninstallCRDChart removes the CRD Helm release and cleans up downloaded charts
func uninstallCRDChart() {
	cmd := exec.Command("helm", "uninstall", "rancher-backup-crd",
		"--namespace", ts.ChartNamespace,
		"--wait",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Warning: Failed to uninstall CRD chart: %s\n", string(output))
	}

	// Clean up downloaded chart file
	exec.Command("rm", "-f", "rancher-backup-crd-*.tgz").Run()
}

// deployOperatorViaHelm installs the operator using Helm at a specific version
func deployOperatorViaHelm(version string) {
	// Remove 'v' prefix from version for chart filename
	chartVersion := version
	if chartVersion[0] == 'v' {
		chartVersion = chartVersion[1:]
	}
	chartFilename := fmt.Sprintf("rancher-backup-%s.tgz", chartVersion)
	chartURL := fmt.Sprintf("https://github.com/rancher/backup-restore-operator/releases/download/%s/%s", version, chartFilename)

	// Download the chart with curl
	curlCmd := exec.Command("curl", "-L", "-o", chartFilename, chartURL)
	curlOutput, err := curlCmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Curl output: %s\n", string(curlOutput))
		Expect(err).NotTo(HaveOccurred(), "Failed to download operator chart")
	}

	// Install the chart with metrics enabled
	// Note: v10.0.5 has hardcoded 60s metrics interval in main.go
	cmd := exec.Command("helm", "install", "rancher-backup",
		chartFilename,
		"--namespace", ts.ChartNamespace,
		"--set", fmt.Sprintf("image.tag=%s", version),
		"--set", "image.repository=rancher/backup-restore-operator",
		"--set", "monitoring.metrics.enabled=true", // Correct path in v10.0.5 chart
		"--wait",
		"--timeout", "3m",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		fmt.Printf("Helm stdout: %s\n", stdout.String())
		fmt.Printf("Helm stderr: %s\n", stderr.String())
	}
	Expect(err).NotTo(HaveOccurred(), "Failed to deploy operator via Helm")
}

// uninstallOperator removes the Helm release and cleans up downloaded charts
func uninstallOperator() {
	cmd := exec.Command("helm", "uninstall", "rancher-backup",
		"--namespace", ts.ChartNamespace,
		"--wait",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Warning: Failed to uninstall operator: %s\n", string(output))
	}

	// Clean up downloaded chart file
	exec.Command("rm", "-f", "rancher-backup-*.tgz").Run()
}

// waitForDeploymentReady waits for a deployment to have all replicas ready
func waitForDeploymentReady(name string) {
	Eventually(func() bool {
		deployment := &appsv1.Deployment{}
		err := k8sClient.Get(testCtx,
			client.ObjectKey{Name: name, Namespace: ts.ChartNamespace},
			deployment)
		if err != nil {
			return false
		}
		return deployment.Status.ReadyReplicas > 0 &&
			deployment.Status.ReadyReplicas == *deployment.Spec.Replicas
	}, 90*time.Second, 2*time.Second).Should(BeTrue(), "Deployment should be ready")
}

// scaleDeploymentToZero scales a deployment to 0 replicas and waits for pods to terminate
func scaleDeploymentToZero(name string) {
	deployment := &appsv1.Deployment{}
	err := k8sClient.Get(testCtx,
		client.ObjectKey{Name: name, Namespace: ts.ChartNamespace},
		deployment)
	Expect(err).NotTo(HaveOccurred(), "Failed to get deployment")

	// Scale to zero
	zero := int32(0)
	deployment.Spec.Replicas = &zero
	err = k8sClient.Update(testCtx, deployment)
	Expect(err).NotTo(HaveOccurred(), "Failed to scale deployment to zero")

	// Wait for all pods to terminate
	Eventually(func() int {
		podList := &corev1.PodList{}
		err := k8sClient.List(testCtx, podList,
			client.InNamespace(ts.ChartNamespace),
			client.MatchingLabels(deployment.Spec.Selector.MatchLabels),
		)
		if err != nil {
			return -1
		}
		return len(podList.Items)
	}, 60*time.Second, 2*time.Second).Should(Equal(0), "All pods should terminate")
}

// checkPodCrashLooping checks if pods are crash looping (multiple restarts)
func checkPodCrashLooping(labelSelector map[string]string) bool {
	podList := &corev1.PodList{}
	err := k8sClient.List(testCtx, podList,
		client.InNamespace(ts.ChartNamespace),
		client.MatchingLabels(labelSelector),
	)
	if err != nil {
		return false
	}

	if len(podList.Items) == 0 {
		return false
	}

	// Check if any container has restart count > 0
	for _, pod := range podList.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.RestartCount > 0 {
				return true
			}
		}
	}
	return false
}

// getPodLogs retrieves logs from a pod (including previous crashed container)
func getPodLogs(podName string) (string, error) {
	// Try to get logs from previous container first (which had the crash)
	req := clientSet.CoreV1().Pods(ts.ChartNamespace).GetLogs(podName, &corev1.PodLogOptions{
		Previous: true,
	})
	logs, err := req.DoRaw(testCtx)
	if err != nil {
		// If previous logs don't exist, get current logs
		req = clientSet.CoreV1().Pods(ts.ChartNamespace).GetLogs(podName, &corev1.PodLogOptions{})
		logs, err = req.DoRaw(testCtx)
		if err != nil {
			return "", err
		}
	}
	return string(logs), nil
}

// cleanupPreExistingResources removes resources from previous test runs that might
// conflict with Helm installation (e.g., ResourceSets created by backup tests)
func cleanupPreExistingResources() {
	// Delete all ResourceSets that might have been created by previous tests
	resourceSets := &unstructured.UnstructuredList{}
	resourceSets.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "resources.cattle.io",
		Version: "v1",
		Kind:    "ResourceSetList",
	})

	err := k8sClient.List(testCtx, resourceSets)
	if err != nil {
		fmt.Printf("Warning: Failed to list ResourceSets during cleanup: %v\n", err)
		return
	}

	for _, rs := range resourceSets.Items {
		err := k8sClient.Delete(testCtx, &rs)
		if err != nil {
			fmt.Printf("Warning: Failed to delete ResourceSet %s: %v\n", rs.GetName(), err)
		} else {
			fmt.Printf("Deleted pre-existing ResourceSet: %s\n", rs.GetName())
		}
	}

	// Wait for ResourceSets to be fully deleted
	Eventually(func() int {
		resourceSets := &unstructured.UnstructuredList{}
		resourceSets.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "resources.cattle.io",
			Version: "v1",
			Kind:    "ResourceSetList",
		})
		_ = k8sClient.List(testCtx, resourceSets)
		return len(resourceSets.Items)
	}, 30*time.Second, 1*time.Second).Should(Equal(0), "All ResourceSets should be deleted")
}

// SetupOperator starts the operator in-process (reused from backup suite pattern)
func SetupOperator(ctx context.Context, kubeconfig *rest.Config, options operator.RunOptions) (chan error, context.CancelFunc) {
	ctxca, ca := context.WithCancel(ctx)
	errC := make(chan error, 1)
	go func() {
		err := operator.Run(ctxca, kubeconfig, options)
		errC <- err
	}()
	return errC, ca
}
