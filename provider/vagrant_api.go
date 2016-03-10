package provider

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"strings"
)

var vagrantCmd = "vagrant"
var shCmd = "sh"

type vagrantAPI struct{}

func newVagrantAPI() vagrantAPI {
	vagrant := vagrantAPI{}
	return vagrant
}

func (api vagrantAPI) Init(cloudConfig string, id string) error {
	_, err := os.Stat(api.VagrantDir())
	if os.IsNotExist(err) {
		os.Mkdir(api.VagrantDir(), os.ModeDir|os.ModePerm)
	}
	path := api.VagrantDir() + id
	os.Mkdir(path, os.ModeDir|os.ModePerm)

	_, err = api.Shell(id, `vagrant --machine-readable init coreos-beta`)
	if err != nil {
		api.Destroy(id)
		return err
	}

	err = ioutil.WriteFile(path+"/user-data", []byte(cloudConfig), 0644)
	if err != nil {
		api.Destroy(id)
		return err
	}

	vagrant := vagrantFile()
	err = ioutil.WriteFile(path+"/vagrantFile", []byte(vagrant), 0644)
	if err != nil {
		api.Destroy(id)
		return err
	}

	err = ioutil.WriteFile(path+"/size", []byte(size), 0644)
	if err != nil {
		api.Destroy(id)
		return err
	}

	return nil
}

func (api vagrantAPI) Up(id string) error {
	_, err := api.Shell(id, `vagrant --machine-readable up`)
	if err != nil {
		return err
	}
	return nil
}

func (api vagrantAPI) Destroy(id string) error {
	_, err := api.Shell(id, `vagrant --machine-readable destroy -f; cd ../; rm -rf %s`)
	if err != nil {
		return err
	}
	return nil
}

func (api vagrantAPI) PublicIP(id string) (string, error) {
	ip, err := api.Shell(id, `vagrant ssh -c "ip address show eth1 | grep 'inet ' | sed -e 's/^.*inet //' -e 's/\/.*$//' | tr -d '\n'"`)
	if err != nil {
		return "", err
	}
	return string(ip[:]), nil
}

func (api vagrantAPI) Status(id string) (string, error) {
	output, err := api.Shell(id, `vagrant --machine-readable status`)
	if err != nil {
		return "", err
	}
	lines := bytes.Split(output, []byte("\n"))
	for _, line := range lines {
		words := strings.Split(string(line[:]), ",")
		if len(words) >= 4 {
			if strings.Compare(words[2], "state") == 0 {
				return words[3], nil
			}
		}
	}
	return "", nil
}

func (api vagrantAPI) List() ([]string, error) {
	subdirs := []string{}
	_, err := os.Stat(api.VagrantDir())
	if os.IsNotExist(err) {
		return subdirs, nil
	}

	files, err := ioutil.ReadDir(api.VagrantDir())
	if err != nil {
		return subdirs, err
	}
	for _, file := range files {
		subdirs = append(subdirs, file.Name())
	}
	return subdirs, nil
}

func (api vagrantAPI) AddBox(name string, provider string) error {
	/* Adding a box fails if it already exists, hence the check. */
	exists, err := api.ContainsBox(name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	err = exec.Command(vagrantCmd, []string{"--machine-readable", "box", "add", "--provider", provider, name}...).Run()
	if err != nil {
		return err
	}
	return nil
}

func (api vagrantAPI) ContainsBox(name string) (bool, error) {
	output, err := exec.Command(vagrantCmd, []string{"--machine-readable", "box", "list"}...).Output()
	if err != nil {
		return false, err
	}
	lines := bytes.Split(output, []byte("\n"))
	for _, line := range lines {
		words := strings.Split(string(line[:]), ",")
		if words[len(words)-1] == name {
			return true, nil
		}
	}
	return false, nil
}

func (api vagrantAPI) Shell(id string, commands string) ([]byte, error) {
	chdir := `(cd %s; `
	chdir = fmt.Sprintf(chdir, api.VagrantDir()+id)
	shellCommand := chdir + strings.Replace(commands, "%s", id, -1) + ")"
	output, err := exec.Command(shCmd, []string{"-c", shellCommand}...).Output()
	return output, err
}

func (api vagrantAPI) VagrantDir() string {
	current, _ := user.Current()
	vagrantDir := current.HomeDir + "/.vagrant/"
	return vagrantDir
}

func vagrantFile() string {
	vagrantfile := `CLOUD_CONFIG_PATH = File.join(File.dirname(__FILE__), "user-data")
Vagrant.require_version ">= 1.6.0"

Vagrant.configure(2) do |config|
  config.vm.box = "boxcutter/ubuntu1504"

  config.vm.network "private_network", type: "dhcp"

  if File.exist?(CLOUD_CONFIG_PATH)
    config.vm.provision "shell", path: "#{CLOUD_CONFIG_PATH}"
  end
end
`
	return vagrantfile
}
