package vcd

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

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
	defaultNamePrefix     = "docker-machine"
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
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
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
	err = vm.Refresh()
	if err != nil {
		return err
	}
	log.Infof("Found VM: %s...", vm.VM.Name)

	cWait := make(chan string, 1)
	go func() {
		for {
			status, _ := vm.GetStatus()
			if status == "POWERED_OFF" {
				break
			}
			time.Sleep(5 * time.Second)
		}

		time.Sleep(30 * time.Second) // FIXME
		cWait <- "ok"
	}()

	select {
	case res := <-cWait:
		fmt.Println(res)
	case <-time.After(15 * time.Minute):
		return fmt.Errorf("Reached timeout while deploying VM")
	}

	if vm.VM.VmSpecSection == nil {
		return fmt.Errorf("VM Spec Section empty")
	}
	vm.Refresh()

	vm.VM.VmSpecSection.MemoryResourceMb.Configured = int64(d.MemorySizeMb)
	vm.VM.VmSpecSection.NumCpus = &d.NumCpus
	vm.VM.VmSpecSection.NumCoresPerSocket = &d.CoresPerSocket

	log.Infof("Updating virtual hardware specs...")
	vm, err = vm.UpdateVmSpecSection(vm.VM.VmSpecSection, d.Description)
	if err != nil {
		return err
	}

	log.Infof("Configuring network...")
	var netConn *types.NetworkConnection
	var netSection *types.NetworkConnectionSection
	if vm.VM.NetworkConnectionSection == nil {
		netSection = &types.NetworkConnectionSection{}
	} else {
		netSection = vm.VM.NetworkConnectionSection
	}

	if len(netSection.NetworkConnection) < 1 {
		netConn = &types.NetworkConnection{}
		netSection.NetworkConnection = append(netSection.NetworkConnection, netConn)
	}

	netConn = netSection.NetworkConnection[0]

	netConn.IPAddressAllocationMode = types.IPAllocationModePool
	netConn.NetworkConnectionIndex = 0
	netConn.IsConnected = true
	netConn.NeedsCustomization = true
	netConn.Network = d.VcdOrgVDCNetwork

	vm.UpdateNetworkConnectionSection(netSection)

	log.Infof("Setting up guest customization...")
	sshCustomScript, err := d.getGuestCustomizationScript()
	if err != nil {
		return err
	}

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

	d.VAppHREF = vapp.VApp.HREF
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
			Name:   "vcd-user",
			Usage:  "vCloud Director username",
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_PASSWORD",
			Name:   "vcd-password",
			Usage:  "vCloud Director password",
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_ORGVDCNETWORK",
			Name:   "vcd-orgvdcnetwork",
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

		mcnflag.IntFlag{
			EnvVar: "VCD_DOCKER_PORT",
			Name:   "vcd-docker-port",
			Usage:  "vCloud Director Docker Port",
			Value:  defaultDockerPort,
		},
		mcnflag.IntFlag{
			EnvVar: "VCD_SSH_PORT",
			Name:   "vcd-ssh-port",
			Usage:  "vCloud Director SSH Port",
			Value:  defaultSSHPort,
		},
		mcnflag.IntFlag{
			EnvVar: "VCD_NUM_CPUS",
			Name:   "vcd-numcpus",
			Usage:  "vCloud Director Num CPUs",
			Value:  defaultCpus,
		},
		mcnflag.IntFlag{
			EnvVar: "VCD_CORES_PER_SOCKET",
			Name:   "vcd-corespersocket",
			Usage:  "vCloud Director Cores Per Socket",
			Value:  defaultCoresPerSocket,
		},
		mcnflag.IntFlag{
			EnvVar: "VCD_MEMORY_SIZE_MB",
			Name:   "vcd-memory-size-mb",
			Usage:  "vCloud Director Memory Size MB",
			Value:  defaultMemoryMb,
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_STORAGE_PROFILE",
			Name:   "vcd-storageprofile",
			Usage:  "vCloud Director Storage Profile",
			Value:  defaultStorageProfile,
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_DESCRIPTION",
			Name:   "vcd-description",
			Usage:  "vCloud Director VApp Description",
			Value:  defaultDescription,
		},
		mcnflag.StringFlag{
			EnvVar: "VCD_NAME_PREFIX",
			Name:   "vcd-name-prefix",
			Usage:  "vCloud Director VApp Name Prefix",
			Value:  defaultNamePrefix,
		},
	}
}

// GetIP returns an IP or hostname that this host is available at
// e.g. 1.2.3.4 or docker-host-d60b70a14d3a.cloudapp.net
func (d *Driver) GetIP() (string, error) {
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return "", err
	}

	vm := govcd.NewVM(&client.Client)
	vm.VM.HREF = d.VMHREF
	err = vm.Refresh()
	if err != nil {
		return "", err
	}

	// We assume that the vApp has only one VM with only one NIC
	if vm.VM.NetworkConnectionSection != nil {
		networks := vm.VM.NetworkConnectionSection.NetworkConnection
		for _, n := range networks {
			if n.ExternalIPAddress != "" {
				return n.ExternalIPAddress, nil
			}
			if n.IPAddress != "" { // perhaps this is too opinionated ?
				return n.IPAddress, nil
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
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return state.Error, err
	}
	vapp := govcd.NewVApp(&client.Client)
	vapp.VApp.HREF = d.VAppHREF
	err = vapp.Refresh()
	if err != nil {
		return state.Error, err
	}

	status, err := vapp.GetStatus()
	if err != nil {
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
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return err
	}
	vapp := govcd.NewVApp(&client.Client)
	vapp.VApp.HREF = d.VAppHREF
	err = vapp.Refresh()
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

	return nil
}

// PreCreateCheck allows for pre-create operations to make sure a driver is ready for creation
func (d *Driver) PreCreateCheck() error {
	return nil
}

// Remove a host
func (d *Driver) Remove() error {
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return err
	}
	vapp := govcd.NewVApp(&client.Client)
	vapp.VApp.HREF = d.VAppHREF
	err = vapp.Refresh()
	if err != nil {
		return err
	}

	status, _ := vapp.GetStatus()
	if status != "POWERED_OFF" {
		task, err := vapp.PowerOff() // we no longer care, so no shutdown
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
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return err
	}
	vapp := govcd.NewVApp(&client.Client)
	vapp.VApp.HREF = d.VAppHREF
	err = vapp.Refresh()
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
	d.Catalog = flags.String("vcd-catalog")
	d.Template = flags.String("vcd-template")

	d.SetSwarmConfigFromFlags(flags)

	// Check for required Params
	if vcdURL == "" || d.VcdOrg == "" || d.VcdVdc == "" || d.VcdUser == "" || d.VcdPassword == "" || d.VcdOrgVDCNetwork == "" || d.Catalog == "" || d.Template == "" {
		return fmt.Errorf("Please specify the mandatory parameters: -vcd-url, -vcd-org, -vcd-vdc, -vcd-user, -vcd-password, -vcd-orgvdcnetwork, -catalog, -template")
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
	d.MachineNamePrefix = flags.String("vcd-name-prefix")

	return nil
}

// Start a host
func (d *Driver) Start() error {
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return err
	}
	vapp := govcd.NewVApp(&client.Client)
	vapp.VApp.HREF = d.VAppHREF
	err = vapp.Refresh()
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

	return nil
}

// Stop a host gracefully
func (d *Driver) Stop() error {
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return err
	}
	vapp := govcd.NewVApp(&client.Client)
	vapp.VApp.HREF = d.VAppHREF
	err = vapp.Refresh()
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

	return nil
}