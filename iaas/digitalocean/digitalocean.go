package digitalocean

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"context"

	"github.com/docker/machine/drivers/digitalocean"
	"github.com/docker/machine/libmachine"
	"github.com/docker/machine/libmachine/host"
	"github.com/gofn/gofn/iaas"
	gofnssh "github.com/gofn/gofn/ssh"
	uuid "github.com/satori/go.uuid"
)

const (
	defaultRegion    = "nyc3"
	defaultSize      = "1gb"
	defaultImageSlug = "ubuntu-16-04-x64"
)

// Digitalocean definition, represents a concrete implementation of an iaas
type Digitalocean struct {
	Client            *libmachine.Client
	Host              *host.Host
	Region            string
	Size              string
	ImageSlug         string
	KeyID             int
	Ctx               context.Context
	sshPublicKeyPath  string
	sshPrivateKeyPath string
}

type driverConfig struct {
	Driver struct {
		DropletID   int    `json:"DropletID"`
		DropletName string `json:"DropletName"`
		IPAddress   string `json:"IPAddress"`
		Image       string `json:"Image"`
		SSHKeyID    int    `json:"SSHKeyID"`
	} `json:"Driver"`
}

// SetSSHPublicKeyPath adjust the system path for the ssh key
// but if the environment variable GOFN_SSH_PUBLICKEY_PATH exists
// the system will use the value contained in the variable instead
// of the one entered in SetSSHPublicKeyPath
func (do *Digitalocean) SetSSHPublicKeyPath(path string) {
	do.sshPublicKeyPath = path
}

// SetSSHPrivateKeyPath adjust the system path for the ssh key
// but if the environment variable GOFM_SSH_PRIVATEKEY_PATH exists
// the system will use the value contained in the variable instead
// of the one entered in SetSSHPrivateKeyPath
func (do *Digitalocean) SetSSHPrivateKeyPath(path string) {
	do.sshPrivateKeyPath = path
}

// GetSSHPublicKeyPath the path may change according to the
// environment variable GOFN_SSH_PUBLICKEY_PATH or by using
// the SetSSHPublicKeyPath
func (do *Digitalocean) GetSSHPublicKeyPath() (path string) {
	path = os.Getenv("GOFN_SSH_PUBLICKEY_PATH")
	if path != "" {
		return
	}
	path = do.sshPublicKeyPath
	if path != "" {
		return
	}
	do.sshPublicKeyPath = filepath.Join(gofnssh.KeysDir, gofnssh.PublicKeyName)
	path = do.sshPublicKeyPath
	return
}

// GetSSHPrivateKeyPath the path may change according to the
// environment variable GOFM_SSH_PRIVATEKEY_PATH or by using
// the SetSSHPrivateKeyPath
func (do *Digitalocean) GetSSHPrivateKeyPath() (path string) {
	path = os.Getenv("GOFN_SSH_PRIVATEKEY_PATH")
	if path != "" {
		return
	}
	path = do.sshPrivateKeyPath
	if path != "" {
		return
	}
	do.sshPrivateKeyPath = filepath.Join(gofnssh.KeysDir, gofnssh.PrivateKeyName)
	path = do.sshPrivateKeyPath
	return
}

// GetRegion returns region or default if empty
func (do Digitalocean) GetRegion() string {
	if do.Region == "" {
		return defaultRegion
	}
	return do.Region
}

// GetSize returns size or default if empty
func (do Digitalocean) GetSize() string {
	if do.Size == "" {
		return defaultSize
	}
	return do.Size
}

// GetImageSlug returns image slug  or default if empty
func (do Digitalocean) GetImageSlug() string {
	if do.ImageSlug == "" {
		return defaultImageSlug
	}
	return do.ImageSlug
}

func getConfig(machineDir, hostName string) (config *driverConfig, err error) {
	configPath := fmt.Sprintf("%s/%s/config.json", machineDir, hostName)
	raw, err := ioutil.ReadFile(configPath)
	if err != nil {
		return
	}
	err = json.Unmarshal(raw, &config)
	if err != nil {
		return
	}
	return
}

// CreateMachine on digitalocean
func (do *Digitalocean) CreateMachine() (machine *iaas.Machine, err error) {
	var uid uuid.UUID
	uid, err = uuid.NewV4()
	if err != nil {
		return
	}

	clientPath := fmt.Sprintf("/tmp/gofn-%s", uid.String())
	do.Client = libmachine.NewClient(clientPath, clientPath+"/certs")

	hostName := fmt.Sprintf("gofn-%s", uid.String())
	driver := digitalocean.NewDriver(hostName, clientPath)
	key := os.Getenv("DIGITALOCEAN_API_KEY")
	if key == "" {
		err = errors.New("You must provide a Digital Ocean API Key")
		return
	}
	driver.AccessToken = key
	driver.Size = do.GetSize()
	driver.Region = do.GetRegion()
	driver.Image = do.GetImageSlug()

	data, err := json.Marshal(driver)
	if err != nil {
		return
	}

	do.Host, err = do.Client.NewHost("digitalocean", data)
	if err != nil {
		return
	}

	err = do.Client.Create(do.Host)
	if err != nil {
		return
	}
	config, err := getConfig(do.Client.Filestore.GetMachinesDir(), hostName)
	if err != nil {
		return
	}

	machine = &iaas.Machine{
		ID:        strconv.Itoa(config.Driver.DropletID),
		IP:        config.Driver.IPAddress,
		Image:     config.Driver.Image,
		Kind:      "digitalocean",
		Name:      config.Driver.DropletName,
		SSHKeysID: []int{config.Driver.SSHKeyID},
		CertsDir:  clientPath + "/certs",
	}
	return
}

// DeleteMachine Shutdown and Delete a droplet
func (do *Digitalocean) DeleteMachine(machine *iaas.Machine) (err error) {
	err = do.Host.Driver.Remove()
	defer do.Client.Close()
	if err != nil {
		return
	}
	return
}

// ExecCommand on droplet
func (do *Digitalocean) ExecCommand(machine *iaas.Machine, cmd string) (output []byte, err error) {
	out, err := do.Host.RunSSHCommand(cmd)
	if err != nil {
		return
	}
	output = []byte(out)
	return
}
