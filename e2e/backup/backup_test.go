package backup_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rancher/backup-restore-operator/pkg/util/encryptionconfig"

	. "github.com/kralicky/kmatch"
	"github.com/minio/minio-go/v7"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/backup-restore-operator/e2e/test"
	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/samber/lo"
	"github.com/testcontainers/testcontainers-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	encSecret                    = "encryption-config"
	s3Recurring                  = "s3-recurring"
	s3NonEncryptedBackup         = "s3-insecure"
	s3EncryptedBackup            = "s3-secure"
	localNonEncrypedBackup       = "local-driver-non-encrypted"
	localEncryptedBackup         = "local-driver-encrypted"
	localCustomResourceSetBackup = "local-custom-resource-set-backup"
	localCattleDevDriverPath     = "../../backups"

	insecureBucket  = "rancherbackups-insecure"
	secureBucket    = "rancherbackups-secure"
	recurringBucket = "rancherbackups-recurring"

	metricsURL = "http://localhost:8080/metrics"
)

func isBackupSuccessul(b *backupv1.Backup) error {
	bD, err := Object(b)()

	if err != nil {
		return err
	}

	if !condition.Cond(backupv1.BackupConditionUploaded).IsTrue(bD) {
		message := condition.Cond(backupv1.BackupConditionReady).GetMessage(bD)
		return fmt.Errorf("backup %s did not upload %s", b.Name, message)
	}
	return nil
}

type containerLogWrapper struct{}

func (c *containerLogWrapper) Accept(l testcontainers.Log) {
	GinkgoWriter.Write(append([]byte(fmt.Sprintf("Type : %s | ", l.LogType)), l.Content...))
}

const (
	accessKey            = "basedmoose"
	secretKey            = "unintelligiblebaboon"
	credentialSecretName = "s3config"
)

func formatBackupMetrics(backups []string) string {
	var metrics string

	rancherBackupCountHeader := fmt.Sprint(`
	# HELP rancher_backup_count Number of existing Rancher Backup CRs
	# TYPE rancher_backup_count gauge
	`)

	metrics += rancherBackupCountHeader
	metrics += fmt.Sprintf("rancher_backup_count %d", len(backups))

	rancherBackupsAttemptedHeader := fmt.Sprint(`
	# HELP rancher_backups_attempted_total Number of Rancher Backups processed by this operator
	# TYPE rancher_backups_attempted_total counter
	`)

	metrics += rancherBackupsAttemptedHeader
	for _, b := range backups {
		if b == s3Recurring {
			metrics += fmt.Sprintf("rancher_backups_attempted_total{name=\"%s\"} 2\n", b)
		} else {
			metrics += fmt.Sprintf("rancher_backups_attempted_total{name=\"%s\"} 1\n", b)
		}
	}

	rancherBackupsFailedHeader := fmt.Sprint(`
	# HELP rancher_backups_failed_total Number of failed Rancher Backups processed by this operator
	# TYPE rancher_backups_failed_total counter
	`)

	metrics += rancherBackupsFailedHeader
	for _, b := range backups {
		metrics += fmt.Sprintf("rancher_backups_failed_total{name=\"%s\"} 0\n", b)
	}

	return metrics + "\n"
}

func formatBackupMetadataMetrics(backups []backupv1.Backup) string {
	var metrics string

	rancherBackupHeader := fmt.Sprint(`
	# HELP rancher_backup_info Details on a specific Rancher Backup CR
	# TYPE rancher_backup_info gauge
	`)

	metrics += rancherBackupHeader

	var backupType, backupNextSnapshot, backupMessage string
	for _, b := range backups {
		backupType = b.Status.BackupType
		if backupType == "One-time" {
			backupNextSnapshot = "N/A - One-time Backup"
		} else {
			backupNextSnapshot = b.Status.NextSnapshotAt
		}

		if len(b.Status.Conditions) > 0 {
			backupMessage = b.Status.Conditions[0].Message
		}

		metrics += fmt.Sprintf(`
		rancher_backup_info{backupType="%s",filename="%s",lastSnapshot="%s",name="%s",nextSnapshot="%s",resourceSetName="%s",retentionCount="%d",status="%s",storageLocation="%s"} 1
		`, backupType, b.Status.Filename, b.Status.LastSnapshotTS, b.Name, backupNextSnapshot, b.Spec.ResourceSetName, b.Spec.RetentionCount, backupMessage, b.Status.StorageLocation)
	}

	return metrics
}

// extractTarballContents extracts the contents of a tarball from MinIO and returns them as a map of filenames to contents.
func extractTarballContents(minioClient *minio.Client, bucket, key string) (map[string][]byte, error) {
	obj, err := minioClient.GetObject(testCtx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tarball from minio: %v", err)
	}
	defer obj.Close()

	gzr, err := gzip.NewReader(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzr.Close()

	contents := make(map[string][]byte)
	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tarball: %v", err)
		}

		switch header.Typeflag {
		case tar.TypeReg:
			// file
			bb := &bytes.Buffer{}
			if _, err := io.Copy(bb, tr); err != nil {
				return nil, fmt.Errorf("failed to read file contents: %v", err)
			}

			contents[header.Name] = bb.Bytes()
		case tar.TypeDir:
			// directory
			contents[header.Name] = nil
		case tar.TypeSymlink:
			// symlink
			contents[header.Name] = []byte(header.Linkname)
		default:
			return nil, fmt.Errorf("unsupported tarball entry type: %v", header.Typeflag)
		}
	}

	return contents, nil
}

var _ = Describe("Backup e2e remote", Ordered, Label("integration"), func() {
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
		By("deploying minio locally")
		minioClient, minioEndpoint = SetupMinio(o)

		By("creating custom resourceSet")
		SetupCustomResourceSet(testCtx, o, k8sClient)
	})

	When("we take a non-encrypted backup", func() {
		It("should create a backup CRD", func() {
			b := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: localNonEncrypedBackup,
				},
				Spec: backupv1.BackupSpec{
					ResourceSetName: "rancher-resource-set-basic",
				},
			}
			o.Add(b)

			Expect(k8sClient.Create(testCtx, b)).To(Succeed())
			Eventually(Object(b)).Should(Exist())

		})

		Specify("the backup should be successful", func() {
			Eventually(func() error {
				return isBackupSuccessul(&backupv1.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name: localNonEncrypedBackup,
					},
				})
			}).Should(Succeed())
		})

		Specify("ensure collected metrics match expected", func() {

			Eventually(func() error {
				expected := formatBackupMetrics([]string{
					localNonEncrypedBackup,
				})

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_backup_count",
					"rancher_backups_attempted_total",
					"rancher_backups_failed_total",
				)
			}).Should(Succeed())
		})
	})

	When("we take an encrypted backup", func() {
		It("should be able to create an encrypted backup configuration", func() {
			b := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: localEncryptedBackup,
				},
				Spec: backupv1.BackupSpec{
					ResourceSetName:            "rancher-resource-set-basic",
					EncryptionConfigSecretName: encSecret,
				},
			}
			o.Add(b)

			Expect(k8sClient.Create(testCtx, b)).To(Succeed())
			By("verifying the backup resource exists")
			Eventually(Object(b)).Should(Exist())
		})

		Specify("the backup should be successful", func() {
			Eventually(func() error {
				return isBackupSuccessul(&backupv1.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name: localEncryptedBackup,
					},
				})
			}).Should(Succeed())
		})

		Specify("ensure collected metrics match expected", func() {

			Eventually(func() error {
				expected := formatBackupMetrics([]string{
					localNonEncrypedBackup,
					localEncryptedBackup,
				})

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_backup_count",
					"rancher_backups_attempted_total",
					"rancher_backups_failed_total",
				)
			}).Should(Succeed())
		})
	})

	When("we take a non-encrypted backup", func() {
		Specify("check that minio is accessible", func() {
			ctxCa, caT := context.WithTimeout(testCtx, time.Second*5)
			defer caT()
			_, err := minioClient.ListBuckets(ctxCa)
			Expect(err).To(Succeed())
		})

		Specify("create necessary buckets for backups", func() {
			ctxCa, caT := context.WithTimeout(testCtx, time.Second*10)
			defer caT()
			err := minioClient.MakeBucket(ctxCa, insecureBucket, minio.MakeBucketOptions{})
			Expect(err).To(Succeed())

			err = minioClient.MakeBucket(ctxCa, secureBucket, minio.MakeBucketOptions{})
			Expect(err).To(Succeed())

			err = minioClient.MakeBucket(ctxCa, recurringBucket, minio.MakeBucketOptions{})
			Expect(err).To(Succeed())

			Eventually(func() []string {
				ctxCa, caT := context.WithTimeout(testCtx, time.Second*5)
				defer caT()
				buckets, err := minioClient.ListBuckets(ctxCa)
				if err != nil {
					return []string{}
				}
				return lo.Map(buckets, func(b minio.BucketInfo, _ int) string {
					return b.Name
				})
			}).Should(ConsistOf([]string{secureBucket, insecureBucket, recurringBucket}))

		})

		Specify("we should be able to create the backup spec", func() {
			b := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: s3NonEncryptedBackup,
				},
				Spec: backupv1.BackupSpec{
					StorageLocation: &backupv1.StorageLocation{
						S3: &backupv1.S3ObjectStore{
							CredentialSecretName:      credentialSecretName,
							CredentialSecretNamespace: ts.ChartNamespace,
							BucketName:                insecureBucket,
							Endpoint:                  minioEndpoint,
							InsecureTLSSkipVerify:     true,
						},
					},
					ResourceSetName: "rancher-resource-set-basic",
				},
			}
			o.Add(b)
			err := k8sClient.Create(testCtx, b)
			Expect(err).To(Succeed())
			Eventually(Object(b)).Should(Exist())
		})

		Specify("the backup should be marked as successful", func() {
			Eventually(func() error {
				return isBackupSuccessul(&backupv1.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name: s3NonEncryptedBackup,
					},
				})
			}).Should(Succeed())
		})

		Specify("ensure collected metrics match expected", func() {

			Eventually(func() error {
				expected := formatBackupMetrics([]string{
					localNonEncrypedBackup,
					localEncryptedBackup,
					s3NonEncryptedBackup,
				})

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_backup_count",
					"rancher_backups_attempted_total",
					"rancher_backups_failed_total",
				)
			}).Should(Succeed())
		})

		Specify("the backup should exist in the remote store", func() {

			objs := minioClient.ListObjects(testCtx, insecureBucket, minio.ListObjectsOptions{})

			retObj := []minio.ObjectInfo{}
			for obj := range objs {
				retObj = append(retObj, obj)
			}
			keys := lo.Filter(
				lo.Map(retObj, func(info minio.ObjectInfo, _ int) string {
					return info.Key
				}), func(key string, _ int) bool {
					return strings.HasPrefix(key, s3NonEncryptedBackup)
				})
			Expect(keys).To(HaveLen(1))
		})
	})

	When("we take an encrypted backup", func() {
		Specify("it should successfully create the backup spec", func() {
			b := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: s3EncryptedBackup,
				},
				Spec: backupv1.BackupSpec{
					EncryptionConfigSecretName: encSecret,
					StorageLocation: &backupv1.StorageLocation{
						S3: &backupv1.S3ObjectStore{
							CredentialSecretName:      credentialSecretName,
							CredentialSecretNamespace: ts.ChartNamespace,
							BucketName:                secureBucket,
							Endpoint:                  minioEndpoint,
							InsecureTLSSkipVerify:     true,
						},
					},
					ResourceSetName: "rancher-resource-set-basic",
				},
			}
			o.Add(b)

			err := k8sClient.Create(testCtx, b)
			Expect(err).To(Succeed())
			Eventually(Object(b)).Should(Exist())
		})

		Specify("The backup should be marked as successful", func() {
			Eventually(func() error {
				return isBackupSuccessul(&backupv1.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name: s3EncryptedBackup,
					},
				})
			}).Should(Succeed())
		})

		Specify("ensure collected metrics match expected", func() {

			Eventually(func() error {
				expected := formatBackupMetrics([]string{
					localNonEncrypedBackup,
					localEncryptedBackup,
					s3NonEncryptedBackup,
					s3EncryptedBackup,
				})

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_backup_count",
					"rancher_backups_attempted_total",
					"rancher_backups_failed_total",
				)
			}).Should(Succeed())
		})

		Specify("the backup should exist in the remote store", func() {

			objs := minioClient.ListObjects(testCtx, secureBucket, minio.ListObjectsOptions{})

			retObj := []minio.ObjectInfo{}
			for obj := range objs {
				retObj = append(retObj, obj)
			}
			fmt.Fprintf(GinkgoWriter, "%d", len(retObj))
			keys := lo.Filter(
				lo.Map(retObj, func(info minio.ObjectInfo, _ int) string {
					return info.Key
				}), func(key string, _ int) bool {
					return strings.HasPrefix(key, s3EncryptedBackup)
				})
			Expect(keys).To(HaveLen(1))
		})
	})

	When("we taking a recurring backup", func() {

		Specify("It should create the backup spec", func() {
			recBackup := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: s3Recurring,
				},
				Spec: backupv1.BackupSpec{
					StorageLocation: &backupv1.StorageLocation{
						S3: &backupv1.S3ObjectStore{
							CredentialSecretName:      credentialSecretName,
							CredentialSecretNamespace: ts.ChartNamespace,
							BucketName:                recurringBucket,
							Endpoint:                  minioEndpoint,
							InsecureTLSSkipVerify:     true,
						},
					},
					ResourceSetName: "rancher-resource-set-basic",
					Schedule:        "@every 5s",
					RetentionCount:  2,
				},
			}
			o.Add(recBackup)

			Expect(k8sClient.Create(testCtx, recBackup)).To(Succeed())
			Eventually(Object(recBackup)).Should(Exist())
		})

		Specify("the backup should succeed", func() {
			Eventually(func() error {
				return isBackupSuccessul(&backupv1.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name: s3Recurring,
					},
				})
			}).Should(Succeed())
		})

		Specify("ensure collected metrics match expected", func() {

			Eventually(func() error {
				expected := formatBackupMetrics([]string{
					localNonEncrypedBackup,
					localEncryptedBackup,
					s3NonEncryptedBackup,
					s3EncryptedBackup,
					s3Recurring,
				})

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_backup_count",
					"rancher_backups_attempted_total",
					"rancher_backups_failed_total",
				)
			}).Should(Succeed())
		})

		Specify("we should eventually have two backups in the remote store", func() {
			ok, err := minioClient.BucketExists(testCtx, recurringBucket)
			Expect(err).To(Succeed())
			Expect(ok).To(BeTrue())
			Eventually(func() int {
				objs := minioClient.ListObjects(testCtx, recurringBucket, minio.ListObjectsOptions{})

				retObj := []minio.ObjectInfo{}
				for obj := range objs {
					retObj = append(retObj, obj)
				}
				keys := lo.Filter(
					lo.Map(retObj, func(info minio.ObjectInfo, _ int) string {
						return info.Key
					}), func(key string, _ int) bool {
						return strings.HasPrefix(key, s3Recurring)
					})
				return len(keys)
			}).Should(BeNumerically(">=", 2))
		})
	})

	When("we're done with all test backups", func() {
		Specify("we should eventually have the correct backup metadata metrics", func() {

			Eventually(func() error {
				var backups backupv1.BackupList

				Expect(k8sClient.List(testCtx, &backups)).To(Succeed())
				expected := formatBackupMetadataMetrics(backups.Items)

				return promtestutil.ScrapeAndCompare(metricsURL, strings.NewReader(expected),
					"rancher_backup_info",
				)
			}).Should(Succeed())
		})
	})

	When("we take a non-encrypted backup with a custom resource-set", func() {
		It("should create a backup CR", func() {
			b := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: localCustomResourceSetBackup,
				},
				Spec: backupv1.BackupSpec{
					ResourceSetName: "custom-resource-set",
					StorageLocation: &backupv1.StorageLocation{
						S3: &backupv1.S3ObjectStore{
							CredentialSecretName:      credentialSecretName,
							CredentialSecretNamespace: ts.ChartNamespace,
							BucketName:                insecureBucket,
							Endpoint:                  minioEndpoint,
							InsecureTLSSkipVerify:     true,
						},
					},
				},
			}
			o.Add(b)

			Expect(k8sClient.Create(testCtx, b)).To(Succeed())
			Eventually(Object(b)).Should(Exist())
		})

		Specify("the backup should be successful", func() {
			Eventually(func() error {
				return isBackupSuccessul(&backupv1.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name: localCustomResourceSetBackup,
					},
				})
			}).Should(Succeed())
		})

		Specify("ensure the backup contains the single secret", func() {
			Eventually(func() error {
				// Fetch the backup object from the cluster
				backup := &backupv1.Backup{}
				if err := k8sClient.Get(testCtx, client.ObjectKey{Name: localCustomResourceSetBackup}, backup); err != nil {
					return err
				}

				// Ensure the backup status has a filename
				if backup.Status.Filename == "" {
					return fmt.Errorf("backup filename is empty")
				}

				objs := minioClient.ListObjects(testCtx, insecureBucket, minio.ListObjectsOptions{})

				// convert the channel to a slice
				retObj := make([]minio.ObjectInfo, 2)
				for obj := range objs {
					retObj = append(retObj, obj)
				}

				// filter the slice to only include the keys that contain "custom-resource-set-backup"
				keys := lo.FilterMap(retObj, func(info minio.ObjectInfo, i int) (string, bool) {
					return info.Key, strings.Contains(info.Key, "custom-resource-set-backup")
				})
				if len(keys) == 0 {
					return fmt.Errorf("no appropriate backup found in bucket %s", insecureBucket)
				}

				contents, err := extractTarballContents(minioClient, insecureBucket, keys[0])
				if err != nil {
					return fmt.Errorf("failed to extract tarball contents: %s", err)
				}

				// we backed up the docker-config secret
				Expect(contents).To(HaveKey("secrets.#v1/default/docker-config-json.json"))
				// but not the regular secret
				Expect(contents).NotTo(HaveKey("secrets.#v1/default/regular.json"))

				return nil
			}).Should(Succeed())
		})
	})

})

var _ = Describe("Backup e2e local driver", Ordered, Label("integration"), func() {
	var o *ObjectTracker
	BeforeAll(func() {
		o = &ObjectTracker{
			arr: []client.Object{},
			mu:  sync.Mutex{},
		}
		DeferCleanup(func() {
			o.DeleteAll()
		})

		By("creating a generic secret for encryption configuration")
		payload := test.Data("encryption.yaml")

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      encSecret,
				Namespace: ts.ChartNamespace,
			},
			Data: map[string][]byte{
				encryptionconfig.EncryptionProviderConfigKey: payload,
			},
		}
		o.Add(secret)

		Expect(k8sClient.Create(testCtx, secret)).To(Succeed())
		Eventually(secret).Should(Exist())
	})
})
