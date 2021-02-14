package vcd

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/mcnflag"
	"github.com/docker/machine/libmachine/state"
	"github.com/vmware/go-vcloud-director/v2/govcd"
	"github.com/vmware/go-vcloud-director/v2/types/v56"
)

type Driver struct {
	*drivers.BaseDriver
	VcdURL           *url.URL
	VcdOrg           string
	VcdVdc           string // virtual datacenter
	VcdInsecure      bool
	VcdUser          string
	VcdPassword      string
	VcdOrgVDCNetwork string

	Catalog        string
	Template       string
	DockerPort     int
	CPUCount       int
	MemorySize     int
	VAppID         string
	Description    string
	StorageProfile string
}

const (
	defaultCatalog    = "Public"
	defaultTemplate   = "Ubuntu_Server_20.04"
	defaultCpus       = 1
	defaultMemory     = 2048
	defaultSSHPort    = 22
	defaultDockerPort = 2376

	defaultDescription    = "Created with Docker Machine"
	defaultStorageProfile = ""
)

func NewDriver(hostName, storePath string) drivers.Driver {
	return &Driver{
		Catalog:    defaultCatalog,
		Template:   defaultTemplate,
		CPUCount:   defaultCpus,
		MemorySize: defaultMemory,
		DockerPort: defaultDockerPort,
		BaseDriver: &drivers.BaseDriver{
			SSHPort:     defaultSSHPort,
			MachineName: hostName,
			StorePath:   storePath,
		},
		Description:    defaultDescription,
		StorageProfile: defaultStorageProfile,
	}
}

// Create configures and creates a new vCD vm
func (d *Driver) Create() (err error) {
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdInsecure)
	err := client.Authenticate(d.VcdUser, d.VcdPassword, d.VcdOrg)
	if err != nil {
		return err
	}
	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return err
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return err
	}

	log.Infof("Finding network...")
	net, err := vdc.GetOrgVdcNetworkByName(d.VcdOrgVDCNetwork, true)
	if err != nil {
		return err
	}

	log.Infof("Finding catalog...")
	catalog, err := org.GetCatalogByName(d.Catalog, true)
	if err != nil {
		return err
	}

	log.Infof("Finding template...")
	template, err := catalog.GetCatalogItemByName(d.Template, true)
	if err != nil {
		return err
	}
	vapptemplate, err := template.GetVAppTemplate()
	if err != nil {
		return err
	}

	var storageProfile types.Reference
	if d.StorageProfile != "" {
		storageProfile, err = vdc.FindStorageProfileReference(d.StorageProfile)
		if err != nil {
			return err
		}
	} else {

		storageProfile, err = vdc.GetDefaultStorageProfileReference()
		if err != nil {
			return err
		}
	}

	log.Infof("Creating a new vApp: %s...", d.MachineName)
	networks := []*types.OrgVDCNetwork{}
	networks = append(networks, net.OrgVDCNetwork)
	task, err := vdc.ComposeVApp(
		networks,
		vapptemplate,
		storageProfile,
		d.MachineName,
		d.Description,
		true)

	if err != nil {
		return err
	}
	if err = task.WaitTaskCompletion(); err != nil {
		return err
	}

	return nil
}

// DriverName returns the name of the driver
func (d *Driver) DriverName() string {
	return "vcd"
}

// GetCreateFlags returns the mcnflag.Flag slice representing the flags
// that can be set, their descriptions and defaults.
func (d *Driver) GetCreateFlags() []mcnflag.Flag {
	return []mcnflag.Flag{
		mcnflag.StringFlag{
			EnvVar: "VCD_URL",
			Name:   "vcd-url",
			Usage:  "vCloud Director URL",
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_ORG",
			Name:   "vcd-org",
			Usage:  "vCloud Director Org",
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_VDC",
			Name:   "vcd-vdc",
			Usage:  "vCloud Director Virtual Datacenter",
		},
		mcnflag.BoolFlag{
			EnvVar: "VCD_INSECURE",
			Name:   "vcd-insecure",
			Usage:  "vCloud Director Insecure Connection",
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_USERNAME",
			Name:   "vcd-username",
			Usage:  "vCloud Director username",
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_PASSWORD",
			Name:   "vcd-password",
			Usage:  "vCloud Director password",
		},
		mcnflag.StringFlag{
			EnvVar: "VDC_ORGVDCNETWORK",
			Name:   "vdc-orgvdcnetwork",
			Usage:  "vCloud Director OrgVDC network",
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_CATALOG",
			Name:   "vcd-catalog",
			Usage:  "vCloud Director catalog",
			Value:  defaultCatalog,
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_TEMPLATE",
			Name:   "vcd-template",
			Usage:  "vCloud Director vApp template",
			Value:  defaultTemplate,
		},
	}
}

// GetIP returns an IP or hostname that this host is available at
// e.g. 1.2.3.4 or docker-host-d60b70a14d3a.cloudapp.net
func (d *Driver) GetIP() (string, error) {
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdInsecure)
	err := client.Authenticate(d.VcdUser, d.VcdPassword, d.VcdOrg)
	if err != nil {
		return "", err
	}
	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return "", err
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return "", err
	}
	vapp, err := vdc.GetVAppById(d.VAppID, true)
	if err != nil {
		return "", err
	}

	// We assume that the vApp has only one VM with only one NIC
	for _, vm := range vapp.VApp.Children.VM {
		if vm.NetworkConnectionSection != nil {
			networks := vm.NetworkConnectionSection.NetworkConnection
			for _, n := range networks {
				if n.ExternalIPAddress != "" {
					return n.ExternalIPAddress, nil
				}
			}
		}
	}
	return "", fmt.Errorf("could not get public IP")
}

// GetSSHHostname returns the IP of the server
func (d *Driver) GetSSHHostname() (string, error) {
	return d.GetIP()
}

// GetURL returns a Docker compatible host URL for connecting to this host
// e.g. tcp://1.2.3.4:2376
func (d *Driver) GetURL() (string, error) {
	if err := drivers.MustBeRunning(d); err != nil {
		return "", err
	}
	ip, err := d.GetIP()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("tcp://%s", net.JoinHostPort(ip, strconv.Itoa(d.DockerPort))), nil
}

// GetState returns the state that the host is in (running, stopped, etc)
func (d *Driver) GetState() (state.State, error) {
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdInsecure)
	err := client.Authenticate(d.VcdUser, d.VcdPassword, d.VcdOrg)
	if err != nil {
		return state.Error, err
	}
	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return state.Error, err
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return state.Error, err
	}
	vapp, err := vdc.GetVAppById(d.VAppID, true)
	if err != nil {
		return state.Error, err
	}

	status, err := vapp.GetStatus()
	if err != nil {
		return state.Error, err
	}

	if err = client.Disconnect(); err != nil {
		return state.Error, err
	}

	switch status {
	case "POWERED_ON":
		return state.Running, nil
	case "POWERED_OFF":
		return state.Stopped, nil
	}
	return state.None, nil
}

// Kill stops a host forcefully
func (d *Driver) Kill() error {
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdInsecure)
	err := client.Authenticate(d.VcdUser, d.VcdPassword, d.VcdOrg)
	if err != nil {
		return err
	}
	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return err
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return err
	}
	vapp, err := vdc.GetVAppById(d.VAppID, true)
	if err != nil {
		return err
	}

	task, err := vapp.PowerOff()
	if err != nil {
		return err
	}
	if err = task.WaitTaskCompletion(); err != nil {
		return err
	}

	if err = client.Disconnect(); err != nil {
		return err
	}
	return nil
}

// PreCreateCheck allows for pre-create operations to make sure a driver is ready for creation
func (d *Driver) PreCreateCheck() error {
	return nil
}

// Remove a host
func (d *Driver) Remove() error {
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdInsecure)
	err := client.Authenticate(d.VcdUser, d.VcdPassword, d.VcdOrg)
	if err != nil {
		return err
	}
	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return err
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return err
	}
	vapp, err := vdc.GetVAppById(d.VAppID, true)
	if err != nil {
		return err
	}

	if vapp.VApp.Status != 8 { // powered off
		task, err := vapp.PowerOff()
		if err != nil {
			return err
		}
		if err = task.WaitTaskCompletion(); err != nil {
			return err
		}
	}

	task, err := vapp.Delete()
	if err != nil {
		return err
	}
	if err = task.WaitTaskCompletion(); err != nil {
		return err
	}
	return nil
}

// Restart a host. This may just call Stop(); Start() if the provider does not
// have any special restart behaviour.
func (d *Driver) Restart() error {
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdInsecure)
	err := client.Authenticate(d.VcdUser, d.VcdPassword, d.VcdOrg)
	if err != nil {
		return err
	}
	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return err
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return err
	}
	vapp, err := vdc.GetVAppById(d.VAppID, true)
	if err != nil {
		return err
	}
	task, err := vapp.Reboot()
	if err != nil {
		return err
	}
	if err = task.WaitTaskCompletion(); err != nil {
		return err
	}

	if err = client.Disconnect(); err != nil {
		return err
	}
	return nil
}

// SetConfigFromFlags configures the driver with the object that was returned
// by RegisterCreateFlags
func (d *Driver) SetConfigFromFlags(opts drivers.DriverOptions) error {
	return nil
}

// Start a host
func (d *Driver) Start() error {
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdInsecure)
	err := client.Authenticate(d.VcdUser, d.VcdPassword, d.VcdOrg)
	if err != nil {
		return err
	}
	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return err
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return err
	}
	vapp, err := vdc.GetVAppById(d.VAppID, true)
	if err != nil {
		return err
	}
	task, err := vapp.PowerOn()
	if err != nil {
		return err
	}
	if err = task.WaitTaskCompletion(); err != nil {
		return err
	}

	if err = client.Disconnect(); err != nil {
		return err
	}
	return nil
}

// Stop a host gracefully
func (d *Driver) Stop() error {
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdInsecure)
	err := client.Authenticate(d.VcdUser, d.VcdPassword, d.VcdOrg)
	if err != nil {
		return err
	}
	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return err
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return err
	}
	vapp, err := vdc.GetVAppById(d.VAppID, true)
	if err != nil {
		return err
	}
	task, err := vapp.Shutdown()
	if err != nil {
		return err
	}
	if err = task.WaitTaskCompletion(); err != nil {
		return err
	}

	if err = client.Disconnect(); err != nil {
		return err
	}
	return nil
}
