package vcd

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/docker/machine/libmachine/mcnutils"
	"github.com/docker/machine/libmachine/ssh"
	"github.com/vmware/go-vcloud-director/v2/govcd"
)

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

func newClient(apiURL url.URL, user, password, org string, insecure bool) (*govcd.VCDClient, error) {
	vcdclient := &govcd.VCDClient{
		Client: govcd.Client{
			APIVersion: "32.0", // supported by 9.5, 9.7, 10.0, 10.1
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
