package test

import (
	"embed"
	"io/fs"
	"path"
)

var (
	//go:embed data
	TestDataFS embed.FS
)

func TestData(filename string) []byte {
	data, err := fs.ReadFile(TestDataFS, path.Join("data", filename))
	if err != nil {
		panic(err)
	}
	return data
}
