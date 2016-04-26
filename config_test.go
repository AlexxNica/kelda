package main

import (
	"bufio"
	"testing"
	"text/scanner"

	"github.com/NetSys/di/db"
	"github.com/NetSys/di/dsl"
	"github.com/NetSys/di/engine"
	"github.com/NetSys/di/util"
)

func configRunOnce(configPath string, diPath []string) error {
	f, err := util.Open(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var sc scanner.Scanner
	spec, err := dsl.New(*sc.Init(bufio.NewReader(f)), diPath)
	if err != nil {
		return err
	}

	err = engine.UpdatePolicy(db.New(), spec)
	if err != nil {
		return err
	}

	return nil
}

func TestConfigs(t *testing.T) {
	testConfig := func(configPath string, diPath []string) {
		if err := configRunOnce(configPath, diPath); err != nil {
			t.Errorf("%s failed validation: %s", configPath, err.Error())
		}
	}
	testConfig("./config.spec", []string{"specs/stdlib"})
	testConfig("di-tester/config/config.spec", []string{"specs/stdlib"})
	testConfig("specs/spark/main.spec",
		[]string{"specs/stdlib", "specs/spark", "specs/zookeeper"})
	testConfig("specs/wordpress/main.spec",
		[]string{
			"specs/stdlib",
			"specs/wordpress",
			"specs/spark",
			"specs/zookeeper",
		})
}
