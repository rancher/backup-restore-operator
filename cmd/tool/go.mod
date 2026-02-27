module github.com/rancher/backup-restore-operator/cmd/tool

go 1.24.0

toolchain go1.24.4

replace github.com/rancher/backup-restore-operator => ../../

require (
	github.com/rancher/backup-restore-operator v0.0.0
	github.com/sirupsen/logrus v1.9.3
)

require golang.org/x/sys v0.36.0 // indirect
