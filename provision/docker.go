package provision

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/gofn/gofn/iaas"
	"github.com/gofrs/uuid"
)

var (
	// ErrImageNotFound is raised when image is not found
	ErrImageNotFound = errors.New("provision: image not found")

	// ErrContainerNotFound is raised when image is not found
	ErrContainerNotFound = errors.New("provision: container not found")

	// ErrContainerExecutionFailed is raised if container exited with status different of zero
	ErrContainerExecutionFailed = errors.New("provision: container exited with failure")

	// Input receives a string that will be written to the stdin of the container in function FnRun
	Input string
)

// BuildOptions are options used in the image build
type BuildOptions struct {
	ContextDir              string
	Dockerfile              string
	DoNotUsePrefixImageName bool
	ImageName               string
	RemoteURI               string
	StdIN                   string
	Iaas                    iaas.Iaas
	Auth                    docker.AuthConfiguration
	ForcePull               bool
}

// ContainerOptions are options used in container
type ContainerOptions struct {
	Cmd     []string
	Volumes []string
	Image   string
	Env     []string
	Runtime string
}

// GetImageName sets prefix gofn when needed
func (opts BuildOptions) GetImageName() string {
	if opts.DoNotUsePrefixImageName {
		return opts.ImageName
	}
	return path.Join("gofn", opts.ImageName)
}

// FnRemove remove container
func FnRemove(client *docker.Client, containerID string) (err error) {
	err = client.RemoveContainer(docker.RemoveContainerOptions{ID: containerID, Force: true})
	return
}

// FnContainer create container
func FnContainer(client *docker.Client, opts ContainerOptions) (container *docker.Container, err error) {
	config := &docker.Config{
		Image:     opts.Image,
		Cmd:       opts.Cmd,
		Env:       opts.Env,
		StdinOnce: true,
		OpenStdin: true,
	}
	var uid uuid.UUID
	uid, err = uuid.NewV4()
	if err != nil {
		return
	}
	container, err = client.CreateContainer(docker.CreateContainerOptions{
		Name:       fmt.Sprintf("gofn-%s", uid.String()),
		HostConfig: &docker.HostConfig{Binds: opts.Volumes, Runtime: opts.Runtime},
		Config:     config,
	})
	return
}

// FnImageBuild builds an image
func FnImageBuild(client *docker.Client, opts *BuildOptions) (Name string, Stdout *bytes.Buffer, err error) {
	if opts.Dockerfile == "" {
		opts.Dockerfile = "Dockerfile"
	}
	if opts.ContextDir == "" && opts.RemoteURI == "" {
		opts.ContextDir = "./"
	}
	err = auth(client, opts)
	if err != nil {
		return
	}
	stdout := new(bytes.Buffer)
	Name = opts.GetImageName()
	if opts.ForcePull {
		err = FnPull(client, opts)
		return
	}
	err = client.BuildImage(docker.BuildImageOptions{
		Name:           Name,
		Dockerfile:     opts.Dockerfile,
		SuppressOutput: true,
		OutputStream:   stdout,
		ContextDir:     opts.ContextDir,
		Remote:         opts.RemoteURI,
		Auth:           opts.Auth,
	})
	if err != nil {
		if !strings.Contains(err.Error(), "Cannot locate specified Dockerfile:") { // the error is not exported so we need to verify using the message
			return
		}
		err = FnPull(client, opts)
		if err != nil {
			return
		}
	}
	Stdout = stdout
	return
}

func auth(client *docker.Client, opts *BuildOptions) (err error) {
	if (opts.Auth.Email != "" || opts.Auth.Username != "") && opts.Auth.Password != "" {
		if opts.Auth.ServerAddress == "" {
			opts.Auth.ServerAddress = "https://index.docker.io/v1/"
		}
		var status docker.AuthStatus
		status, err = client.AuthCheck(&opts.Auth)
		if err != nil {
			return
		}
		opts.Auth.IdentityToken = status.IdentityToken
	}
	return
}

// FnPull pull image from registry
func FnPull(client *docker.Client, opts *BuildOptions) (err error) {
	repo, tag := parseDockerImage(opts.GetImageName())
	err = client.PullImage(docker.PullImageOptions{
		Repository: repo,
		Tag:        tag,
	}, opts.Auth)
	return
}

func parseDockerImage(image string) (repo, tag string) {
	repo, tag = docker.ParseRepositoryTag(image)
	if tag != "" {
		return repo, tag
	}
	if i := strings.IndexRune(image, '@'); i > -1 { // Has digest (@sha256:...)
		// when pulling images with a digest, the repository contains the sha hash, and the tag is empty
		// see: https://github.com/fsouza/go-dockerclient/blob/master/image_test.go#L471
		repo = image
	} else {
		tag = "latest"
	}
	return repo, tag
}

// FnFindImage returns image data by name
func FnFindImage(client *docker.Client, imageName string) (image docker.APIImages, err error) {
	var imgs []docker.APIImages
	imgs, err = client.ListImages(docker.ListImagesOptions{Filter: imageName})
	if err != nil {
		return
	}
	if len(imgs) == 0 {
		err = ErrImageNotFound
		return
	}
	image = imgs[0]
	return
}

// FnFindContainerByID return container by ID
func FnFindContainerByID(client *docker.Client, ID string) (container docker.APIContainers, err error) {
	var containers []docker.APIContainers
	containers, err = client.ListContainers(docker.ListContainersOptions{All: true})
	if err != nil {
		return
	}
	for _, v := range containers {
		if v.ID == ID {
			container = v
			return
		}
	}
	err = ErrContainerNotFound
	return
}

// FnFindContainer return container by image name
func FnFindContainer(client *docker.Client, imageName string) (container docker.APIContainers, err error) {
	var containers []docker.APIContainers
	containers, err = client.ListContainers(docker.ListContainersOptions{All: true})
	if err != nil {
		return
	}

	if !strings.HasPrefix(imageName, "gofn") {
		imageName = "gofn/" + imageName
	}

	for _, v := range containers {
		if v.Image == imageName {
			container = v
			return
		}
	}
	err = ErrContainerNotFound
	return
}

// FnKillContainer kill the container
func FnKillContainer(client *docker.Client, containerID string) (err error) {
	err = client.KillContainer(docker.KillContainerOptions{ID: containerID})
	return
}

//FnAttach attach into a running container
func FnAttach(client *docker.Client, containerID string, stdin io.Reader, stdout io.Writer, stderr io.Writer) (w docker.CloseWaiter, err error) {
	return client.AttachToContainerNonBlocking(docker.AttachToContainerOptions{
		Container:    containerID,
		RawTerminal:  true,
		Stream:       true,
		Stdin:        true,
		Stderr:       true,
		Stdout:       true,
		Logs:         true,
		InputStream:  stdin,
		ErrorStream:  stderr,
		OutputStream: stdout,
	})
}

// FnStart start the container
func FnStart(client *docker.Client, containerID string) error {
	return client.StartContainer(containerID, nil)
}

// FnRun runs the container
func FnRun(client *docker.Client, containerID, input string) (Stdout *bytes.Buffer, Stderr *bytes.Buffer, err error) {
	err = FnStart(client, containerID)
	if err != nil {
		return
	}

	// attach to write input
	_, err = FnAttach(client, containerID, strings.NewReader(input), nil, nil)
	if err != nil {
		return
	}

	e := FnWaitContainer(client, containerID)
	err = <-e

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	// omit logs because execution error is more important
	_ = FnLogs(client, containerID, stdout, stderr) // nolint

	Stdout = stdout
	Stderr = stderr
	return
}

// FnLogs logs all container activity
func FnLogs(client *docker.Client, containerID string, stdout io.Writer, stderr io.Writer) error {
	return client.Logs(docker.LogsOptions{
		Container:    containerID,
		Stdout:       true,
		Stderr:       true,
		ErrorStream:  stderr,
		OutputStream: stdout,
	})
}

// FnWaitContainer wait until container finnish your processing
func FnWaitContainer(client *docker.Client, containerID string) chan error {
	errs := make(chan error)
	go func() {
		code, err := client.WaitContainer(containerID)
		if err != nil {
			errs <- err
		}
		if code != 0 {
			errs <- ErrContainerExecutionFailed
		}
		errs <- nil
	}()
	return errs
}

// FnListContainers lists all the containers created by the gofn.
// It returns the APIContainers from the API, but have to be formatted for pretty printing
func FnListContainers(client *docker.Client) (containers []docker.APIContainers, err error) {
	hostContainers, err := client.ListContainers(docker.ListContainersOptions{
		All: true,
	})
	if err != nil {
		containers = nil
		return
	}
	for _, container := range hostContainers {
		if strings.HasPrefix(container.Image, "gofn/") {
			containers = append(containers, container)
		}
	}
	return
}
