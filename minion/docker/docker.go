package docker

import (
	"errors"
	"strings"
	"time"

	dkc "github.com/fsouza/go-dockerclient"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("docker")

var errNoSuchContainer = errors.New("container does not exist")

type Container struct {
	ID    string
	Image string

	IPs   []string
	Names []string

	Labels map[string]string
}

// A Client to the local docker daemon.
type Client interface {
	Run(opts RunOptions) error
	Exec(name string, cmd ...string) error
	Remove(name string) error
	RemoveID(id string) error
	Pull(image string) error
	CreateLSwitch(name string) error
	List(filters map[string][]string) ([]Container, error)
}

type RunOptions struct {
	Name   string
	Image  string
	Args   []string
	Labels map[string]string

	Binds       []string
	NetworkMode string
	PidMode     string
	Privileged  bool
	VolumesFrom []string
}

type pullRequest struct {
	image string
	done  chan error
}

type docker struct {
	*dkc.Client

	pullChan chan pullRequest
}

// New creates client to the docker daemon.
func New(sock string) Client {
	var client *dkc.Client
	for {
		var err error
		client, err = dkc.NewClient(sock)
		if err != nil {
			log.Warning("Failed to create docker client: %s", err)
			time.Sleep(10 * time.Second)
			continue
		}
		break
	}

	dk := docker{client, make(chan pullRequest)}
	go pullServer(dk)

	return dk
}

func pullServer(dk docker) {
	images := make(map[string]struct{})

	for req := range dk.pullChan {
		if _, ok := images[req.image]; ok {
			req.done <- nil
			continue
		}

		log.Info("Pulling docker image: %s", req.image)
		opts := dkc.PullImageOptions{Repository: string(req.image)}
		err := dk.PullImage(opts, dkc.AuthConfiguration{})

		if err != nil {
			log.Warning("Failed to pull image: %s", req.image)
		} else {
			images[req.image] = struct{}{}
		}
		req.done <- err
	}
}

func (dk docker) Run(opts RunOptions) error {
	if opts.Name != "" {
		_, err := dk.get(opts.Name)
		if err == errNoSuchContainer {
			// Only log the first time we attempt to boot.
			log.Info("Start Container: %s", opts.Name)
		} else if err != nil {
			return err
		}
	}

	id, err := dk.create(opts.Name, opts.Image, opts.Args, opts.Labels)
	if err != nil {
		return err
	}

	hc := dkc.HostConfig{
		Binds:       opts.Binds,
		NetworkMode: opts.NetworkMode,
		PidMode:     opts.PidMode,
		Privileged:  opts.Privileged,
		VolumesFrom: opts.VolumesFrom,
	}
	if err = dk.StartContainer(id, &hc); err != nil {
		if _, ok := err.(*dkc.ContainerAlreadyRunning); ok {
			return nil
		}
		return err
	}

	return nil
}

func (dk docker) Exec(name string, cmd ...string) error {
	id, err := dk.get(name)
	if err != nil {
		return err
	}

	log.Info("Exec in %s: %s", name, strings.Join(cmd, " "))
	exec, err := dk.CreateExec(dkc.CreateExecOptions{Container: id, Cmd: cmd})
	if err != nil {
		return err
	}

	err = dk.StartExec(exec.ID, dkc.StartExecOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (dk docker) Remove(name string) error {
	id, err := dk.get(name)
	if err != nil {
		return nil // Can't remove a non-existent container.
	}

	log.Info("Remove Container: %s", name)
	return dk.RemoveID(id)
}

func (dk docker) RemoveID(id string) error {
	err := dk.RemoveContainer(dkc.RemoveContainerOptions{ID: id, Force: true})
	if err != nil {
		return err
	}

	return nil
}

func (dk docker) CreateLSwitch(name string) error {
	_, err := dk.CreateNetwork(dkc.CreateNetworkOptions{
		Name:           name,
		CheckDuplicate: true,
		Driver:         "openvswitch"})
	return err
}

func (dk docker) Pull(image string) error {
	done := make(chan error)
	dk.pullChan <- pullRequest{image, done}
	return <-done
}

func (dk docker) List(filters map[string][]string) ([]Container, error) {
	opts := dkc.ListContainersOptions{All: true, Filters: filters}
	apic, err := dk.ListContainers(opts)
	if err != nil {
		return nil, err
	}

	var containers []Container
	for _, c := range apic {
		var ips []string
		for _, port := range c.Ports {
			ips = append(ips, port.IP)
		}

		containers = append(containers, Container{
			Names:  c.Names,
			ID:     c.ID,
			IPs:    ips,
			Image:  c.Image,
			Labels: c.Labels,
		})
	}

	return containers, nil
}

func (dk docker) create(name, image string, args []string,
	labels map[string]string) (string, error) {
	if err := dk.Pull(image); err != nil {
		return "", err
	}

	id, err := dk.get(name)
	if err == nil {
		return id, nil
	}

	container, err := dk.CreateContainer(dkc.CreateContainerOptions{
		Name:   name,
		Config: &dkc.Config{Image: string(image), Cmd: args, Labels: labels},
	})
	if err != nil {
		return "", err
	}

	return container.ID, nil
}

func (dk docker) get(name string) (string, error) {
	containers, err := dk.List(nil)
	if err != nil {
		return "", err
	}

	name = "/" + name
	for _, c := range containers {
		for _, cname := range c.Names {
			if name == cname {
				return c.ID, nil
			}
		}
	}

	return "", errNoSuchContainer
}
