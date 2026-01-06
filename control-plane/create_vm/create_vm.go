package create_vm

import (
	"context"
	"fmt"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "google.golang.org/genproto/googleapis/cloud/compute/v1"
)

func CreateVM(
	ctx context.Context,
	projectID string,
	zone string,
	vmName string,
) error {

	instancesClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return err
	}
	defer instancesClient.Close()

	// === SSH key metadata ===
	sshKey := "matth:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICrnRGaFqSdQWQ7H0Ia0po0nDG88pMj8pa7wXkQLXSmQ matth@arcturus"

	// === Disk ===
	bootDisk := &computepb.AttachedDisk{
		AutoDelete: true,
		Boot:       true,
		Type:       computepb.AttachedDisk_PERSISTENT.String(),
		InitializeParams: &computepb.AttachedDiskInitializeParams{
			SourceImage: "projects/debian-cloud/global/images/family/debian-12",
			DiskSizeGb:  10,
		},
	}

	// === Network interface ===
	networkInterface := &computepb.NetworkInterface{
		Network: "global/networks/default",
		AccessConfigs: []*computepb.AccessConfig{
			{
				Name: computepb.AccessConfig_NAT.String(),
				Type: computepb.AccessConfig_ONE_TO_ONE_NAT.String(),
			},
		},
	}

	// === Instance definition ===
	instance := &computepb.Instance{
		Name:        vmName,
		MachineType: fmt.Sprintf("zones/%s/machineTypes/e2-medium", zone),

		Disks: []*computepb.AttachedDisk{
			bootDisk,
		},

		NetworkInterfaces: []*computepb.NetworkInterface{
			networkInterface,
		},

		// 🔥 防火墙关联点就在这
		Tags: &computepb.Tags{
			Items: []string{
				"http-server",
				"https-server",
				"lb-health-check",
			},
		},

		Metadata: &computepb.Metadata{
			Items: []*computepb.MetadataItems{
				{
					Key:   "ssh-keys",
					Value: &sshKey,
				},
			},
		},
	}

	req := &computepb.InsertInstanceRequest{
		Project:          projectID,
		Zone:             zone,
		InstanceResource: instance,
	}

	op, err := instancesClient.Insert(ctx, req)
	if err != nil {
		return err
	}

	fmt.Printf("VM creation started: %s\n", op.Proto().GetName())
	return nil
}
