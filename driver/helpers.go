package vcd

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/docker/machine/libmachine/mcnutils"
	"github.com/docker/machine/libmachine/ssh"
	"github.com/vmware/go-vcloud-director/v2/govcd"
)

func (d *Driver) vcdSeemsAlive() bool {
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return false
	}
	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return false
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return false
	}
	_, err = vdc.GetOrgVdcNetworkByName(d.VcdOrgVDCNetwork, true)
	return err == nil
}

func generateVMName(prefix string) string {
	randomID := mcnutils.TruncateID(mcnutils.GenerateRandomID())
	return fmt.Sprintf("%s-%s", prefix, randomID)
}

func (d *Driver) createSSHKey() (string, error) {
	if err := ssh.GenerateSSHKey(d.GetSSHKeyPath()); err != nil {
		return "", err
	}

	publicKey, err := ioutil.ReadFile(d.publicSSHKeyPath())
	if err != nil {
		return "", err
	}

	return string(publicKey), nil
}

func (d *Driver) publicSSHKeyPath() string {
	return d.GetSSHKeyPath() + ".pub"
}

func (d *Driver) getGuestCustomizationScript() (string, error) {
	key, err := d.createSSHKey()
	if err != nil {
		return "", err
	}
	sshCustomScript := `#!/bin/bash
if [ x$1 == x"precustomization" ]; then
	echo 'Precustom'
elif [ x$1 == x"postcustomization" ]; then
	mkdir -p /root/.ssh
	echo '%s' >> /root/.ssh/authorized_keys
	chmod -R go-rwx /root/.ssh
fi`
	sshCustomScript = fmt.Sprintf(sshCustomScript, strings.TrimSpace(key))
	return sshCustomScript, nil
}

func (d *Driver) getVApp() (*govcd.VApp, error) {
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return nil, err
	}

	if d.VAppHREF != "" { // this is way quicker
		vapp := govcd.NewVApp(&client.Client)
		vapp.VApp.HREF = d.VAppHREF
		err = vapp.Refresh()
		if err != nil {
			return nil, err
		}
		return vapp, nil
	}

	org, err := client.GetOrgByName(d.VcdOrg)
	if err != nil {
		return nil, err
	}
	vdc, err := org.GetVDCByName(d.VcdVdc, false)
	if err != nil {
		return nil, err
	}
	vapp, err := vdc.GetVAppByName(d.MachineName, true)
	if err != nil {
		return nil, err
	}
	d.VAppHREF = vapp.VApp.HREF
	return vapp, nil
}

func (d *Driver) getVM() (*govcd.VM, error) {
	client, err := newClient(*d.VcdURL, d.VcdUser, d.VcdPassword, d.VcdOrg, d.VcdInsecure)
	if err != nil {
		return nil, err
	}
	if d.VMHREF != "" {
		vm := govcd.NewVM(&client.Client)
		vm.VM.HREF = d.VMHREF
		err = vm.Refresh()
		if err != nil {
			return nil, err
		}
		return vm, nil
	}

	vapp, err := d.getVApp()
	if err != nil {
		return nil, err
	}

	if len(vapp.VApp.Children.VM) != 1 {
		return nil, fmt.Errorf("VM count != 1")
	}
	vm := govcd.NewVM(&client.Client)
	vm.VM.HREF = vapp.VApp.Children.VM[0].HREF
	err = vm.Refresh()
	if err != nil {
		return nil, err
	}

	d.VMHREF = vm.VM.HREF
	return vm, nil

}

func newClient(apiURL url.URL, user, password, org string, insecure bool) (*govcd.VCDClient, error) {
	vcdclient := &govcd.VCDClient{
		Client: govcd.Client{
			APIVersion: "36.3",
			VCDHREF:    apiURL,
			Http: http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: insecure,
					},
					Proxy:               http.ProxyFromEnvironment,
					TLSHandshakeTimeout: 120 * time.Second, // Default timeout for TSL hand shake
				},
				Timeout: 600 * time.Second, // Default value for http request+response timeout
			},
			MaxRetryTimeout: 60, // Default timeout in seconds for retries calls in functions
		},
	}
	err := vcdclient.Authenticate(user, password, org)
	if err != nil {
		return nil, fmt.Errorf("unable to authenticate to Org \"%s\": %s", org, err)
	}
	return vcdclient, nil
}
