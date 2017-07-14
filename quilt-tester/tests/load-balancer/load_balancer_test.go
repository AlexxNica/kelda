package main

import (
	"os/exec"
	"strings"
	"testing"

	log "github.com/Sirupsen/logrus"

	"github.com/quilt/quilt/api"
	"github.com/quilt/quilt/api/client"
	"github.com/quilt/quilt/db"
)

const (
	fetcherLabel      = "fetcher"
	loadBalancedLabel = "loadBalanced"
)

func TestLoadBalancer(t *testing.T) {
	c, err := client.New(api.DefaultSocket)
	if err != nil {
		t.Fatalf("couldn't get local client: %s", err)
	}
	defer c.Close()

	containers, err := c.QueryContainers()
	if err != nil {
		t.Fatalf("couldn't get containers: %s", err)
	}

	var loadBalancedContainers []db.Container
	var fetcherID string
	for _, c := range containers {
		if contains(c.Labels, fetcherLabel) {
			fetcherID = c.StitchID
		}
		if contains(c.Labels, loadBalancedLabel) {
			loadBalancedContainers = append(loadBalancedContainers, c)
		}
	}
	log.WithField("expected unique responses", len(loadBalancedContainers)).
		Info("Starting fetching..")

	if fetcherID == "" {
		t.Fatal("couldn't find fetcher")
	}

	loadBalancedCounts := map[string]int{}
	for i := 0; i < len(loadBalancedContainers)*15; i++ {
		outBytes, err := exec.Command("quilt", "ssh", fetcherID,
			"wget", "-q", "-O", "-", loadBalancedLabel+".q").
			CombinedOutput()
		if err != nil {
			t.Errorf("Unable to GET: %s", err)
			continue
		}

		loadBalancedCounts[strings.TrimSpace(string(outBytes))]++
	}

	log.WithField("counts", loadBalancedCounts).Info("Fetching completed")
	if len(loadBalancedCounts) < len(loadBalancedContainers) {
		t.Fatal("some containers not load balanced: "+
			"expected to query %d containers, got %d",
			len(loadBalancedContainers), len(loadBalancedCounts))
	}
}

func contains(lst []string, key string) bool {
	for _, v := range lst {
		if v == key {
			return true
		}
	}
	return false
}
