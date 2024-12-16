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
	"github.com/rancher/backup-restore-operator/e2e/test"
	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	preserveFieldsBackup   = "preserve-fields.tar.gz"
	deletionGraceBackup    = "deletion-grace-backup.tar.gz"
	encryptedRestoreBackup = "encrypted-resources.tar.gz"
)

func isRestoreSuccessful(b *backupv1.Restore) error {
	bD, err := Object(b)()
	if err != nil {
		return err
	}

	if !condition.Cond(backupv1.RestoreConditionReady).IsTrue(bD) {
		message := condition.Cond(backupv1.RestoreConditionReady).GetMessage(bD)
		return fmt.Errorf("backup %s did not upload %s", b.Name, message)
	}

	message := strings.ToLower(strings.TrimSpace(condition.Cond(backupv1.RestoreConditionReady).GetMessage(bD)))
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

			By("uploading the preserve-unkown-fields backup")
			ctxCa, caT := context.WithTimeout(testCtx, 10*time.Second)
			defer caT()
			preserveData := test.TestData("restore/preserve-unknown-fields.tar.gz")
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
			deleteData := test.TestData("restore/deletion-grace-period-seconds.tar.gz")
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
			for _ = range objectInfo {
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
	})

	When("we restore from an encrypted backup", func() {
		It("should upload the test files to the remote store", func() {
			Expect(minioClient.MakeBucket(testCtx, secureBucket, minio.MakeBucketOptions{})).To(Succeed())

			By("uploading the preserve-unkown-fields backup")
			ctxCa, caT := context.WithTimeout(testCtx, 10*time.Second)
			defer caT()
			encryptData := test.TestData("restore/encrypted-resources.tar.gz")
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
			for _ = range objectInfo {
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
	})
})

// TODO : left as an exercise to the reader
var _ = Describe("Restore from local driver", Ordered, Label("integration"), func() {
	BeforeAll(func() {

	})
})
