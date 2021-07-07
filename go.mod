module github.com/rancher/backup-restore-operator

go 1.16

replace k8s.io/client-go => k8s.io/client-go v0.21.2

require (
	github.com/minio/minio-go/v6 v6.0.57
	github.com/rancher/lasso v0.0.0-20200515155337-a34e1e26ad91
	github.com/rancher/wrangler v0.6.2-0.20200622171942-7224e49a2407
	github.com/rancher/wrangler-api v0.6.1-0.20200515193802-dcf70881b087
	github.com/robfig/cron v1.2.0
	github.com/sirupsen/logrus v1.5.0
	golang.org/x/sync v0.0.0-20201020160332-67f06af15bc9
	k8s.io/api v0.21.2
	k8s.io/apiextensions-apiserver v0.18.0
	k8s.io/apimachinery v0.21.2
	k8s.io/apiserver v0.18.0
	k8s.io/client-go v0.18.0
	k8s.io/utils v0.0.0-20201110183641-67b214c5f920
)
