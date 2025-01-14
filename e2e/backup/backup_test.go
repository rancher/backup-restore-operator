package backup_test

import (
	"context"
	"fmt"
	"github.com/rancher/backup-restore-operator/pkg/util/encryptionconfig"
	"strings"
	"sync"
	"time"

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
	encSecret                = "encryption-config"
	nonEncrypedBackup        = "local-driver-non-encrypted"
	encryptedBackup          = "local-driver-encrypted"
	localCattleDevDriverPath = "../../backups"

	insecureBucket  = "rancherbackups-insecure"
	secureBucket    = "rancherbackups-secure"
	recurringBucket = "rancherbackups-recurring"
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
					Name: "s3-insecure",
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
						Name: "s3-insecure",
					},
				})
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
					return strings.HasPrefix(key, "s3-insecure")
				})
			Expect(keys).To(HaveLen(1))
		})
	})

	When("we take an encrypted backup", func() {
		Specify("it should successfully create the backup spec", func() {
			b := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "s3-secure",
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
						Name: "s3-secure",
					},
				})
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
					return strings.HasPrefix(key, "s3-secure")
				})
			Expect(keys).To(HaveLen(1))
		})
	})

	When("we taking a recurring backup", func() {

		Specify("It should create the backup spec", func() {
			recBackup := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "s3-recurring",
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

		Specify("the backup should succced", func() {
			Eventually(func() error {
				return isBackupSuccessul(&backupv1.Backup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "s3-recurring",
					},
				})
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
						return strings.HasPrefix(key, "s3-recurring")
					})
				return len(keys)
			}).Should(BeNumerically(">=", 2))

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

	When("we take a non-encrypted backup", func() {
		It("should create a backup CRD", func() {
			b := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: nonEncrypedBackup,
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
						Name: nonEncrypedBackup,
					},
				})
			}).Should(Succeed())

		})
	})

	When("we take an encrypted backup", func() {
		It("should be able to create an encrypted backup configuration", func() {
			b := &backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: encryptedBackup,
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
						Name: encryptedBackup,
					},
				})
			}).Should(Succeed())
		})
	})
})
