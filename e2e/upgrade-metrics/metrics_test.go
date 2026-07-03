package upgrade_metrics_test

import (
	"embed"
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"path/filepath"
	"sync"
	"time"

	. "github.com/kralicky/kmatch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/operator"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	//go:embed testdata
	testDataFS embed.FS
)

func loadTestData(feature, filename string) []byte {
	data, err := fs.ReadFile(testDataFS, path.Join("testdata", feature, filename))
	if err != nil {
		panic(err)
	}
	return data
}

// Metrics-related upgrade and regression tests
var _ = Describe("Metrics upgrade scenarios", func() {

	// Test: Restore metrics with nil Prune field
	// Covers: SURE-11795
	// Bug: Nil pointer crash in metrics collection when Restore.Spec.Prune is nil
	// Occurs: Upgrading from v9 to v10 with completed in-place restores
	// Location: pkg/monitoring/metrics.go:154
	// Note: If this regresses, add the new JIRA ID to the "Covers:" line above
	Describe("Restore metrics with nil pointer fields", Ordered, Label("upgrade", "regression", "metrics", "SURE-11795"), func() {
		var o *ObjectTracker

		const (
			// Version that has the nil pointer bug in metrics collection
			buggyVersion    = "v10.0.5"
			deploymentName  = "rancher-backup"
			testRestoreName = "legacy-restore-with-nil-prune"
		)

		BeforeAll(func() {
			o = &ObjectTracker{arr: []client.Object{}, mu: sync.Mutex{}}
			DeferCleanup(func() {
				o.DeleteAll()
			})

			By("cleaning up any pre-existing test resources")
			cleanupPreExistingResources()

			By("uninstalling current CRD chart to install old version")
			uninstallCRDChart()

			By("installing old CRD chart version that lacks Prune default")
			installCRDChart(buggyVersion)

			By("deploying buggy BRO version via Helm")
			deployOperatorViaHelm(buggyVersion)

			By("waiting for buggy operator deployment to be ready")
			waitForDeploymentReady(deploymentName)
		})

		AfterAll(func() {
			By("cleaning up Helm deployment")
			uninstallOperator()

			By("cleaning up old CRD chart")
			uninstallCRDChart()

			By("reinstalling current CRD chart for subsequent tests")
			// Reinstall the current CRDs that were installed by testenv
			// Use glob to find the chart file
			charts, err := filepath.Glob("../../build/artifacts/rancher-backup-crd-*.tgz")
			if err != nil || len(charts) == 0 {
				fmt.Printf("Warning: Could not find current CRD chart to reinstall\n")
				return
			}

			cmd := exec.Command("helm", "install", "rancher-backup-crd",
				charts[0],
				"--namespace", ts.ChartNamespace,
				"--wait",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				fmt.Printf("Warning: Failed to reinstall current CRD chart: %s\n", string(output))
			}
		})

		Context("when a Restore CR with nil Prune exists", func() {
			It("should create the legacy Restore CR with nil Prune field", func() {
				By("loading the test restore from YAML")
				restoreYAML := loadTestData("metrics", "restore-nil-prune.yaml")

				restore := &backupv1.Restore{}
				Expect(yaml.Unmarshal(restoreYAML, restore)).To(Succeed(),
					"Failed to unmarshal restore YAML")

				By("creating the Restore CR in the cluster")
				o.Add(restore)
				Expect(k8sClient.Create(testCtx, restore)).To(Succeed(),
					"Failed to create Restore CR")

				By("verifying the Restore CR exists")
				Eventually(Object(restore)).Should(Exist())

				By("verifying Prune field is nil (old CRD has no default)")
				fetchedRestore := &backupv1.Restore{}
				Expect(k8sClient.Get(testCtx,
					client.ObjectKey{Name: testRestoreName},
					fetchedRestore)).To(Succeed())
				Expect(fetchedRestore.Spec.Prune).To(BeNil(),
					"Prune field should be nil with old CRD to replicate the bug condition")
			})

			It("should cause the buggy version to crash loop", func() {
				By("checking if the operator pod crashes due to nil pointer")
				deployment := &appsv1.Deployment{}
				Expect(k8sClient.Get(testCtx,
					client.ObjectKey{Name: deploymentName, Namespace: ts.ChartNamespace},
					deployment)).To(Succeed())

				// The buggy version should crash when trying to collect metrics
				// on the Restore with nil Prune.
				// Note: v10.0.5 has hardcoded 60s metrics interval, so we need to wait
				// at least 60s for the first metrics collection to run and trigger the crash
				Eventually(func() bool {
					return checkPodCrashLooping(deployment.Spec.Selector.MatchLabels)
				}, 90*time.Second, 2*time.Second).Should(BeTrue(),
					"Buggy operator should crash loop due to nil pointer dereference")

				By("retrieving crash logs for verification")
				podList := &corev1.PodList{}
				Expect(k8sClient.List(testCtx, podList,
					client.InNamespace(ts.ChartNamespace),
					client.MatchingLabels(deployment.Spec.Selector.MatchLabels),
				)).To(Succeed())

				if len(podList.Items) > 0 {
					logs, err := getPodLogs(podList.Items[0].Name)
					if err == nil && logs != "" {
						GinkgoWriter.Printf("Pod logs showing crash:\n%s\n", logs)
						// Optionally verify the panic message contains expected text
						Expect(logs).To(ContainSubstring("panic"),
							"Logs should contain panic from nil pointer dereference")
					}
				}
			})

			It("should be fixed in the current version", func() {
				By("stopping the buggy Helm deployment")
				scaleDeploymentToZero(deploymentName)

				By("starting current code in-process")
				errC, cancel := SetupOperator(testCtx, restCfg, operator.RunOptions{
					ChartNamespace:         ts.ChartNamespace,
					MetricsServerEnabled:   true,
					MetricsPort:            8080,
					MetricsIntervalSeconds: 1,
				})
				DeferCleanup(func() {
					cancel()
					select {
					case err := <-errC:
						Expect(err).NotTo(HaveOccurred(),
							"Operator should not error during shutdown")
					default:
					}
				})

				By("waiting for operator to start and collect metrics")
				// Give the operator time to start up and run the metrics collection
				// which previously would have crashed
				time.Sleep(5 * time.Second)

				By("verifying operator does not crash")
				select {
				case err := <-errC:
					Fail("Operator crashed with error: " + err.Error())
				default:
					// No error means operator is running successfully
				}

				By("verifying the Restore is still accessible")
				fetchedRestore := &backupv1.Restore{}
				Expect(k8sClient.Get(testCtx,
					client.ObjectKey{Name: testRestoreName},
					fetchedRestore)).To(Succeed(),
					"Restore CR should still be accessible")

				By("verifying operator reconciles without panic")
				// The operator should handle nil Prune gracefully
				// If we get here without panic, the bug is fixed
				Consistently(func() error {
					select {
					case err := <-errC:
						return err
					default:
						return nil
					}
				}, 10*time.Second, 1*time.Second).Should(BeNil(),
					"Operator should continue running without crashes")
			})
		})

	})

	// Add more metrics-related upgrade tests here
	// Each test should document which JIRA IDs it covers in comments
})
