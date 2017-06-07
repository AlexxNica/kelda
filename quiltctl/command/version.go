package command

import (
	"flag"
	"fmt"

	"github.com/quilt/quilt/version"

	log "github.com/Sirupsen/logrus"
)

// Version prints the Quilt version information.
type Version struct {
	*connectionHelper
}

// NewVersionCommand creates a new Version command instance.
func NewVersionCommand() *Version {
	return &Version{
		connectionHelper: &connectionHelper{},
	}
}

var versionUsage = `usage: quilt version [-H=<daemon_host>]
Show the Quilt version information.`

// InstallFlags sets up parsing for command line flags.
func (vCmd *Version) InstallFlags(flags *flag.FlagSet) {
	vCmd.connectionHelper.InstallFlags(flags)
	flags.Usage = func() {
		fmt.Println(versionUsage)
	}
}

// Parse parses the command line arguments for the version command.
func (vCmd *Version) Parse(args []string) error {
	return nil
}

// Run prints the version information.
func (vCmd *Version) Run() int {
	fmt.Println("Client:", version.Version)

	daemonVersion, err := vCmd.client.Version()
	if err != nil {
		log.WithError(err).Error("Failed to get daemon version")
		return 1
	}
	fmt.Println("Daemon:", daemonVersion)

	return 0
}
