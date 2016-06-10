package main

import (
	"bufio"
	"testing"
	"text/scanner"

	"github.com/NetSys/quilt/db"
	"github.com/NetSys/quilt/dsl"
	"github.com/NetSys/quilt/engine"
	"github.com/NetSys/quilt/util"
)

func configRunOnce(configPath string, quiltPath []string) error {
	f, err := util.Open(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var sc scanner.Scanner
	spec, err := dsl.New(*sc.Init(bufio.NewReader(f)), quiltPath)
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
	testConfig := func(configPath string, quiltPath []string) {
		if err := configRunOnce(configPath, quiltPath); err != nil {
			t.Errorf("%s failed validation: %s", configPath, err.Error())
		}
	}
	testConfig("./example.spec", []string{"specs/stdlib"})
	testConfig("quilt-tester/config/config.spec", []string{"specs/stdlib"})
	testConfig("specs/spark/sparkPI.spec",
		[]string{"specs/stdlib", "specs/spark", "specs/zookeeper"})
	testConfig("specs/wordpress/main.spec",
		[]string{
			"specs/stdlib",
			"specs/wordpress",
			"specs/spark",
			"specs/zookeeper",
		})
}
