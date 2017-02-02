package command

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

	log "github.com/Sirupsen/logrus"

	"github.com/NetSys/quilt/api/client"
	"github.com/NetSys/quilt/api/client/getter"
	"github.com/NetSys/quilt/db"
)

// SSH contains the options for SSHing into machines.
type SSH struct {
	targetMachine string
	sshArgs       []string

	common       *commonFlags
	clientGetter client.Getter
}

// NewSSHCommand creates a new SSH command instance.
func NewSSHCommand() *SSH {
	return &SSH{
		clientGetter: getter.New(),
		common:       &commonFlags{},
	}
}

// InstallFlags sets up parsing for command line flags.
func (sCmd *SSH) InstallFlags(flags *flag.FlagSet) {
	sCmd.common.InstallFlags(flags)

	flags.Usage = func() {
		fmt.Println("usage: quilt ssh [-H=<daemon_host>] <machine_num> " +
			"[ssh_options]")
		fmt.Println("`ssh` creates a SSH session to the specified machine. " +
			"The machine is identified the database ID produced by " +
			"`quilt queryMachines`.")
		fmt.Println("For example, to SSH to machine 5 with a specific " +
			"private key: quilt ssh 5 -i ~/.ssh/quilt")
		flags.PrintDefaults()
	}
}

// Parse parses the command line arguments for the ssh command.
func (sCmd *SSH) Parse(args []string) error {
	if len(args) == 0 {
		return errors.New("must specify a target machine")
	}

	sCmd.targetMachine = args[0]
	sCmd.sshArgs = args[1:]
	return nil
}

// Run SSHs into the given machine.
func (sCmd *SSH) Run() int {
	c, err := sCmd.clientGetter.Client(sCmd.common.host)
	if err != nil {
		log.Error(err)
		return 1
	}
	defer c.Close()

	tgtMach, err := getMachine(c, sCmd.targetMachine)
	if err != nil {
		log.WithError(err).Error("Unable to find machine")
		return 1
	}

	if err = runSSHCommand(tgtMach.PublicIP, sCmd.sshArgs).Run(); err != nil {
		log.WithError(err).Error("Error executing the SSH command")
		return 1
	}
	return 0
}

func getMachine(c client.Client, id string) (db.Machine, error) {
	machines, err := c.QueryMachines()
	if err != nil {
		return db.Machine{}, err
	}

	var choice *db.Machine
	for _, m := range machines {
		if len(id) > len(m.StitchID) || m.StitchID[:len(id)] != id {
			continue
		}
		if choice != nil {
			return db.Machine{}, fmt.Errorf("ambiguous stitchIDs %s and %s",
				choice.StitchID, m.StitchID)
		}
		copy := m
		choice = &copy
	}

	if choice == nil {
		return db.Machine{}, fmt.Errorf("no machine with stitchID %q", id)
	}

	return *choice, nil
}

// Stored in a variable so we can mock it out for unit tests.
var runSSHCommand = func(host string, args []string) *exec.Cmd {
	baseArgs := []string{fmt.Sprintf("quilt@%s", host),
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}

	cmd := exec.Command("ssh", append(baseArgs, args...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}
