package vcd

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

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
	Catalog          string
	Template         string

	DockerPort        int
	NumCpus           int
	CoresPerSocket    int
	MemorySizeMb      int
	VAppName          string
	VAppHREF          string
	VMHREF            string
	Description       string
	StorageProfile    string
	MachineNamePrefix string
}

const (
	defaultCatalog        = "Public"
	defaultTemplate       = "Ubuntu_Server_20.04"
	defaultCpus           = 1
	defaultCoresPerSocket = 1
	defaultMemoryMb       = 2048
	defaultSSHPort        = 22
	defaultDockerPort     = 2376

	defaultDescription    = "Created with Docker Machine"
	defaultStorageProfile = ""
)

func NewDriver(hostName, storePath string) drivers.Driver {
	return &Driver{
		Catalog:        defaultCatalog,
		Template:       defaultTemplate,
		NumCpus:        defaultCpus,
		CoresPerSocket: defaultCoresPerSocket,
		MemorySizeMb:   defaultMemoryMb,
		DockerPort:     defaultDockerPort,
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
func (d *Driver) Create() error {
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
		if len(vdc.Vdc.VdcStorageProfiles.VdcStorageProfile) < 1 {
			return fmt.Errorf("No storage profile available")
		}
		storageProfile = *(vdc.Vdc.VdcStorageProfiles.VdcStorageProfile[0])
		if err != nil {
			return err
		}
	}

	if d.MachineNamePrefix != "" {
		d.VAppName = fmt.Sprintf("%s-%s", d.MachineNamePrefix, d.MachineName)
	} else {
		d.VAppName = d.MachineName
	}

	log.Infof("Creating a new vApp: %s...", d.VAppName)
	networks := []*types.OrgVDCNetwork{}
	networks = append(networks, net.OrgVDCNetwork)
	task, err := vdc.ComposeVApp(
		networks,
		vapptemplate,
		storageProfile,
		d.VAppName,
		d.Description,
		true)

	if err != nil {
		return err
	}
	if err = task.WaitTaskCompletion(); err != nil {
		return err
	}

	vapp, err := vdc.GetVAppByName(d.VAppName, true)
	if err != nil {
		return err
	}

	if len(vapp.VApp.Children.VM) != 1 {
		return fmt.Errorf("VM count != 1")
	}

	vm := govcd.NewVM(&client.Client)
	vm.VM.HREF = vapp.VApp.Children.VM[0].HREF
	vm.Refresh()
	vm.VM.VmSpecSection.MemoryResourceMb.Configured = int64(d.MemorySizeMb)
	vm.VM.VmSpecSection.NumCpus = &d.NumCpus
	vm.VM.VmSpecSection.NumCoresPerSocket = &d.CoresPerSocket

	log.Infof("Updating virtual hardware specs...")
	vm, err = vm.UpdateVmSpecSection(vm.VM.VmSpecSection, d.Description)
	if err != nil {
		return err
	}

	key, err := d.createSSHKey()
	if err != nil {
		return err
	}
	sshCustomScript := "echo \"" + strings.TrimSpace(key) + "\" > /root/.ssh/authorized_keys"

	log.Infof("Setting up guest customization...")
	enabled := true
	vm.VM.GuestCustomizationSection.Enabled = &enabled
	vm.VM.GuestCustomizationSection.CustomizationScript = sshCustomScript
	_, err = vm.SetGuestCustomizationSection(vm.VM.GuestCustomizationSection)
	if err = task.WaitTaskCompletion(); err != nil {
		return err
	}

	log.Infof("Booting up %s...", d.MachineName)
	task, err = vapp.PowerOn()
	if err != nil {
		return err
	}
	if err = task.WaitTaskCompletion(); err != nil {
		return err
	}

	d.VAppHREF = d.VAppHREF
	d.VMHREF = vm.VM.HREF

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
	vapp, err := vdc.GetVAppByHref(d.VAppHREF)
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
	vapp, err := vdc.GetVAppByHref(d.VAppHREF)
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
	vapp, err := vdc.GetVAppByHref(d.VAppHREF)
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
	vapp, err := vdc.GetVAppByHref(d.VAppHREF)
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
	vapp, err := vdc.GetVAppByHref(d.VAppHREF)
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
func (d *Driver) SetConfigFromFlags(flags drivers.DriverOptions) error {
	vcdURL := flags.String("vcd-url")
	d.VcdOrg = flags.String("vcd-org")
	d.VcdVdc = flags.String("vcd-vdc")
	d.VcdInsecure = flags.Bool("vcd-insecure")
	d.VcdUser = flags.String("vcd-user")
	d.VcdPassword = flags.String("vcd-password")
	d.VcdOrgVDCNetwork = flags.String("vcd-orgvdcnetwork")
	d.Catalog = flags.String("catalog")
	d.Template = flags.String("template")

	d.SetSwarmConfigFromFlags(flags)

	// Check for required Params
	if vcdURL == "" || d.VcdOrg == "" || d.VcdVdc == "" || d.VcdUser == "" || d.VcdPassword == "" || d.VcdOrgVDCNetwork == "" || d.Catalog == "" || d.Template == "" {
		return fmt.Errorf("Please specify the mandatory parameters: -vcd-url, -vcd-org, -vcd-vdc, -vcd-user, -vcd-password, -vdc-orgvdcnetwork, -catalog, -template")
	}

	u, err := url.ParseRequestURI(vcdURL)
	if err != nil {
		return fmt.Errorf("unable to pass url: %s", err)
	}
	d.VcdURL = u

	d.DockerPort = flags.Int("vcd-docker-port")
	d.SSHUser = "root"
	d.SSHPort = flags.Int("vcd-ssh-port")
	d.NumCpus = flags.Int("vcd-numcpus")
	d.CoresPerSocket = flags.Int("vcd-corespersocket")
	d.MemorySizeMb = flags.Int("vcd-memory-size-mb")
	d.StorageProfile = flags.String("vcd-storageprofile")
	d.Description = flags.String("vcd-description")

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
	vapp, err := vdc.GetVAppByHref(d.VAppHREF)
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
	vapp, err := vdc.GetVAppByHref(d.VAppHREF)
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
