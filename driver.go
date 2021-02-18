package main

import (
	"encoding/json"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/docker/go-plugins-helpers/volume"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	// VolumeDirMode sets the permissions for the volume directory
	VolumeDirMode	= 0700
	// VolumeFileMode sets permissions for the volume files
	VolumeFileMode  = 0600
)

type s3fsVolume struct {
	Name			string
	MountPoint		string
	CreatedAt		string
	RefCount		int
	// s3fs options
	Options			[]string
	Bucket			string
	AccessKeyID		string
	SecretAccessKey string
}

type s3fsDriver struct {
	mutex			*sync.Mutex
	volumes			map[string]*s3fsVolume
	volumePath		string
	statePath		string
}

func (v *s3fsVolume) setupOptions(options map[string]string) error {
	for key, val := range options {
		switch key {
		case "bucket":
			v.Bucket = val
		case "access_key_id":
			v.AccessKeyID = val
		case "secret_access_key":
			v.SecretAccessKey = val
		default:
			if key == "debug" {
				log.Infof("Ignoring debug option, as it breaks s3fs")
			} else if val != "" {
				v.Options = append(v.Options, key+"="+val)
			} else {
				v.Options = append(v.Options, key)
			}
		}
	}

	if v.Bucket == "" {
		return fmt.Errorf("'bucket' option required")
	}

	if (v.AccessKeyID == "") != (v.SecretAccessKey == "") {
		return fmt.Errorf("'access_key_id' and 'secret_access_key' option must be used together")
	}

	return nil
}


func newS3fsDriver(basePath string) (*s3fsDriver, error) {
	log.Infof("Creating a new driver instance %s", basePath)

	volumePath := filepath.Join(basePath, "volumes")
	statePath := filepath.Join(basePath, "state", "s3fs-state.json")

	if verr := os.MkdirAll(volumePath, VolumeDirMode); verr != nil {
		return nil, verr
	}

	log.Infof("Initialized driver, volumes='%s' state='%s", volumePath, statePath)

	driver := &s3fsDriver{
		volumes:		make(map[string]*s3fsVolume),
		volumePath:		volumePath,
		statePath:		statePath,
		mutex:			&sync.Mutex{},
	}

	data, err := ioutil.ReadFile(driver.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debugf("No state found at %s", driver.statePath)
		} else {
			return nil, err
		}
	} else {
		if err := json.Unmarshal(data, &driver.volumes); err != nil {
			return nil, err
		}
	}
	return driver, nil
}

func (d *s3fsDriver) saveState() {
	data, err := json.Marshal(d.volumes)
	if err != nil {
		log.Errorf("saveState failed %s", err)
		return
	}

	if err := ioutil.WriteFile(d.statePath, data, VolumeFileMode); err != nil {
		log.Errorf("Failed to write state %s to %s (%s)", data, d.statePath, err)
	}
}

// Driver API
func (d *s3fsDriver) Create(r *volume.CreateRequest) error {
	log.Debugf("Create Request %s", r)
	d.mutex.Lock()
	defer d.mutex.Unlock()

	vol, err := d.newVolume(r.Name)
	if err != nil {
		return err
	}

	if err := vol.setupOptions(r.Options); err != nil {
		return err
	}

	d.volumes[r.Name] = vol
	d.saveState()
	return nil
}

func (d *s3fsDriver) List() (*volume.ListResponse, error) {
	log.Debugf("List Request")

	var vols = []*volume.Volume{}
	for _, vol := range d.volumes {
		vols = append(vols,
			&volume.Volume{Name: vol.Name, Mountpoint: vol.MountPoint})
	}
	return &volume.ListResponse{Volumes: vols}, nil
}

func (d *s3fsDriver) Get(r *volume.GetRequest) (*volume.GetResponse, error) {
	log.Debugf("Get Request %s", r)

	vol, ok := d.volumes[r.Name]
	if !ok {
		msg := fmt.Sprintf("Failed to get volume %s because it doesn't exist", r.Name)
		log.Error(msg)
		return &volume.GetResponse{}, fmt.Errorf(msg)
	}

	return &volume.GetResponse{Volume:
		&volume.Volume{Name: vol.Name, Mountpoint: vol.MountPoint}}, nil
}

func (d *s3fsDriver) Remove(r *volume.RemoveRequest) error {
	log.Debugf("Remove Request %s", r)
	d.mutex.Lock()
	defer d.mutex.Unlock()

	vol, ok := d.volumes[r.Name]
	if !ok {
		msg := fmt.Sprintf("Failed to remove volume %s because it doesn't exist", r.Name)
		log.Error(msg)
		return fmt.Errorf(msg)
	}

	if vol.RefCount > 0 {
		msg := fmt.Sprintf("Can't remove volume %s because it is mounted by %d containers", vol.Name, vol.RefCount)
		log.Error(msg)
		return fmt.Errorf(msg)
	}

	if err := d.removeVolume(vol); err != nil {
		return err
	}

	delete(d.volumes, vol.Name)
	d.saveState()
	return nil
}

func (d *s3fsDriver) Path(r *volume.PathRequest) (*volume.PathResponse, error) {
	log.Debugf("Path Request %s", r)
	vol, ok := d.volumes[r.Name]
	if !ok {
		msg := fmt.Sprintf("Failed to find path for volume %s because it doesn't exist", r.Name)
		log.Error(msg)
		return &volume.PathResponse{}, fmt.Errorf(msg)
	}

	return &volume.PathResponse{Mountpoint: vol.MountPoint}, nil
}

func (d *s3fsDriver) Mount(r *volume.MountRequest) (*volume.MountResponse, error) {
	log.Debugf("Mount Request %s", r)
	d.mutex.Lock()
	defer d.mutex.Unlock()

	vol, ok := d.volumes[r.Name]
	if !ok {
		msg := fmt.Sprintf("Failed to mount volume %s because it doesn't exist", r.Name)
		log.Error(msg)
		return &volume.MountResponse{}, fmt.Errorf(msg)
	}

	if vol.RefCount == 0 {
		log.Debugf("First volume mount %s establish connection to %s", vol.Name, vol.Bucket)
		if err := d.mountVolume(vol); err != nil {
			msg := fmt.Sprintf("Failed to mount %s, %s", vol.Name, err)
			log.Error(msg)
			return &volume.MountResponse{}, fmt.Errorf(msg)
		}
	}
	vol.RefCount++
	d.saveState()
	return &volume.MountResponse{Mountpoint: vol.MountPoint}, nil
}

func (d *s3fsDriver) Unmount(r *volume.UnmountRequest) error {
	log.Debugf("Umount Request %s", r)
	d.mutex.Lock()
	defer d.mutex.Unlock()

	vol, ok := d.volumes[r.Name]
	if !ok {
		msg := fmt.Sprintf("Failed to unmount volume %s because it doesn't exist", r.Name)
		log.Error(msg)
		return fmt.Errorf(msg)
	}

	vol.RefCount--
	if vol.RefCount <= 0 {
		if err := d.unmountVolume(vol); err != nil {
			return err
		}
		vol.RefCount = 0
	}
	d.saveState()
	return nil
}

func (d *s3fsDriver) Capabilities() *volume.CapabilitiesResponse {
	log.Debugf("Capabilities Request")
	return &volume.CapabilitiesResponse{Capabilities: volume.Capability{Scope: "global"}}
}

// Helper methods

func (d *s3fsDriver) newVolume(name string) (*s3fsVolume, error) {
	path := filepath.Join(d.volumePath, name)
	err := os.MkdirAll(path, VolumeDirMode)
	if err != nil {
		msg := fmt.Sprintf("Failed to create the volume mount path %s (%s)", path, err)
		log.Error(msg)
		return nil, fmt.Errorf(msg)
	}

	vol := &s3fsVolume{
		Name: name,
		MountPoint: path,
		CreatedAt: time.Now().Format(time.RFC3339Nano),
		RefCount: 0,
	}
	return vol, nil
}

func (d *s3fsDriver) removeVolume(vol *s3fsVolume) error {
	// Remove MountPoint
	if  err := os.Remove(vol.MountPoint); err != nil {
		msg := fmt.Sprintf("Failed to remove the volume %s mountpoint %s (%s)", vol.Name, vol.MountPoint, err)
		log.Error(msg)
		return fmt.Errorf(msg)
	}

	return nil
}

func (d *s3fsDriver) mountVolume(vol *s3fsVolume) error {
	cmd := exec.Command("s3fs", vol.Bucket, vol.MountPoint)

	if vol.AccessKeyID != "" {
		cmd.Env = append(os.Environ(),
				"AWSACCESSKEYID="+vol.AccessKeyID,
				"AWSSECRETACCESSKEY="+vol.SecretAccessKey,
			)
	}

	// Append the rest
	for _, option := range vol.Options {
		cmd.Args = append(cmd.Args, "-o", option)
	}

	// Ensure that children have the same process pgid
	log.Debugf("Executing mount command %v", cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("s3fs command failed %v (%s)", err, output)
	}

	return nil
}

func (d *s3fsDriver) unmountVolume(vol *s3fsVolume) error {
	cmd := fmt.Sprintf("umount %s", vol.MountPoint)
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		return err
	}
	// Check that the mountpoint is empty
	files, err := ioutil.ReadDir(vol.MountPoint)
	if err != nil {
		return err
	}

	if len(files) > 0 {
		return fmt.Errorf("after unmount %d files still exists in %s", len(files), vol.MountPoint)
	}

	return nil
}
