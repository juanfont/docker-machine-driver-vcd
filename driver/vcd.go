package vcd

import (
	"net/url"

	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/state"
	"github.com/vmware/go-vcloud-director/v2/govcd"
)

type Driver struct {
	*drivers.BaseDriver
	VcdURL           *url.URL
	VcdOrg           string
	VcdVdc           string // virtual datacenter
	VcdAllowInsecure bool
	VcdUser          string
	VcdPassword      string

	ComputeID   string
	OrgVDCNet   string
	EdgeGateway string
	PublicIP    string
	Catalog     string
	CatalogItem string
	DockerPort  int
	CPUCount    int
	MemorySize  int
	VAppID      string
}

const (
	defaultCatalog     = "Public Catalog"
	defaultCatalogItem = "Ubuntu Server 12.04 LTS (amd64 20150127)"
	defaultCpus        = 1
	defaultMemory      = 2048
	defaultSSHPort     = 22
	defaultDockerPort  = 2376
)

func NewDriver(hostName, storePath string) drivers.Driver {
	return &Driver{
		Catalog:     defaultCatalog,
		CatalogItem: defaultCatalogItem,
		CPUCount:    defaultCpus,
		MemorySize:  defaultMemory,
		DockerPort:  defaultDockerPort,
		BaseDriver: &drivers.BaseDriver{
			SSHPort:     defaultSSHPort,
			MachineName: hostName,
			StorePath:   storePath,
		},
	}
}

// Create configures and creates a new vCD vm
func (d *Driver) Create() (err error) {
	return nil
}

// DriverName returns the name of the driver
func (d *Driver) DriverName() string {
	return "vcd"
}

// GetState returns the state that the host is in (running, stopped, etc)
func (d *Driver) GetState() (state.State, error) {
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdAllowInsecure)
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
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdAllowInsecure)
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
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdAllowInsecure)
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
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdAllowInsecure)
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
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdAllowInsecure)
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
	client := govcd.NewVCDClient(*d.VcdURL, d.VcdAllowInsecure)
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
