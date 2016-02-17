package cluster

// First, download Publish Settings using the following link:
// https://manage.windowsazure.com/PublishSettings/
// Save the file as ~/.azure/azure.publishsettings
//
// Second, if there is no storage account in the subscription yet, add a classic storage
// account from the portal (not resource group)

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/management"
	"github.com/Azure/azure-sdk-for-go/management/hostedservice"
	"github.com/Azure/azure-sdk-for-go/management/virtualmachine"
	"github.com/Azure/azure-sdk-for-go/management/vmutils"
	"github.com/NetSys/di/db"
	"github.com/satori/go.uuid"
)

const storageAccount string = "netsysstorage"
const clusterLocation string = "Central US"
const vmSize string = "Basic_A0"
const vmImage string = "b39f27a8b8c64d52b05eac6a62ebad85__Ubuntu-15_10-amd64-server-20151116.1-en-us-30GB"
const username string = "ubuntu"

type azureCluster struct {
	azureClient    management.Client
	hsClient       hostedservice.HostedServiceClient
	vmClient       virtualmachine.VirtualMachineClient
	namespace      string
	storageAccount string
	location       string
	vmSize         string
	vmImage        string
	username       string
	userPassword   string // Required password is a randomly generated UUID.
}

// Create an Azure clister.
func newAzure(conn db.Conn, clusterId int, namespace string) (provider, error) {
	if namespace == "" {
		return nil, errors.New("namespace cannot be empty.")
	}

	keyfile := filepath.Join(os.Getenv("HOME"), ".azure", "azure.publishsettings")

	azureClient, err := management.ClientFromPublishSettingsFile(keyfile, "")
	if err != nil {
		return nil, errors.New("error retrieving azure client from publishsettings")
	}

	clst := &azureCluster{
		azureClient:    azureClient,
		hsClient:       hostedservice.NewClient(azureClient),
		vmClient:       virtualmachine.NewClient(azureClient),
		namespace:      namespace,
		storageAccount: storageAccount,
		location:       clusterLocation,
		vmSize:         vmSize,
		vmImage:        vmImage,
		username:       username,
		userPassword:   uuid.NewV4().String(), // Randomly generate pwd
	}

	return clst, nil
}

// Retrieve list of instances.
func (clst *azureCluster) get() ([]machine, error) {
	var mList []machine

	hsResponse, err := clst.hsClient.ListHostedServices()
	if err != nil {
		return nil, err
	}

	for _, hs := range hsResponse.HostedServices {
		if hs.Description != clst.namespace {
			continue
		}
		id := hs.ServiceName

		// Will return empty string if the hostedservice does not have a deployment.
		// e.g. some hosted services contains only a storage account, but no deployment.
		deploymentName, err := clst.vmClient.GetDeploymentName(id)
		if err != nil {
			return nil, err
		}

		if deploymentName == "" {
			continue
		}

		deploymentResponse, err := clst.vmClient.GetDeployment(id, deploymentName)
		if err != nil {
			return nil, err
		}

		roleInstance := deploymentResponse.RoleInstanceList[0]
		privateIp := roleInstance.IPAddress
		publicIp := roleInstance.InstanceEndpoints[0].Vip

		mList = append(mList, machine{
			id:        id,
			publicIP:  publicIp,
			privateIP: privateIp,
		})
	}

	return mList, nil
}

// Boot Azure instances (blocking by calling instanceNew).
func (clst *azureCluster) boot(count int, cloudConfig string) error {
	if count < 0 {
		panic("boot count cannot be negative")
	}

	for i := 0; i < count; i++ {
		name := "di-" + uuid.NewV4().String()
		if err := clst.instanceNew(name, cloudConfig); err != nil {
			return err
		}
	}

	return nil
}

// Delete Azure instances (blocking by calling instanceDel).
func (clst *azureCluster) stop(ids []string) error {
	for _, id := range ids {
		if err := clst.instanceDel(id); err != nil {
			return err
		}
	}
	return nil
}

// Disconnect.
func (clst *azureCluster) disconnect() {
	// nothing
}

// Create one Azure instance (blocking).
func (clst *azureCluster) instanceNew(name string, cloudConfig string) error {
	// create hostedservice
	err := clst.hsClient.CreateHostedService(
		hostedservice.CreateHostedServiceParameters{
			ServiceName: name,
			Description: clst.namespace,
			Location:    clst.location,
			Label:       base64.StdEncoding.EncodeToString([]byte(name)),
		})
	if err != nil {
		return err
	}

	role := vmutils.NewVMConfiguration(name, clst.vmSize)
	mediaLink := fmt.Sprintf(
		"http://%s.blob.core.windows.net/vhds/%s.vhd",
		clst.storageAccount,
		name)
	vmutils.ConfigureDeploymentFromPlatformImage(
		&role,
		clst.vmImage,
		mediaLink,
		"")
	vmutils.ConfigureForLinux(&role, name, clst.username, clst.userPassword)
	vmutils.ConfigureWithPublicSSH(&role)

	role.ConfigurationSets[0].CustomData =
		base64.StdEncoding.EncodeToString([]byte(cloudConfig))

	operationID, err := clst.vmClient.CreateDeployment(
		role,
		name,
		virtualmachine.CreateDeploymentOptions{})
	if err != nil {
		return err
	}

	// Block the operation.
	if err := clst.azureClient.WaitForOperation(operationID, nil); err != nil {
		return err
	}

	return nil
}

// Delete one Azure instance by name (blocking).
func (clst *azureCluster) instanceDel(name string) error {
	operationID, err := clst.hsClient.DeleteHostedService(name, true)
	if err != nil {
		return err
	}

	// Block the operation.
	if err := clst.azureClient.WaitForOperation(operationID, nil); err != nil {
		return err
	}

	return nil
}
