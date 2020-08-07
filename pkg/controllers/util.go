package controllers

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
)

const WorkerThreads = 25

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
		if info.IsDir() {
			hdr.Name = filepath.Base(currPath)
			if err := tw.WriteHeader(hdr); err != nil {
				return fmt.Errorf("error writing header for %v: %v", info.Name(), err)
			}
			return nil
		}
		// for example, for /var/tmp/authconfigs.management.cattle.io#v3/adfs.json,
		// containingDirFullPath = /var/tmp/authconfigs.management.cattle.io#v3
		containingDirFullPath := path.Dir(currPath)
		// containingDirBasePath = authconfigs.management.cattle.io#v3
		containingDirBasePath := filepath.Base(containingDirFullPath)
		hdr.Name = filepath.Join(containingDirBasePath, filepath.Base(currPath))
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

// https://medium.com/@skdomino/taring-untaring-files-in-go-6b07cf56bc07
func LoadFromTarGzip(tarGzFilePath, tmpBackupPath string) error {
	r, err := os.Open(tarGzFilePath)
	if err != nil {
		return fmt.Errorf("error opening tar.gz backup fike %v", err)
	}

	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tarball := tar.NewReader(gz)

	for {
		tarContent, err := tarball.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if tarContent.Typeflag == tar.TypeDir {
			if _, err := os.Stat(filepath.Join(tmpBackupPath, tarContent.Name)); err != nil {
				if os.IsNotExist(err) {
					err := os.Mkdir(filepath.Join(tmpBackupPath, tarContent.Name), os.ModePerm)
					if err != nil {
						return fmt.Errorf("error creating dir %v", err)

					}
				}
			}
		} else if tarContent.Typeflag == tar.TypeReg {
			file, err := os.OpenFile(filepath.Join(tmpBackupPath, tarContent.Name), os.O_CREATE|os.O_RDWR, os.FileMode(tarContent.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, tarball); err != nil {
				return err
			}

			file.Close()
		}
	}
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
