package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"reflect"
	"time"

	"github.com/op/go-logging"

	"github.com/NetSys/di/util"
)

type Config struct {
	Namespace string

	RedCount    int
	BlueCount   int
	WorkerCount int    /* Number of worker VMs */
	MasterCount int    /* Number of master VMs */
	Region      string /* AWS availability zone */

	AdminACL          []string
	SSHAuthorizedKeys []string
}

var log = logging.MustGetLogger("config")

/* Convert 'cfg' its string representation. */
func (cfg Config) String() string {
	str := fmt.Sprintf(
		"{\n\tNamespace: %s,\n\tMasterCount: %d\n\tWorkerCount: %d,\n\tRegion: %s\n}",
		cfg.Namespace, cfg.MasterCount, cfg.WorkerCount, cfg.Region)
	return str
}

func parseConfig(config_path string) *Config {
	var config Config

	config_file, err := ioutil.ReadFile(config_path)
	if err != nil {
		log.Warning("Error reading config")
		log.Warning(err.Error())
		return nil
	}

	err = json.Unmarshal(config_file, &config)
	if err != nil {
		log.Warning("Malformed config")
		log.Warning(err.Error())
		return nil
	}

	for i, acl := range config.AdminACL {
		if acl == "local" {
			ip, err := util.MyIp()
			if err != nil {
				log.Warning("Failed to get local IP address. Skipping ACL: %s",
					err)
			} else {
				config.AdminACL[i] = ip + "/32"
			}
		}
	}

	/* XXX: There's research in this somewhere.  How do we validate inputs into
	 * the policy?  What do we do with a policy that's wrong?  Also below, we
	 * want someone to be able to say "limit the number of instances for cost
	 * reasons" ... look at what's going on in amp for example.  100k in a month
	 * is crazy. */

	return &config
}

func watchConfigForUpdates(config_path string, config_chan chan Config) {
	var old_config *Config

	old_config = nil

	for {
		new_config := parseConfig(config_path)
		if new_config != nil && (old_config == nil ||
			!reflect.DeepEqual(*old_config, *new_config)) {
			config_chan <- *new_config
			old_config = new_config
		}
		time.Sleep(2 * time.Second)
	}
}

func CloudConfig(cfg Config) string {
	cloud_config := `#cloud-config

coreos:
    units:
        - name: minion.service
          command: start
          content: |
            [Unit]
            Description=DI Minion
            After=docker.service
            Requires=docker.service

            [Service]
            ExecStartPre=-/usr/bin/docker kill minion
            ExecStartPre=-/usr/bin/docker rm minion
            ExecStartPre=/usr/bin/docker pull quay.io/netsys/di-minion
            ExecStart=/usr/bin/docker run --net=host --name=minion --privileged \
            -v /var/run/docker.sock:/var/run/docker.sock quay.io/netsys/di-minion

`

	if len(cfg.SSHAuthorizedKeys) > 0 {
		cloud_config += "ssh_authorized_keys:\n"
		for _, key := range cfg.SSHAuthorizedKeys {
			cloud_config += fmt.Sprintf("    - \"%s\"\n", key)
		}
	}

	return cloud_config
}

func WatchConfig(config_path string) chan Config {
	config_chan := make(chan Config)
	go watchConfigForUpdates(config_path, config_chan)
	return config_chan
}
