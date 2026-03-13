package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/rancher/backup-restore-operator/cmd/tool/internal/cmd/resourcesetcheck"
	"github.com/rancher/backup-restore-operator/cmd/tool/internal/cmd/resourcesetview"
	"github.com/rancher/backup-restore-operator/pkg/version"
	"github.com/sirupsen/logrus"
)

var (
	Debug        bool
	PrintVersion bool
)

func init() {
	flag.BoolVar(&Debug, "debug", false, "Enable debug logging.")
	flag.BoolVar(&PrintVersion, "version", false, "Print version information and exit.")
	flag.Usage = usage
}

func usage() {
	fmt.Fprintf(os.Stderr, "bro-tool — CLI helper for the backup-restore-operator.\n")
	fmt.Fprintf(os.Stderr, "Versioned alongside BRO; see BRO docs for compatibility notes.\n\n")
	fmt.Fprintf(os.Stderr, "Usage: bro-tool [flags] <command> [command flags]\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  resource-set:view   View the ResourceSets defined by a BRO helm chart.\n")
	fmt.Fprintf(os.Stderr, "  resource-set:check  Check whether a resource would be covered by any ResourceSet rule.\n\n")
	fmt.Fprintf(os.Stderr, "Flags:\n")
	flag.PrintDefaults()
}

func main() {
	flag.Parse()

	if PrintVersion {
		fmt.Println(version.FmtVersionInfo("bro-tool"))
		return
	}

	if Debug {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.Debugf("Loglevel set to [%v]", logrus.DebugLevel)
	}

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return
	}

	var err error
	switch args[0] {
	case "resource-set:view":
		err = resourcesetview.Run(args[1:])
	case "resource-set:check":
		err = resourcesetcheck.Run(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", args[0])
		flag.Usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
