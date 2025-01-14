package util

import (
	"context"
	"fmt"
	"reflect"
)

const (
	WorkerThreads = 25
	S3Backup      = "S3"
	PVBackup      = "PV"
)

var (
	chartNamespace string
	devMode        bool
)

var (
	initNs  = Initializer{}
	initDev = Initializer{}
)

func SetDevMode(enabled bool) {
	initDev.InitOnce(func() {
		devMode = enabled
	})
}

func DevMode() bool {
	initDev.WaitForInit()
	return devMode
}

func DevModeContext(ctx context.Context) bool {
	err := initDev.WaitForInitContext(ctx)
	if err != nil {
		return false
	}
	return devMode
}

func GetChartNamespaceContext(ctx context.Context) (string, error) {
	if err := initNs.WaitForInitContext(ctx); err != nil {
		return "", err
	}
	return chartNamespace, nil
}

func GetChartNamespace() string {
	initNs.WaitForInit()
	return chartNamespace
}

func SetChartNamespace(ns string) {
	initNs.InitOnce(func() {
		chartNamespace = ns
	})
}

func GetObjectQueue(l interface{}, capacity int) chan interface{} {
	s := reflect.ValueOf(l)
	c := make(chan interface{}, capacity)

	for i := 0; i < s.Len(); i++ {
		c <- s.Index(i).Interface()
	}
	return c
}

func ErrList(e []error) error {
	if len(e) > 0 {
		return fmt.Errorf("%v", e)
	}
	return nil
}
