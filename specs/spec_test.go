package specs

import (
	"bufio"
	"testing"
	"text/scanner"

	"github.com/NetSys/quilt/stitch"
	"github.com/NetSys/quilt/util"
)

func configRunOnce(configPath string, quiltPath []string) error {
	f, err := util.Open(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var sc scanner.Scanner
	if _, err = stitch.New(*sc.Init(bufio.NewReader(f)), quiltPath); err != nil {
		return err
	}

	return nil
}

func TestConfigs(t *testing.T) {
	testConfig := func(configPath string, quiltPath []string) {
		if err := configRunOnce(configPath, quiltPath); err != nil {
			t.Errorf("%s failed validation: %s", configPath, err.Error())
		}
	}

	path := []string{
		"./etcd",
		"./redis",
		"./spark",
		"./stdlib",
		"./wordpress",
		"./zookeeper",
	}

	testConfig("example.spec", path)
	testConfig("../quilt-tester/config/config.spec", path)
	testConfig("./spark/sparkPI.spec", path)
	testConfig("./wordpress/main.spec", path)
	testConfig("./wordpress/main.spec", path)
	testConfig("./etcd/example.spec", path)
	testConfig("./redis/example.spec", path)
}
