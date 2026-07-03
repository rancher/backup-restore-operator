package backup_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	. "github.com/kralicky/kmatch"
	"github.com/minio/minio-go/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rancher/backup-restore-operator/e2e/fixtures"
	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	preserveFieldsBackup   = "preserve-fields.tar.gz"
	deletionGraceBackup    = "deletion-grace-backup.tar.gz"
	encryptedRestoreBackup = "encrypted-resources.tar.gz"
)

func formatRestoreMetrics(restores []string) string {
	var metrics string

	rancherRestoreCountHeader := fmt.Sprint(`
	# HELP rancher_restore_count Number of existing Rancher Restore CRs
	# TYPE rancher_restore_count gauge
	`)

	metrics += rancherRestoreCountHeader
	metrics += fmt.Sprintf("rancher_restore_count %d", len(restores))

	return metrics + "\n"
}

func formatRestoreMetadataMetrics(restores []backupv1.Restore) string {
	var metrics string

	rancherRestoreHeader := fmt.Sprint(`
	# HELP rancher_restore_info Details on a specific Rancher Restore CR
	# TYPE rancher_restore_info gauge
	`)

	metrics += rancherRestoreHeader

	var restoreMessage string
	for _, r := range restores {
		if len(r.Status.Conditions) > 0 {
			restoreMessage = r.Status.Conditions[0].Message
		}

		metrics += fmt.Sprintf(`
		rancher_restore_info{fileName="%s",name="%s",prune="%t",restoreTime="%s",status="%s",storageLocation="%s"} 1
		`, r.Spec.BackupFilename, r.Name, r.Spec.GetPrune(), r.Status.RestoreCompletionTS, restoreMessage, r.Status.BackupSource)
	}

	return metrics
}

func isRestoreSuccessful(b *backupv1.Restore) error {
	bD, err := Object(b)()
	if err != nil {
		return err
	}

	if !backupv1.RestoreConditionReady.IsTrue(bD) {
		message := backupv1.RestoreConditionReady.GetMessage(bD)
		return fmt.Errorf("backup %s did not upload %s", b.Name, message)
	}

	message := strings.ToLower(strings.TrimSpace(backupv1.RestoreConditionReady.GetMessage(bD)))
	if message != "completed" {
		return fmt.Errorf("The restore was not eventually completed : %s", message)
	}
	return nil
}

var _ = Describe("Restore from remote driver", Ordered, Label("integration"), func() {
	var o *ObjectTracker
	var minioClient *minio.Client
	var minioEndpoint string
	BeforeAll(func() {
		o = &ObjectTracker{
			arr: []client.Object{},
			mu:  sync.Mutex{},
		}

		DeferCleanup(func() {
			o.DeleteAll()
		})
		SetupEncryption(o)
		minioClient, minioEndpoint = SetupMinio(o)
	})

	When("we restore a non encrypted backup", func() {
		It("should upload the required backups to the remote store", func() {
			Expect(minioClient.MakeBucket(testCtx, insecureBucket, minio.MakeBucketOptions{})).To(Succeed())

			By("uploading the preserve-unknown-fields backup")
			ctxCa, caT := context.WithTimeout(testCtx, 10*time.Second)
			defer caT()
			preserveData := fixtures.Data("restore/preserve-unknown-fields.tar.gz")
			_, err := minioClient.PutObject(
				ctxCa,
				insecureBucket,
				preserveFieldsBackup,
				bytes.NewReader(preserveData),
				int64(len(preserveData)),
				minio.PutObjectOptions{},
			)
			Expect(err).NotTo(HaveOccurred())

			By("uploading deletion grace period backup")
			deleteData := fixtures.Data("restore/deletion-grace-period-seconds.tar.gz")
			_, err = minioClient.PutObject(
				ctxCa,
				insecureBucket,
				deletionGraceBackup,
				bytes.NewReader(deleteData),
				int64(len(deleteData)),
				minio.PutObjectOptions{},
			)
			Expect(err).NotTo(HaveOccurred())

			objectInfo := minioClient.ListObjects(ctxCa, insecureBucket, minio.ListObjectsOptions{})
			i := 0
			for range objectInfo {
				i++
			}
			Expect(i).To(Equal(2))
		})

		It("should restore while preserving unknown fields", func() {
			r := &backupv1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "s3-restore-preserve-unknown-fields",
				},
				Spec: backupv1.RestoreSpec{
					BackupFilename: preserveFieldsBackup,
					Prune:          lo.ToPtr(false),
					StorageLocation: &backupv1.StorageLocation{
						S3: &backupv1.S3ObjectStore{
							CredentialSecretName:      credentialSecretName,
							CredentialSecretNamespace: ts.ChartNamespace,
							BucketName:                insecureBucket,
							Endpoint:                  minioEndpoint,
						},
					},
				},
			}
			o.Add(r)
			Expect(k8sClient.Create(testCtx, r)).To(Succeed())
			Eventually(Object(r)).Should(Exist())

			Eventually(func() error {
				return isRestoreSuccessful(r)
			}).Should(Succeed())

		})
		Specify("ensure collected metrics match expected", func() {
			Eventually(func() error {
				expected := formatRestoreMetrics([]string{
					"s3-restore-preserve-unknown-fields",
				})

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_restore_count",
				)
			}).Should(Succeed())
		})

		It("should preserve deletion grace periods", func() {
			r := &backupv1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "s3-deletion-grace-period",
				},
				Spec: backupv1.RestoreSpec{
					BackupFilename: deletionGraceBackup,
					Prune:          lo.ToPtr(false),
					StorageLocation: &backupv1.StorageLocation{
						S3: &backupv1.S3ObjectStore{
							CredentialSecretName:      credentialSecretName,
							CredentialSecretNamespace: ts.ChartNamespace,
							BucketName:                insecureBucket,
							Endpoint:                  minioEndpoint,
						},
					},
				},
			}
			o.Add(r)
			Expect(k8sClient.Create(testCtx, r)).To(Succeed())
			Eventually(Object(r)).Should(Exist())

			Eventually(func() error {
				return isRestoreSuccessful(r)
			}).Should(Succeed())
		})
		Specify("ensure collected metrics match expected", func() {
			Eventually(func() error {
				expected := formatRestoreMetrics([]string{
					"s3-restore-preserve-unknown-fields",
					"s3-deletion-grace-period",
				})

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_restore_count",
				)
			}).Should(Succeed())
		})
	})

	When("we restore from an encrypted backup", func() {
		It("should upload the test files to the remote store", func() {
			Expect(minioClient.MakeBucket(testCtx, secureBucket, minio.MakeBucketOptions{})).To(Succeed())

			By("uploading the preserve-unknown-fields backup")
			ctxCa, caT := context.WithTimeout(testCtx, 10*time.Second)
			defer caT()
			encryptData := fixtures.Data("restore/encrypted-resources.tar.gz")
			_, err := minioClient.PutObject(
				ctxCa,
				secureBucket,
				encryptedRestoreBackup,
				bytes.NewReader(encryptData),
				int64(len(encryptData)),
				minio.PutObjectOptions{},
			)
			Expect(err).NotTo(HaveOccurred())
			objectInfo := minioClient.ListObjects(ctxCa, secureBucket, minio.ListObjectsOptions{})
			i := 0
			for range objectInfo {
				i++
			}
			Expect(i).To(Equal(1))
		})
		It("should restore the encrypted resources", func() {
			r := &backupv1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "s3-encrypted",
				},
				Spec: backupv1.RestoreSpec{
					BackupFilename:             encryptedRestoreBackup,
					EncryptionConfigSecretName: encSecret,
					Prune:                      lo.ToPtr(false),
					StorageLocation: &backupv1.StorageLocation{
						S3: &backupv1.S3ObjectStore{
							CredentialSecretName:      credentialSecretName,
							CredentialSecretNamespace: ts.ChartNamespace,
							BucketName:                secureBucket,
							Endpoint:                  minioEndpoint,
						},
					},
				},
			}
			o.Add(r)
			Expect(k8sClient.Create(testCtx, r)).To(Succeed())
			Eventually(func() error {
				return isRestoreSuccessful(r)
			}).Should(Succeed())
		})
		Specify("ensure collected metrics match expected", func() {
			Eventually(func() error {
				expected := formatRestoreMetrics([]string{
					"s3-restore-preserve-unknown-fields",
					"s3-deletion-grace-period",
					"s3-encrypted",
				})

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_restore_count",
				)
			}).Should(Succeed())
		})
	})

	When("we restore with Prune field unset (CRD default handling)", func() {
		It("should handle unset Prune field with CRD default", func() {
			// Note: The CRD has +kubebuilder:default:=true, so when Prune is omitted,
			// the API server applies Prune=true automatically. This is the correct behavior
			// for NEW Restores and prevents nil values going forward.
			//
			// OLD Restores (created before the default) may have Prune=nil. The GetPrune()
			// helper handles this for backward compatibility. See unit tests in
			// pkg/monitoring/metrics_test.go:TestUpdateRestoreMetricsWithNilPrune for
			// verification of nil handling.
			r := &backupv1.Restore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "s3-restore-nil-prune",
				},
				Spec: backupv1.RestoreSpec{
					BackupFilename: preserveFieldsBackup,
					// Prune field intentionally omitted - CRD default will apply Prune=true
					StorageLocation: &backupv1.StorageLocation{
						S3: &backupv1.S3ObjectStore{
							CredentialSecretName:      credentialSecretName,
							CredentialSecretNamespace: ts.ChartNamespace,
							BucketName:                insecureBucket,
							Endpoint:                  minioEndpoint,
						},
					},
				},
			}
			o.Add(r)
			Expect(k8sClient.Create(testCtx, r)).To(Succeed())
			Eventually(Object(r)).Should(Exist())

			Eventually(func() error {
				return isRestoreSuccessful(r)
			}).Should(Succeed())
		})

		It("should verify GetPrune() defaults correctly", func() {
			// Note: The CRD now has +kubebuilder:default:=true, which means the API server
			// automatically applies Prune=true when the field is omitted or null.
			// This prevents NEW Restores from having Prune=nil, which is correct behavior.
			//
			// However, OLD Restores created before the default was added could still have Prune=nil.
			// The GetPrune() helper function handles this case for backward compatibility.
			//
			// Nil handling verification: See unit tests in pkg/monitoring/metrics_test.go:
			// - TestUpdateRestoreMetricsWithNilPrune (line 214)
			// - TestUpdateRestoreMetricsWithMixedPruneValues (line 249)
			// These verify that metrics warmup handles Prune=nil correctly without crashing.

			By("verifying the Restore exists (will have Prune=true due to CRD default)")
			var restore backupv1.Restore
			Expect(k8sClient.Get(testCtx, client.ObjectKey{Name: "s3-restore-nil-prune"}, &restore)).To(Succeed())

			By("verifying GetPrune() returns true when Prune field uses CRD default")
			// With the CRD default, Prune will be a pointer to true (not nil)
			Expect(restore.Spec.Prune).NotTo(BeNil(), "CRD default should apply Prune=true")
			Expect(restore.Spec.GetPrune()).To(BeTrue(), "GetPrune() should return true")
		})

		Specify("ensure restore count includes nil Prune restore", func() {
			Eventually(func() error {
				expected := formatRestoreMetrics([]string{
					"s3-restore-preserve-unknown-fields",
					"s3-deletion-grace-period",
					"s3-encrypted",
					"s3-restore-nil-prune",
				})

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_restore_count",
				)
			}).Should(Succeed())
		})
	})


	When("we're done with all test restores", func() {
		Specify("we should eventually have the correct restore metadata metrics", func() {

			Eventually(func() error {
				var restores backupv1.RestoreList

				Expect(k8sClient.List(testCtx, &restores)).To(Succeed())
				expected := formatRestoreMetadataMetrics(restores.Items)

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_restore_info",
				)
			}).Should(Succeed())
		})
	})
})

// TODO : left as an exercise to the reader
var _ = Describe("Restore from local driver", Ordered, Label("integration"), func() {
	BeforeAll(func() {

	})
})
