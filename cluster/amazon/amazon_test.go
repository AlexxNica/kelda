//go:generate mockery -inpkg -name=client
package amazon

import (
	"encoding/base64"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/NetSys/quilt/cluster/acl"
	"github.com/NetSys/quilt/cluster/cloudcfg"
	"github.com/NetSys/quilt/cluster/machine"
	"github.com/NetSys/quilt/db"
)

const testNamespace = "namespace"

func TestList(t *testing.T) {
	t.Parallel()

	mc := new(mockClient)
	instances := []*ec2.Instance{
		// A booted spot instance (with a matching spot tag).
		{
			InstanceId:            aws.String("inst1"),
			SpotInstanceRequestId: aws.String("spot1"),
			PublicIpAddress:       aws.String("publicIP"),
			PrivateIpAddress:      aws.String("privateIP"),
			InstanceType:          aws.String("size"),
			State: &ec2.InstanceState{
				Name: aws.String(ec2.InstanceStateNameRunning),
			},
		},
		// A booted spot instance (with a lost spot tag).
		{
			InstanceId:            aws.String("inst2"),
			SpotInstanceRequestId: aws.String("spot2"),
			InstanceType:          aws.String("size2"),
			State: &ec2.InstanceState{
				Name: aws.String(ec2.InstanceStateNameRunning),
			},
		},
	}
	mc.On("DescribeInstances", mock.Anything).Return(
		&ec2.DescribeInstancesOutput{
			Reservations: []*ec2.Reservation{
				{
					Instances: instances,
				},
			},
		}, nil,
	)
	mc.On("DescribeSpotInstanceRequests", mock.Anything).Return(
		&ec2.DescribeSpotInstanceRequestsOutput{
			SpotInstanceRequests: []*ec2.SpotInstanceRequest{
				// A spot request with tags and a corresponding instance.
				{
					SpotInstanceRequestId: aws.String("spot1"),
					State: aws.String(ec2.SpotInstanceStateActive),
					Tags: []*ec2.Tag{
						{
							Key:   aws.String(testNamespace),
							Value: aws.String(""),
						},
					},
					InstanceId: aws.String("inst1"),
				},
				// A spot request without tags, but with
				// a corresponding instance.
				{
					SpotInstanceRequestId: aws.String("spot2"),
					State: aws.String(
						ec2.SpotInstanceStateActive),
					InstanceId: aws.String("inst2"),
				},
				// A spot request that hasn't been booted yet.
				{
					SpotInstanceRequestId: aws.String("spot3"),
					State: aws.String(ec2.SpotInstanceStateOpen),
					Tags: []*ec2.Tag{
						{
							Key:   aws.String(testNamespace),
							Value: aws.String(""),
						},
					},
				},
				// A spot request in another namespace.
				{
					SpotInstanceRequestId: aws.String("spot4"),
					State: aws.String(ec2.SpotInstanceStateOpen),
					Tags: []*ec2.Tag{
						{
							Key:   aws.String("notOurs"),
							Value: aws.String(""),
						},
					},
				},
			},
		}, nil,
	)

	emptyClient := new(mockClient)
	emptyClient.On("DescribeInstances", mock.Anything).Return(
		&ec2.DescribeInstancesOutput{}, nil,
	)
	emptyClient.On("DescribeSpotInstanceRequests", mock.Anything).Return(
		&ec2.DescribeSpotInstanceRequestsOutput{}, nil,
	)

	amazonCluster := newAmazon(testNamespace)
	amazonCluster.newClient = func(region string) client {
		if region == "us-west-1" {
			return mc
		}
		return emptyClient
	}

	spots, err := amazonCluster.List()

	assert.Nil(t, err)
	assert.Equal(t, []machine.Machine{
		{
			ID:        "spot1",
			Provider:  db.Amazon,
			PublicIP:  "publicIP",
			PrivateIP: "privateIP",
			Size:      "size",
			Region:    "us-west-1",
		},
		{
			ID:       "spot2",
			Provider: db.Amazon,
			Region:   "us-west-1",
			Size:     "size2",
		},
		{
			ID:       "spot3",
			Provider: db.Amazon,
			Region:   "us-west-1",
		},
	}, spots)
}

func TestNewACLs(t *testing.T) {
	t.Parallel()

	mc := new(mockClient)
	mc.On("DescribeSecurityGroups", mock.Anything).Return(
		&ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []*ec2.SecurityGroup{
				{
					IpPermissions: []*ec2.IpPermission{
						{
							IpRanges: []*ec2.IpRange{
								{CidrIp: aws.String(
									"deleteMe")},
							},
							IpProtocol: aws.String("-1"),
						},
						{
							IpRanges: []*ec2.IpRange{
								{CidrIp: aws.String(
									"foo")},
							},
							FromPort:   aws.Int64(1),
							ToPort:     aws.Int64(65535),
							IpProtocol: aws.String("tcp"),
						},
						{
							IpRanges: []*ec2.IpRange{
								{CidrIp: aws.String(
									"foo")},
							},
							FromPort:   aws.Int64(1),
							ToPort:     aws.Int64(65535),
							IpProtocol: aws.String("udp"),
						},
					},
					GroupId: aws.String(""),
				},
			},
		}, nil,
	)
	mc.On("RevokeSecurityGroupIngress", mock.Anything).Return(
		&ec2.RevokeSecurityGroupIngressOutput{}, nil,
	)
	mc.On("AuthorizeSecurityGroupIngress", mock.Anything).Return(
		&ec2.AuthorizeSecurityGroupIngressOutput{}, nil,
	)
	mc.On("DescribeInstances", mock.Anything).Return(
		&ec2.DescribeInstancesOutput{}, nil,
	)

	cluster := newAmazon(testNamespace)
	cluster.newClient = func(region string) client {
		return mc
	}

	err := cluster.SetACLs([]acl.ACL{
		{
			CidrIP:  "foo",
			MinPort: 1,
			MaxPort: 65535,
		},
		{
			CidrIP:  "bar",
			MinPort: 80,
			MaxPort: 80,
		},
	})

	assert.Nil(t, err)

	mc.AssertCalled(t, "RevokeSecurityGroupIngress",
		&ec2.RevokeSecurityGroupIngressInput{
			GroupName: aws.String(testNamespace),
			IpPermissions: []*ec2.IpPermission{
				{
					IpRanges: []*ec2.IpRange{
						{
							CidrIp: aws.String("deleteMe"),
						},
					},
					IpProtocol: aws.String("-1"),
				},
			},
		},
	)

	mc.AssertCalled(t, "AuthorizeSecurityGroupIngress",
		&ec2.AuthorizeSecurityGroupIngressInput{
			GroupName:               aws.String(testNamespace),
			SourceSecurityGroupName: aws.String(testNamespace),
		},
	)

	// Manually extract and compare the ingress rules for allowing traffic based
	// on IP ranges so that we can sort them because HashJoin returns results
	// in a non-deterministic order.
	var perms []*ec2.IpPermission
	var foundCall bool
	for _, call := range mc.Calls {
		if call.Method == "AuthorizeSecurityGroupIngress" {
			arg := call.Arguments.Get(0).(*ec2.
				AuthorizeSecurityGroupIngressInput)
			if len(arg.IpPermissions) != 0 {
				foundCall = true
				perms = arg.IpPermissions
				break
			}
		}
	}
	if !foundCall {
		t.Errorf("Expected call to AuthorizeSecurityGroupIngress to set IP ACLs")
	}

	sort.Sort(ipPermSlice(perms))
	exp := []*ec2.IpPermission{
		{
			IpRanges: []*ec2.IpRange{
				{
					CidrIp: aws.String("bar"),
				},
			},
			FromPort:   aws.Int64(-1),
			ToPort:     aws.Int64(-1),
			IpProtocol: aws.String("icmp"),
		},
		{
			IpRanges: []*ec2.IpRange{
				{CidrIp: aws.String(
					"foo")},
			},
			FromPort:   aws.Int64(-1),
			ToPort:     aws.Int64(-1),
			IpProtocol: aws.String("icmp"),
		},
		{
			IpRanges: []*ec2.IpRange{
				{
					CidrIp: aws.String("bar"),
				},
			},
			FromPort:   aws.Int64(80),
			ToPort:     aws.Int64(80),
			IpProtocol: aws.String("tcp"),
		},
		{
			IpRanges: []*ec2.IpRange{
				{
					CidrIp: aws.String("bar"),
				},
			},
			FromPort:   aws.Int64(80),
			ToPort:     aws.Int64(80),
			IpProtocol: aws.String("udp"),
		},
	}
	if !reflect.DeepEqual(perms, exp) {
		t.Errorf("Bad args to AuthorizeSecurityGroupIngress: "+
			"Expected %v, got %v.", exp, perms)
	}
}

func TestBoot(t *testing.T) {
	t.Parallel()

	sleep = func(t time.Duration) {}
	instances := []*ec2.Instance{
		{
			InstanceId:            aws.String("inst1"),
			SpotInstanceRequestId: aws.String("spot1"),
			InstanceType:          aws.String("m4.large"),
			State: &ec2.InstanceState{
				Name: aws.String(ec2.InstanceStateNameRunning),
			},
		},
		{
			InstanceId:            aws.String("inst2"),
			SpotInstanceRequestId: aws.String("spot2"),
			InstanceType:          aws.String("m4.large"),
			State: &ec2.InstanceState{
				Name: aws.String(ec2.InstanceStateNameRunning),
			},
		},
	}
	mc := new(mockClient)
	mc.On("DescribeSecurityGroups", mock.Anything).Return(
		&ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []*ec2.SecurityGroup{
				{
					GroupId: aws.String("groupId"),
				},
			},
		}, nil,
	)
	mc.On("RequestSpotInstances", mock.Anything).Return(
		&ec2.RequestSpotInstancesOutput{
			SpotInstanceRequests: []*ec2.SpotInstanceRequest{
				{
					SpotInstanceRequestId: aws.String("spot1"),
				},
				{
					SpotInstanceRequestId: aws.String("spot2"),
				},
			},
		}, nil,
	)
	mc.On("CreateTags", mock.Anything).Return(
		&ec2.CreateTagsOutput{}, nil,
	)
	mc.On("DescribeInstances", mock.Anything).Return(
		&ec2.DescribeInstancesOutput{
			Reservations: []*ec2.Reservation{
				{
					Instances: instances,
				},
			},
		}, nil,
	)
	mc.On("DescribeSpotInstanceRequests", mock.Anything).Return(
		&ec2.DescribeSpotInstanceRequestsOutput{
			SpotInstanceRequests: []*ec2.SpotInstanceRequest{
				{
					InstanceId:            aws.String("inst1"),
					SpotInstanceRequestId: aws.String("spot1"),
					State: aws.String(ec2.SpotInstanceStateActive),
					Tags: []*ec2.Tag{
						{
							Key:   aws.String(testNamespace),
							Value: aws.String(""),
						},
					},
				},
				{
					InstanceId:            aws.String("inst2"),
					SpotInstanceRequestId: aws.String("spot2"),
					State: aws.String(ec2.SpotInstanceStateActive),
					Tags: []*ec2.Tag{
						{
							Key:   aws.String(testNamespace),
							Value: aws.String(""),
						},
					},
				},
			},
		}, nil,
	)

	amazonCluster := newAmazon(testNamespace)
	amazonCluster.newClient = func(region string) client {
		return mc
	}

	err := amazonCluster.Boot([]machine.Machine{
		{
			Region:   "us-west-1",
			Size:     "m4.large",
			DiskSize: 32,
		},
		{
			Region:   "us-west-1",
			Size:     "m4.large",
			DiskSize: 32,
		},
	})
	assert.Nil(t, err)

	cfg := cloudcfg.Ubuntu(nil, "xenial")
	mc.AssertCalled(t, "RequestSpotInstances",
		&ec2.RequestSpotInstancesInput{
			SpotPrice: aws.String(spotPrice),
			LaunchSpecification: &ec2.RequestSpotLaunchSpecification{
				ImageId:      aws.String(amis["us-west-1"]),
				InstanceType: aws.String("m4.large"),
				UserData: aws.String(base64.StdEncoding.EncodeToString(
					[]byte(cfg))),
				SecurityGroupIds: aws.StringSlice([]string{"groupId"}),
				BlockDeviceMappings: []*ec2.BlockDeviceMapping{
					blockDevice(32)},
			},
			InstanceCount: aws.Int64(2),
		},
	)
	mc.AssertCalled(t, "CreateTags",
		&ec2.CreateTagsInput{
			Tags: []*ec2.Tag{
				{
					Key:   aws.String(testNamespace),
					Value: aws.String(""),
				},
			},
			Resources: aws.StringSlice([]string{"spot1", "spot2"}),
		},
	)
}

func TestStop(t *testing.T) {
	t.Parallel()

	sleep = func(t time.Duration) {}
	mc := new(mockClient)
	toStopIDs := []string{"spot1", "spot2"}
	// When we're getting information about what machines to stop.
	mc.On("DescribeSpotInstanceRequests",
		&ec2.DescribeSpotInstanceRequestsInput{
			SpotInstanceRequestIds: aws.StringSlice(toStopIDs),
		}).Return(
		&ec2.DescribeSpotInstanceRequestsOutput{
			SpotInstanceRequests: []*ec2.SpotInstanceRequest{
				{
					SpotInstanceRequestId: aws.String(toStopIDs[0]),
					InstanceId:            aws.String("inst1"),
					State: aws.String(
						ec2.SpotInstanceStateActive),
				},
				{
					SpotInstanceRequestId: aws.String(toStopIDs[1]),
					State: aws.String(ec2.SpotInstanceStateActive),
				},
			},
		}, nil,
	)
	// When we're listing machines to tell if they've stopped.
	mc.On("DescribeSpotInstanceRequests", mock.Anything).Return(
		&ec2.DescribeSpotInstanceRequestsOutput{}, nil,
	)
	mc.On("TerminateInstances", mock.Anything).Return(
		&ec2.TerminateInstancesOutput{}, nil,
	)
	mc.On("CancelSpotInstanceRequests", mock.Anything).Return(
		&ec2.CancelSpotInstanceRequestsOutput{}, nil,
	)
	mc.On("DescribeInstances", mock.Anything).Return(
		&ec2.DescribeInstancesOutput{}, nil,
	)

	amazonCluster := newAmazon(testNamespace)
	amazonCluster.newClient = func(region string) client {
		return mc
	}

	err := amazonCluster.Stop([]machine.Machine{
		{
			Region: "us-west-1",
			ID:     toStopIDs[0],
		},
		{
			Region: "us-west-1",
			ID:     toStopIDs[1],
		},
	})
	assert.Nil(t, err)

	mc.AssertCalled(t, "TerminateInstances",
		&ec2.TerminateInstancesInput{
			InstanceIds: aws.StringSlice([]string{"inst1"}),
		},
	)

	mc.AssertCalled(t, "CancelSpotInstanceRequests",
		&ec2.CancelSpotInstanceRequestsInput{
			SpotInstanceRequestIds: aws.StringSlice(toStopIDs),
		},
	)
}

func TestWait(t *testing.T) {
	t.Parallel()

	sleep = func(t time.Duration) {}
	instances := []*ec2.Instance{
		{
			InstanceId:            aws.String("inst1"),
			SpotInstanceRequestId: aws.String("spot1"),
			InstanceType:          aws.String("m4.large"),
			State: &ec2.InstanceState{
				Name: aws.String(ec2.InstanceStateNameRunning),
			},
		},
		{
			InstanceId:            aws.String("inst2"),
			SpotInstanceRequestId: aws.String("spot2"),
			InstanceType:          aws.String("m4.large"),
			State: &ec2.InstanceState{
				Name: aws.String(ec2.InstanceStateNameRunning),
			},
		},
	}
	mc := new(mockClient)
	mc.On("DescribeSecurityGroups", mock.Anything).Return(
		&ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []*ec2.SecurityGroup{
				{
					GroupId: aws.String("groupId"),
				},
			},
		}, nil,
	)
	mc.On("RequestSpotInstances", mock.Anything).Return(
		&ec2.RequestSpotInstancesOutput{
			SpotInstanceRequests: []*ec2.SpotInstanceRequest{
				{
					SpotInstanceRequestId: aws.String("spot1"),
				},
				{
					SpotInstanceRequestId: aws.String("spot2"),
				},
			},
		}, nil,
	)
	mc.On("CreateTags", mock.Anything).Return(
		&ec2.CreateTagsOutput{}, nil,
	)
	describeInstances := mc.On("DescribeInstances", mock.Anything)
	describeInstances.Return(
		&ec2.DescribeInstancesOutput{}, nil,
	)
	mc.On("DescribeSpotInstanceRequests", mock.Anything).Return(
		&ec2.DescribeSpotInstanceRequestsOutput{
			SpotInstanceRequests: []*ec2.SpotInstanceRequest{
				{
					InstanceId:            aws.String("inst1"),
					SpotInstanceRequestId: aws.String("spot1"),
					State: aws.String(ec2.SpotInstanceStateActive),
					Tags: []*ec2.Tag{
						{
							Key:   aws.String(testNamespace),
							Value: aws.String(""),
						},
					},
				},
				{
					InstanceId:            aws.String("inst2"),
					SpotInstanceRequestId: aws.String("spot2"),
					State: aws.String(ec2.SpotInstanceStateActive),
					Tags: []*ec2.Tag{
						{
							Key:   aws.String(testNamespace),
							Value: aws.String(""),
						},
					},
				},
			},
		}, nil,
	)

	amazonCluster := newAmazon(testNamespace)
	amazonCluster.newClient = func(region string) client {
		return mc
	}

	exp := []awsID{{"spot1", "us-west-1"}, {"spot2", "us-west-1"}}
	err := amazonCluster.wait(exp, true)
	assert.Error(t, err, "timed out")

	describeInstances.Return(
		&ec2.DescribeInstancesOutput{
			Reservations: []*ec2.Reservation{
				{
					Instances: instances,
				},
			},
		}, nil,
	)

	err = amazonCluster.wait(exp, true)
	assert.NoError(t, err)
}
