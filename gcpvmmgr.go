package main

import (
	"context"
	"flag"
	"fmt"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	option "google.golang.org/api/option"
	"path"
	"time"

	pretty "github.com/kr/pretty"
	log "github.com/sirupsen/logrus"
)

var (
	// Flags
	tenantProject = flag.String("tenant_project", "", "The tenant project.")
	zone          = flag.String("zone", "", "The instance zone.")
	region        = flag.String("region", "", "The enterprise instance region.")
	waitTime      = flag.Duration("wait_time", 5*time.Minute, "Wait time for the cloud operation")
)

func main() {
	ctx := context.Background()
	flag.Parse()
	// Print context logs to stdout.
	if *zone == "" && *region == "" {
		log.Fatalf("Please specify a valid zone or region\n")
	}
	pretty.Println(ctx)
	computeService, err := compute.NewService(ctx, option.WithScopes(compute.CloudPlatformScope))
	if err != nil {
		fmt.Printf("Error while getting service, err: %v\n", err)
	}
	clusterZones := []string{}
	if *zone != "" {
		clusterZones = append(clusterZones, *zone)
	} else {
		if clusterZones, err = listZones(ctx, computeService, *tenantProject, *region); err != nil {
			log.Fatalf(fmt.Sprintln(err))
		}
	}
	fmt.Printf("zones are %v", clusterZones)
	vms, err := listVMs(ctx, computeService, *tenantProject, *zone)
	if err != nil {
		log.Fatalf(fmt.Sprintln(err))
	}
	fmt.Println(vms)
	for _, vm := range vms {
		if err = StopVMs(ctx, computeService, *tenantProject, *zone, vm.Name); err != nil {
			log.Fatalf(fmt.Sprintln(err))
		}
	}
}

func listZones(ctx context.Context, gceSvc *compute.Service, tenantProject, region string) ([]string, error) {
	zoneFilter := fmt.Sprintf("name = %s-*", region)
	// Fetch all zones in the target region.
	zonesListCall := gceSvc.Zones.List(tenantProject).Filter(zoneFilter)
	pageToken := ""
	var zoneList []*compute.Zone
	for {
		resp, err := zonesListCall.PageToken(pageToken).Do()
		if err != nil {
			return nil, fmt.Errorf("Encountered error when listing zones: %v", err)
		}
		zoneList = append(zoneList, resp.Items...)
		if pageToken = resp.NextPageToken; pageToken == "" {
			break
		}
	}
	zoneNameList := []string{}
	for _, zone := range zoneList {
		zoneNameList = append(zoneNameList, zone.Name)
	}
	return zoneNameList, nil
}

func listVMs(ctx context.Context, gceSvc *compute.Service, tenantProject, zone string) ([]*compute.Instance, error) {
	instancesListCall := gceSvc.Instances.List(tenantProject, zone)
	pageToken := ""
	var vmList []*compute.Instance
	for {
		resp, err := instancesListCall.PageToken(pageToken).Do()
		fmt.Println(resp)
		if err != nil {
			return nil, fmt.Errorf("Encountered error when listing instances: %v", err)
		}
		fmt.Printf("respone is %v \n", resp.Id)
		for _, i := range resp.Items {
			fmt.Printf("resp are %v", i.Name)
		}
		vmList = append(vmList, resp.Items...)
		if pageToken = resp.NextPageToken; pageToken == "" {
			break
		}
	}
	fmt.Println(vmList)
	return vmList, nil
}

func StopVMs(ctx context.Context, gceSvc *compute.Service, tenantProject, zone, vmName string) error {
	op, err := gceSvc.Instances.Stop(tenantProject, zone, vmName).Context(ctx).Do()
	if err != nil {
		return err
	}
	_, err = waitOperation(ctx, gceSvc, tenantProject, op)
	return err
}

func waitOperation(ctx context.Context, gceSvc *compute.Service, tenantProject string, op *compute.Operation) (uint64, error) {
	name, zone, region := op.Name, op.Zone, op.Region
	ctx, cancel := context.WithTimeout(ctx, *waitTime)
	var err error
	defer cancel()
	for {
		switch {
		case zone != "":
			op, err = gceSvc.ZoneOperations.Get(tenantProject, path.Base(zone), name).Context(ctx).Do()
		case region != "":
			op, err = gceSvc.RegionOperations.Get(tenantProject, path.Base(region), name).Context(ctx).Do()
		default:
			op, err = gceSvc.GlobalOperations.Get(tenantProject, name).Context(ctx).Do()
		}
		if err != nil {
			// Retry 503 Service Unavailable.
			if apiErr, ok := err.(*googleapi.Error); !ok || apiErr.Code != 503 {
				return 0, err
			}
			log.Warn("transient error polling operation status for %s (will retry): %v", name, err)
		} else {
			if op != nil && op.Status == "DONE" {
				if op.Error != nil && len(op.Error.Errors) > 0 {
					return 0, fmt.Errorf("operation completes with error %v", op.Error.Errors[0])
				}
				return op.TargetId, nil
			}
		}

		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("context is done while waiting for operation %s", name)
		case <-time.After(5 * time.Second):
			// Continue the loop.
		}
	}
}
