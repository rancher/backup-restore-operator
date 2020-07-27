package controllers

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	BackupBaseDir = "baseBackup"
)

func CreateTarAndGzip(backupPath, targetGzipPath, targetGzipFile string) error {
	gzipFile, err := os.Create(filepath.Join(targetGzipPath, targetGzipFile))
	if err != nil {
		return fmt.Errorf("error creating backup tar gzip file: %v", err)
	}
	gw := gzip.NewWriter(gzipFile)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()
	walkFunc := func(path string, info os.FileInfo, err error) error {
		if info.Name() == BackupBaseDir {
			return nil
		}
		if err != nil {
			return fmt.Errorf("error in walkFunc for %v: %v", path, err)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("error creating header for %v: %v", info.Name(), err)
		}
		hdr.Name = path
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("error writing header for %v: %v", info.Name(), err)
		}
		if info.IsDir() {
			return nil
		}
		fInfo, err := os.Open(path)
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
