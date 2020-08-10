module github.com/rancher/backup-restore-operator

go 1.13

replace k8s.io/client-go => k8s.io/client-go v0.18.0

require (
	github.com/minio/minio-go/v6 v6.0.57
	github.com/mrajashree/backup v0.0.0-20200810051511-8ea9b0797cf7
	github.com/rancher/lasso v0.0.0-20200515155337-a34e1e26ad91
	github.com/rancher/wrangler v0.6.2-0.20200622171942-7224e49a2407
	github.com/rancher/wrangler-api v0.6.1-0.20200515193802-dcf70881b087
	github.com/robfig/cron v1.2.0
	github.com/sirupsen/logrus v1.5.0
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	k8s.io/apiextensions-apiserver v0.18.0
	k8s.io/apimachinery v0.18.0
	k8s.io/apiserver v0.18.0
	k8s.io/client-go v0.18.0
)
