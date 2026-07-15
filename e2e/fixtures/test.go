package fixtures

import (
	"embed"
	"io/fs"
	"path"
)

var (
	//go:embed testdata
	TestDataFS embed.FS
)

func Data(filename string) []byte {
	data, err := fs.ReadFile(TestDataFS, path.Join("testdata", filename))
	if err != nil {
		panic(err)
	}
	return data
}
