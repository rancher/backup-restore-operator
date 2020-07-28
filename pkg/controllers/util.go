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
	// writes to gw will be compressed and written to gzipFile
	gw := gzip.NewWriter(gzipFile)
	defer gw.Close()
	// writes to tw will be written to gw
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

// https://medium.com/@skdomino/taring-untaring-files-in-go-6b07cf56bc07
func LoadFromTarGzip(tarGzFilePath string) error {
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
			if _, err := os.Stat(tarContent.Name); err != nil {
				if os.IsNotExist(err) {
					err := os.Mkdir(tarContent.Name, os.ModePerm)
					if err != nil {
						return fmt.Errorf("error creating dir %v", err)
					}
				}
			}
		} else if tarContent.Typeflag == tar.TypeReg {
			file, err := os.OpenFile(tarContent.Name, os.O_CREATE|os.O_RDWR, os.FileMode(tarContent.Mode))
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
