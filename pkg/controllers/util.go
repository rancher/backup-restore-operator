package controllers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	v1core "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/server/options/encryptionconfig"
	"k8s.io/apiserver/pkg/storage/value"
)

const (
	ChartNamespace = "cattle-resources-system"
	WorkerThreads  = 25
)

func CreateTarAndGzip(backupPath, targetGzipPath, targetGzipFile string) error {
	gzipFile, err := os.Create(filepath.Join(targetGzipPath, targetGzipFile))
	if err != nil {
		return fmt.Errorf("error creating backup tar gzip file: %v", err)
	}
	// writes to gw will be compressed and written to gzipFile
	gw := gzip.NewWriter(gzipFile)
	defer gw.Close()
	// writes to tw will be written to gw
	tw := tar.NewWriter(gw)
	defer tw.Close()
	walkFunc := func(currPath string, info os.FileInfo, err error) error {
		if currPath == backupPath {
			return nil
		}
		if err != nil {
			return fmt.Errorf("error in walkFunc for %v: %v", currPath, err)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("error creating header for %v: %v", info.Name(), err)
		}
		relativePath, err := filepath.Rel(backupPath, currPath)
		if err != nil {
			return fmt.Errorf("error getting relative path for %v: %v", info.Name(), err)
		}
		hdr.Name = filepath.Join(relativePath)
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("error writing header for %v: %v", info.Name(), err)
		}
		if info.IsDir() {
			return nil
		}
		fInfo, err := os.Open(currPath)
		if err != nil {
			return fmt.Errorf("error opening %v: %v", info.Name(), err)
		}
		if _, err := io.Copy(tw, fInfo); err != nil {
			return fmt.Errorf("error copying %v: %v", info.Name(), err)
		}
		fInfo.Close()
		return nil
	}
	if err := filepath.Walk(backupPath, walkFunc); err != nil {
		return err
	}

	return nil
}

func GetEncryptionTransformers(encryptionConfigSecretName string, secrets v1core.SecretController) (map[schema.GroupResource]value.Transformer, error) {
	var transformerMap map[schema.GroupResource]value.Transformer
	// EncryptionConfig secret ns is hardcoded to ns of controller in chart's ns
	// kubectl create secret generic test-encryptionconfig --from-file=./encryptionConfig.yaml
	// TODO: confirm the chart's ns
	fmt.Printf("\nencryptionConfigSecretName: %v\n", encryptionConfigSecretName)
	encryptionConfigSecret, err := secrets.Get(ChartNamespace, encryptionConfigSecretName, k8sv1.GetOptions{})
	if err != nil {
		return transformerMap, err
	}
	for fileName, encryptionConfigBytes := range encryptionConfigSecret.Data {
		logrus.Infof("Using file %v for encryptionConfig", fileName)
		return encryptionconfig.ParseEncryptionConfiguration(bytes.NewReader(encryptionConfigBytes))
	}
	return transformerMap, fmt.Errorf("no encryptionConfig provided")
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

//func (h *handler) createFromDependencyGraph(ownerToDependentsList map[string][]restoreObj, created map[string]bool,
//	numOwnerReferences map[string]int, toRestore []restoreObj) error {
//	//var errgrp errgroup.Group
//	numTotalDependents := 0
//	for _, dependents := range ownerToDependentsList {
//		numTotalDependents += len(dependents)
//	}
//	//totalToRestore := numTotalDependents + len(toRestore)
//	countRestored := 0

//resourcesToRestoreQueue := util.GetObjectQueue(toRestore, totalToRestore)
//for w := 0; w < 50; w++ {
//	errgrp.Go(func() error {
//		var errList []error
//		for currObj := range resourcesToRestoreQueue {
//			curr := currObj.(restoreObj)
//			if created[curr.ResourceConfigPath] {
//				continue
//			}
//			err := h.restoreResource(curr, curr.GVR)
//			if err != nil {
//				logrus.Errorf("error %v restoring resource %v", err, curr.Name)
//				errList = append(errList, err)
//				continue
//			}
//			for _, dependent := range ownerToDependentsList[curr.ResourceConfigPath] {
//				// example, curr = catTemplate, dependent=catTempVer
//				if numOwnerReferences[dependent.ResourceConfigPath] > 0 {
//					numOwnerReferences[dependent.ResourceConfigPath]--
//				}
//				if numOwnerReferences[dependent.ResourceConfigPath] == 0 {
//					logrus.Infof("dependent %v is now ready to create", dependent.Name)
//					fmt.Printf("\nlen of resourcesToRestoreQueue before adding dep: %v\n", len(resourcesToRestoreQueue))
//					resourcesToRestoreQueue <- dependent
//					logrus.Infof("added dependent to channel")
//				}
//			}
//			if len(resourcesToRestoreQueue) == 0 {
//				logrus.Infof("Time after everything's started to process: %v", time.Now())
//			}
//			created[curr.ResourceConfigPath] = true
//			countRestored++
//		}
//		return util.ErrList(errList)
//	})
//}
//fmt.Printf("\nHere after all goroutines have finished\n")
//close(resourcesToRestoreQueue)
//err := errgrp.Wait()
//if err != nil {
//	return err
//}
//}
