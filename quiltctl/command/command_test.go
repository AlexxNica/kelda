package command

import (
	"errors"
	"flag"
	"reflect"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/NetSys/quilt/api"
	clientMock "github.com/NetSys/quilt/api/client/mocks"
	"github.com/NetSys/quilt/db"
	"github.com/NetSys/quilt/quiltctl/testutils"
)

func TestMachineFlags(t *testing.T) {
	t.Parallel()

	expHost := "IP"

	machineCmd := NewMachineCommand()
	err := parseHelper(machineCmd, []string{"-H", expHost})

	if err != nil {
		t.Errorf("Unexpected error when parsing machine args: %s", err.Error())
		return
	}

	if machineCmd.host != expHost {
		t.Errorf("Expected machine command to parse arg %s, but got %s",
			expHost, machineCmd.host)
	}
}

func TestMachineOutput(t *testing.T) {
	t.Parallel()

	res := machinesStr([]db.Machine{{
		ID:       1,
		Role:     db.Master,
		Provider: "Amazon",
		Region:   "us-west-1",
		Size:     "m4.large",
		PublicIP: "8.8.8.8",
	}})

	exp := "Machine-1{Master, Amazon us-west-1 m4.large, PublicIP=8.8.8.8}\n"
	if res != exp {
		t.Errorf("\nGot: %s\nExp: %s\n", res, exp)
	}
}

func TestContainerFlags(t *testing.T) {
	t.Parallel()

	expHost := "IP"

	containerCmd := NewContainerCommand()
	err := parseHelper(containerCmd, []string{"-H", expHost})

	if err != nil {
		t.Errorf("Unexpected error when parsing container args: %s", err.Error())
		return
	}

	if containerCmd.host != expHost {
		t.Errorf("Expected container command to parse arg %s, but got %s",
			expHost, containerCmd.host)
	}
}

func TestContainerOutput(t *testing.T) {
	t.Parallel()

	res := containersStr([]db.Container{{ID: 1, Command: []string{"cmd", "arg"}}})
	exp := "Container-1{run  cmd arg}\n"
	if res != exp {
		t.Errorf("Expected container command to print %s, but got %s.", exp, res)
	}
}

func checkGetParsing(t *testing.T, args []string, expImport string, expErr error) {
	getCmd := &Get{}
	err := parseHelper(getCmd, args)

	if expErr != nil {
		if err.Error() != expErr.Error() {
			t.Errorf("Expected error %s, but got %s",
				expErr.Error(), err.Error())
		}
		return
	}

	if err != nil {
		t.Errorf("Unexpected error when parsing get args: %s", err.Error())
		return
	}

	if getCmd.importPath != expImport {
		t.Errorf("Expected get command to parse arg %s, but got %s",
			expImport, getCmd.importPath)
	}
}

func TestGetFlags(t *testing.T) {
	t.Parallel()

	expImport := "spec"
	checkGetParsing(t, []string{"-import", expImport}, expImport, nil)
	checkGetParsing(t, []string{expImport}, expImport, nil)
	checkGetParsing(t, []string{}, "", errors.New("no import specified"))
}

func checkStopParsing(t *testing.T, args []string, expNamespace string, expErr error) {
	stopCmd := NewStopCommand()
	err := parseHelper(stopCmd, args)

	if expErr != nil {
		if err.Error() != expErr.Error() {
			t.Errorf("Expected error %s, but got %s",
				expErr.Error(), err.Error())
		}
		return
	}

	if err != nil {
		t.Errorf("Unexpected error when parsing stop args: %s", err.Error())
		return
	}

	if stopCmd.namespace != expNamespace {
		t.Errorf("Expected stop command to parse arg %s, but got %s",
			expNamespace, stopCmd.namespace)
	}
}

func TestStopFlags(t *testing.T) {
	t.Parallel()

	expNamespace := "namespace"
	checkStopParsing(t, []string{"-namespace", expNamespace}, expNamespace, nil)
	checkStopParsing(t, []string{expNamespace}, expNamespace, nil)
	checkStopParsing(t, []string{}, defaultNamespace, nil)
}

func checkSSHParsing(t *testing.T, args []string, expMachine int,
	expSSHArgs []string, expErr error) {

	sshCmd := NewSSHCommand()
	err := parseHelper(sshCmd, args)

	if expErr != nil {
		if err.Error() != expErr.Error() {
			t.Errorf("Expected error %s, but got %s",
				expErr.Error(), err.Error())
		}
		return
	}

	if err != nil {
		t.Errorf("Unexpected error when parsing ssh args: %s", err.Error())
		return
	}

	if sshCmd.targetMachine != expMachine {
		t.Errorf("Expected ssh command to parse target machine %d, but got %d",
			expMachine, sshCmd.targetMachine)
	}

	if !reflect.DeepEqual(sshCmd.sshArgs, expSSHArgs) {
		t.Errorf("Expected ssh command to parse SSH args %v, but got %v",
			expSSHArgs, sshCmd.sshArgs)
	}
}

func TestSSHFlags(t *testing.T) {
	t.Parallel()

	checkSSHParsing(t, []string{"1"}, 1, []string{}, nil)
	sshArgs := []string{"-i", "~/.ssh/key"}
	checkSSHParsing(t, append([]string{"1"}, sshArgs...), 1, sshArgs, nil)
	checkSSHParsing(t, []string{}, 0, nil,
		errors.New("must specify a target machine"))
}

func checkExecParsing(t *testing.T, args []string, expContainer int,
	expKey string, expCmd string, expErr error) {

	execCmd := NewExecCommand(nil)
	err := parseHelper(execCmd, args)

	if expErr != nil {
		if err.Error() != expErr.Error() {
			t.Errorf("Expected error %s, but got %s",
				expErr.Error(), err.Error())
		}
		return
	}

	if err != nil {
		t.Errorf("Unexpected error when parsing exec args: %s", err.Error())
		return
	}

	if execCmd.targetContainer != expContainer {
		t.Errorf("Expected exec command to parse target container %d, but got %d",
			expContainer, execCmd.targetContainer)
	}

	if execCmd.command != expCmd {
		t.Errorf("Expected exec command to parse command %s, but got %s",
			expCmd, execCmd.command)
	}

	if execCmd.privateKey != expKey {
		t.Errorf("Expected exec command to parse private key %s, but got %s",
			expKey, execCmd.privateKey)
	}
}

func TestExecFlags(t *testing.T) {
	t.Parallel()

	checkExecParsing(t, []string{"1", "sh"}, 1, "", "sh", nil)
	checkExecParsing(t, []string{"-i", "key", "1", "sh"}, 1, "key", "sh", nil)
	checkExecParsing(t, []string{"1", "cat /etc/hosts"}, 1, "",
		"cat /etc/hosts", nil)
	checkExecParsing(t, []string{"1"}, 0, "", "",
		errors.New("must specify a target container and command"))
	checkExecParsing(t, []string{}, 0, "", "",
		errors.New("must specify a target container and command"))
}

func TestStopNamespace(t *testing.T) {
	t.Parallel()

	mockGetter := new(testutils.Getter)
	c := &clientMock.Client{}
	mockGetter.On("Client", mock.Anything).Return(c, nil)

	stopCmd := NewStopCommand()
	stopCmd.clientGetter = mockGetter
	stopCmd.namespace = "namespace"
	stopCmd.Run()
	expStitch := `{"namespace": "namespace"}`
	if c.DeployArg != expStitch {
		t.Error("stop command invoked Quilt with the wrong stitch")
	}
}

func TestSSHCommandCreation(t *testing.T) {
	t.Parallel()

	exp := []string{"ssh", "quilt@host", "-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null", "-i", "~/.ssh/quilt"}
	res := runSSHCommand("host", []string{"-i", "~/.ssh/quilt"})
	if !reflect.DeepEqual(res.Args, exp) {
		t.Errorf("Bad SSH command creation: expected %v, got %v.", exp, res.Args)
	}
}

func TestExec(t *testing.T) {
	t.Parallel()

	workerHost := "worker"
	targetContainer := 1

	mockGetter := new(testutils.Getter)
	mockGetter.On("Client", mock.Anything).Return(&clientMock.Client{}, nil)
	mockGetter.On("ContainerClient", mock.Anything, mock.Anything).Return(
		&clientMock.Client{
			ContainerReturn: []db.Container{
				{
					StitchID: targetContainer,
					DockerID: "foo",
				},
			},
			HostReturn: workerHost,
		}, nil)

	mockSSHClient := new(testutils.MockSSHClient)
	execCmd := Exec{
		privateKey:      "key",
		command:         "cat /etc/hosts",
		targetContainer: targetContainer,
		SSHClient:       mockSSHClient,
		clientGetter:    mockGetter,
		common: &commonFlags{
			host: api.DefaultSocket,
		},
	}

	mockSSHClient.On("Connect", workerHost, "key").Return(nil)
	mockSSHClient.On("RequestPTY").Return(nil)
	mockSSHClient.On("Run", "docker exec -it foo cat /etc/hosts").Return(nil)
	mockSSHClient.On("Disconnect").Return(nil)

	execCmd.Run()

	mockSSHClient.AssertExpectations(t)
}

func parseHelper(cmd SubCommand, args []string) error {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	cmd.InstallFlags(flags)
	flags.Parse(args)
	return cmd.Parse(flags.Args())
}
