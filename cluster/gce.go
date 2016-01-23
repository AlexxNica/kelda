package cluster

////// ADDING YOUR SSH KEY:
//
// In the Google Developer Console navigate to:
// Metadata > SSH Keys
//
////// SETTING UP API ACCESS:
//
// First, in the Google Developer Console navigate to:
// API Manager > Credentials
//
// To create a new key:
// New credentials > OAuth client ID > Other > Create
//
// To retrieve key:
// Download OAuth 2.0 client ID, save it as "~/.gce/client_secret.json"

import (
	"encoding/gob"
	"errors"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NetSys/di/db"
	"github.com/satori/go.uuid"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

// XXX: Currently the oauth method is interactive (it opens up in the browser),
// this should be changed to something non-interative to be used in a server
// environment.

const COMPUTE_BASE_URL string = "https://www.googleapis.com/compute/v1/projects"

var gAuthClient *http.Client    // the oAuth client
var gceService *compute.Service // gce service

type gceCluster struct {
	projId   string // gce project ID
	zone     string // gce zone
	imgURL   string // gce url to the VM image
	machType string // gce machine type
	baseURL  string // gce project specific url prefix

	ns string // cluster namespace
	id int    // the id of the cluster, used externally
}

// Create a GCE cluster.
//
// Clusters are differentiated (namespace) by setting the description and
// filtering off of that.
//
// XXX: A lot of the fields are hardcoded.
func newGCE(conn db.Conn, clusterId int, namespace string) (provider, error) {
	if namespace == "" {
		panic("newGCE(): namespace CANNOT be empty")
	}

	err := gceInit()
	if err != nil {
		log.Error("Initialize GCE failed")
		return nil, err
	}

	clst := &gceCluster{
		projId:   "declarative-infrastructure",
		zone:     "us-central1-a",
		machType: "f1-micro",
		id:       clusterId,
		ns:       namespace,
		imgURL: fmt.Sprintf(
			"%s/%s",
			COMPUTE_BASE_URL,
			"coreos-cloud/global/images/coreos-beta-899-3-0-v20160115"),
	}
	clst.baseURL = fmt.Sprintf("%s/%s", COMPUTE_BASE_URL, clst.projId)

	return clst, nil
}

func (clst *gceCluster) get() ([]machine, error) {
	list, err := gceService.Instances.List(clst.projId, clst.zone).
		Filter(fmt.Sprintf("description eq %s", clst.ns)).Do()
	if err != nil {
		log.Error("%+v", err)
		return nil, err
	}
	var mList []machine
	for _, item := range list.Items {
		// XXX: This make some iffy assumptions about NetworkInterfaces
		mList = append(mList, machine{
			id:        item.Name,
			publicIP:  item.NetworkInterfaces[0].AccessConfigs[0].NatIP,
			privateIP: item.NetworkInterfaces[0].NetworkIP,
		})
	}
	return mList, nil
}

// Boots instances, it is a blocking call.
//
// XXX: currently ignores cloudConfig
// XXX: should probably have a better clean up routine if an error is encountered
func (clst *gceCluster) boot(count int, cloudConfig string) error {
	if count < 0 {
		return errors.New("count must be >= 0")
	}
	var ids []string
	for i := 0; i < count; i++ {
		name := "di-" + uuid.NewV4().String()
		_, err := clst.instanceNew(name, cloudConfig)
		if err != nil {
			return err
		}
		ids = append(ids, name)
	}
	err := clst.wait(ids, true)
	if err != nil {
		return err
	}
	return nil
}

// Deletes the instances, it is a blocking call.
//
// If an error occurs while deleting, it will finish the ones that have
// successfully started before returning.
//
// XXX: should probably have a better clean up routine if an error is encountered
func (clst *gceCluster) stop(ids []string) error {
	for _, id := range ids {
		_, err := clst.instanceDel(id)
		if err != nil {
			log.Error("%+v", err)
			continue
		}
	}
	err := clst.wait(ids, false)
	if err != nil {
		log.Error("%+v", err)
		return err
	}
	return nil
}

// Disconnect
func (clst *gceCluster) disconnect() {
	// Nothing to do here
}

// Blocking wait with a hardcoded timeout.
//
// If live=true then wait for instance to be active, if live=false then
// wait for instance to be deleted.
//
// XXX: maybe not hardcode timeout, and retry interval
func (clst *gceCluster) wait(ids []string, live bool) error {
	if len(ids) == 0 {
		return nil
	}

	after := time.After(3 * time.Minute)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	var liveCount int
	// Since this Map is used as a Set the value is not used
	idSet := make(map[string]struct{})
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	log.Info("wait(): waiting...")
	defer log.Info("wait(): finished waiting")
	for {
		select {
		case <-after:
			return errors.New("wait(): timeout")
		case <-tick.C:
			machs, err := clst.get()
			if err != nil {
				return err
			}
			liveCount = 0
			for _, m := range machs {
				_, inIds := idSet[m.id]
				if inIds {
					liveCount++
				}
			}
			if live && liveCount == len(ids) {
				return nil
			}
			if !live && liveCount == 0 {
				return nil
			}
		}
	}
}

// Get a GCE instance.
func (clst *gceCluster) instanceGet(name string) (*compute.Instance, error) {
	ist, err := gceService.Instances.
		Get(clst.projId, clst.zone, name).Do()
	return ist, err
}

// Create new GCE instance.
//
// Does not check if the operation succeeds.
//
// XXX: all kinds of hardcoded junk in here
// XXX: currently only defines the bare minimum
func (clst *gceCluster) instanceNew(name string, cloudConfig string) (*compute.Operation, error) {
	instance := &compute.Instance{
		Name:        name,
		Description: clst.ns,
		MachineType: fmt.Sprintf("%s/zones/%s/machineTypes/%s",
			clst.baseURL,
			clst.zone,
			clst.machType),
		Disks: []*compute.AttachedDisk{
			{
				Boot: true,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: clst.imgURL,
				},
			},
		},
		NetworkInterfaces: []*compute.NetworkInterface{
			{
				AccessConfigs: []*compute.AccessConfig{
					{
						Type: "ONE_TO_ONE_NAT",
						Name: "External NAT",
					},
				},
				Network: clst.baseURL + "/global/networks/default",
			},
		},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					// There is a generic startup script method and a
					// CoreOS specific way of startup scripting
					//
					//Key:   "startup-script", //XXX This is the GENERIC way
					Key:   "user-data", //XXX This is CoreOS SPECIFIC
					Value: &cloudConfig,
				},
			},
		},
	}

	op, err := gceService.Instances.Insert(clst.projId, clst.zone, instance).Do()
	if err != nil {
		log.Error("%+v", err)
		return nil, err
	}
	return op, nil
}

// Delete a GCE instance.
//
// Does not check if the operation succeeds
func (clst *gceCluster) instanceDel(name string) (*compute.Operation, error) {
	op, err := gceService.Instances.Delete(clst.projId, clst.zone, name).Do()
	return op, err
}

// Initialize GCE.
//
// Authenication and the client are things that are re-used across clusters.
//
// Idempotent, can call multiple times but will only initialize once.
//
// XXX: ^but should this be the case? maybe we can just have the user call it?
func gceInit() error {
	if gAuthClient == nil {
		log.Info("GCE initializing...")
		keyfile := filepath.Join(
			os.Getenv("HOME"),
			".gce",
			"client_secret.json")
		b, err := ioutil.ReadFile(keyfile)
		if err != nil {
			return fmt.Errorf(
				"Unable to read client secret file: %v",
				err)
		}
		oconf, err := google.ConfigFromJSON(b, compute.ComputeScope)
		if err != nil {
			return fmt.Errorf(
				"Unable to parse client secret file to config: %v",
				err)
		}
		gAuthClient = newOAuthClient(context.Background(), oconf)

		srv, err := compute.New(gAuthClient)
		if err != nil {
			log.Error("Unable to create Compute service %v", err)
			return err
		}
		gceService = srv
	} else {
		log.Info("GCE already initialized! Skipping...")
	}
	log.Info("GCE initialize success")
	return nil
}

func newOAuthClient(ctx context.Context, config *oauth2.Config) *http.Client {
	cacheFile := tokenCacheFile(config)
	token, err := tokenFromFile(cacheFile)
	if err != nil {
		token = tokenFromWeb(ctx, config)
		saveToken(cacheFile, token)
	} else {
		log.Info("Using cached token %#v from %q", token, cacheFile)
	}

	return config.Client(ctx, token)
}

func osUserCacheDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Caches")
	case "linux", "freebsd":
		return filepath.Join(os.Getenv("HOME"), ".cache")
	}
	log.Info("TODO: osUserCacheDir on GOOS %q", runtime.GOOS)
	return "."
}

func saveToken(file string, token *oauth2.Token) {
	f, err := os.Create(file)
	if err != nil {
		log.Warning("Warning: failed to cache oauth token: %v", err)
		return
	}
	defer f.Close()
	gob.NewEncoder(f).Encode(token)
}

func tokenCacheFile(config *oauth2.Config) string {
	hash := fnv.New32a()
	hash.Write([]byte(config.ClientID))
	hash.Write([]byte(config.ClientSecret))
	hash.Write([]byte(strings.Join(config.Scopes, " ")))
	fn := fmt.Sprintf("go-api-demo-tok%v", hash.Sum32())
	return filepath.Join(osUserCacheDir(), url.QueryEscape(fn))
}

func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	t := new(oauth2.Token)
	err = gob.NewDecoder(f).Decode(t)
	return t, err
}

func tokenFromWeb(ctx context.Context, config *oauth2.Config) *oauth2.Token {
	ch := make(chan string)
	randState := fmt.Sprintf("st%d", time.Now().UnixNano())
	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/favicon.ico" {
			http.Error(rw, "", 404)
			return
		}
		if req.FormValue("state") != randState {
			log.Info("State doesn't match: req = %#v", req)
			http.Error(rw, "", 500)
			return
		}
		if code := req.FormValue("code"); code != "" {
			fmt.Fprintf(rw, "<h1>Success</h1>Authorized.")
			rw.(http.Flusher).Flush()
			ch <- code
			return
		}
		log.Info("no code")
		http.Error(rw, "", 500)
	}))
	defer ts.Close()

	config.RedirectURL = ts.URL
	authURL := config.AuthCodeURL(randState)
	go openURL(authURL)
	log.Info("Authorize this app at: %s", authURL)
	code := <-ch
	log.Info("Got code: %s", code)

	token, err := config.Exchange(ctx, code)
	if err != nil {
		log.Warning("Token exchange error: %v", err)
	}
	return token
}

func openURL(url string) {
	try := []string{"xdg-open", "google-chrome", "open"}
	for _, bin := range try {
		err := exec.Command(bin, url).Run()
		if err == nil {
			return
		}
	}
	log.Warning("Error opening URL in browser.")
}
