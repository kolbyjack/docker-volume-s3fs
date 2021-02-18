package main

import (
	log "github.com/Sirupsen/logrus"
	"github.com/docker/go-plugins-helpers/volume"
	"os"
	"strconv"
)

const (
	// DefaultBasePath defines the base path within the docker plugins rootfs file system
	DefaultBasePath = "/mnt"
	// DefaultUnixSocket sets the path to the plugin socket
	DefaultUnixSocket = "/run/docker/plugins/s3fs.sock"
)

func main() {
	debug := os.Getenv("DEBUG")
	if ok, _ := strconv.ParseBool(debug); ok {
		log.SetLevel(log.DebugLevel)
	}

	driver, err := newS3fsDriver(DefaultBasePath)
	if err != nil {
		log.Errorf("Failed to create the driver %s", err)
		os.Exit(1)
	}

	handler := volume.NewHandler(driver)
	handler.ServeUnix(DefaultUnixSocket, 0)
}