# docker-machine-driver-vcd
A VMware vCloud Director driver for Docker Machine

## Overview

This project is a little experiment to run Docker Machine on top of vCloud Director virtual machines. 

Apparently there are still use cases for this in 2021 😉

## Usage

Install latest (git) version of docker-machine-driver-vcd in your $GOPATH/bin (depends on Golang and docker-machine)
```
$ go get -u github.com/juanfont/docker-machine-driver-vcd
```

If you don't have $GOPATH/bin in your $PATH, copy the binary somewhere useful :)
```
cp $GOPATH/bin/docker-machine-driver-vcd /usr/local/bin/

```


## Credits 
This driver is based on previous drivers:

- The original vmwarevcloudair https://github.com/docker/machine/tree/master/drivers/vmwarevcloudair
- This vcd driver targeting an old version of govcd https://github.com/jxoir/docker-machine-driver-vcloud-director
- Somehow this Scaleway driver  https://github.com/scaleway/docker-machine-driver-scaleway
