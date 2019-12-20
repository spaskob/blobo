// Storage-abstraction.
//
// This file contains an abstract interface for storing/retrieving data, as well as a concrete
// implementation for doing that over a local filesystem.
//
// It is possible that other users would be interested in storing data inside MySQL, Postgres,
// Redis, or similar.  To do that should involve only implementing the `StorageHandler` interface
// and changing the setup to construct a new instance.
//
// We allow "data" to be read or written, by ID.
//
// We also allow (optional) meta-data to be written/retrieved alongside the data.  The latter is
// saved as a JSON file, alongside the data.

package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// StorageHandler is the interface for a storage class.
type StorageHandler interface {

	// If you implement a database-based system the string here might be used to specify
	// host/user/password, etc.
	Setup(connection string)

	// Retrieve the contents of a blob by ID.
	//
	// The fetch will also return any (optional) key=value parameters which were stored when the
	// content was uploaded.
	Get(id string) (*[]byte, map[string]string)

	// Store some data against the given ID.
	Store(id string, data []byte) bool

	// Get all known IDs.
	Existing() []string

	// Does the given ID exist?
	Exists(id string) bool
}

// FilesystemStorage is a concrete type which implements the StorageHandler interface.
type FilesystemStorage struct {
	// cwd records whether we're chrooted or not
	cwd bool

	// prefix holds our prefix directory if we didn't chroot
	prefix string
}

// Setup method to ensure we have a data-directory.
func (fss *FilesystemStorage) Setup(connection string) {
	os.MkdirAll(connection, 0755)

	// We default to changing to the directory and chrooting But we can't do that when testing.
	if flag.Lookup("test.v") != nil {
		fss.cwd = false
		fss.prefix = connection
		return
	}

	syscall.Chdir(connection)
	syscall.Chroot(connection)
	fss.cwd = true
}

// Get the contents of a given ID.
func (fss *FilesystemStorage) Get(id string) (*[]byte, map[string]string) {
	// If we're not using the cwd we need to build up the complete path to the file.
	target := id
	if fss.cwd == false {
		target = filepath.Join(fss.prefix, id)
	}

	if _, err := os.Stat(target); os.IsNotExist(err) {
		return nil, nil
	}

	x, err := ioutil.ReadFile(target)
	if err != nil {
		return nil, nil
	}

	var meta map[string]string
	metaData, err := ioutil.ReadFile(target + ".json")
	if err == nil {
		meta = make(map[string]string)
		json.Unmarshal([]byte(metaData), &meta)
	}
	return &x, meta
}

// Store the specified data against the given file.
func (fss *FilesystemStorage) Store(id string, data []byte) bool {
	// If we're not using the cwd we need to build up the complete path to the file.
	target := id
	if fss.cwd == false {
		target = filepath.Join(fss.prefix, id)
	}
	if err := ioutil.WriteFile(target, data, 0644); err != nil {
		return false
	}
	return true
}

// Existing returns all known IDs.
//
// We assume we've been chdir() + chroot() into the data-directory so we just need to read the
// filenames we can find.
func (fss *FilesystemStorage) Existing() []string {
	var list []string

	target := "."
	if fss.cwd == false {
		target = fss.prefix
	}

	files, _ := ioutil.ReadDir(target)
	for _, f := range files {
		name := f.Name()

		if !strings.HasSuffix(name, ".json") {
			list = append(list, name)
		}
	}
	return list
}

// Exists tests whether the given ID exists (as a file).
func (fss *FilesystemStorage) Exists(id string) bool {
	target := id
	if fss.cwd == false {
		target = filepath.Join(fss.prefix, id)
	}

	if _, err := os.Stat(target); os.IsNotExist(err) {
		return false
	}
	return true
}
