package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"

	"github.com/gorilla/mux"
)

// STORAGE holds a handle to our selected storage-method.
var STORAGE StorageHandler

func HealthHandler(res http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(res, "alive")
}

// GetHandler allows a blob to be retrieved by name.
// This is called with requests like `GET /blob/XXXXXX`.
func GetHandler(res http.ResponseWriter, req *http.Request) {
	var (
		status int
		err    error
	)
	defer func() {
		if nil != err {
			http.Error(res, err.Error(), status)
		}
	}()

	vars := mux.Vars(req)
	id := vars["id"]

	r, _ := regexp.Compile("^([a-z0-9]+)$")
	if !r.MatchString(id) {
		status = http.StatusInternalServerError
		err = errors.New("alphanumeric IDs only")
		return
	}

	if req.Method == "HEAD" {
		res.Header().Set("Connection", "close")
		if !STORAGE.Exists(id) {
			res.WriteHeader(http.StatusNotFound)
		}
		return
	}

	data, meta := STORAGE.Get(id)
	if data == nil {
		http.NotFound(res, req)
	} else {
		if meta != nil {
			for k, v := range meta {
				// Special case to set the content-type of the returned value.
				if k == "X-Mime-Type" {
					res.Header().Set(k, v)
					k = "Content-Type"
				}
				res.Header().Set(k, v)
			}
		}
		io.Copy(res, bytes.NewReader(*data))
	}
}

func MissingHandler(res http.ResponseWriter, req *http.Request) {
	res.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(res, "404 - content is not hosted here.")
}

func ListHandler(res http.ResponseWriter, req *http.Request) {
	list := STORAGE.Existing()
	mapB, _ := json.Marshal(list)
	fmt.Fprintf(res, string(mapB))
}

func UploadHandler(res http.ResponseWriter, req *http.Request) {
	var (
		status int
		err    error
	)
	defer func() {
		if nil != err {
			http.Error(res, err.Error(), status)
		}
	}()

	// Get the name of the blob to upload.
	vars := mux.Vars(req)
	id := vars["id"]

	// Ensure the ID is entirely alphanumeric, to prevent traversal attacks.
	r, _ := regexp.Compile("^([a-z0-9]+)$")
	if !r.MatchString(id) {
		err = errors.New("alphanumeric IDs only")
		status = http.StatusInternalServerError
		return
	}

	// Read the body of the request.
	content, err := ioutil.ReadAll(req.Body)
	if err != nil {
		err = errors.New("failed to read body")
		status = http.StatusInternalServerError
		return
	}

	if ok := STORAGE.Store(id, content); !ok {
		err = errors.New("failed to write to storage")
		status = http.StatusInternalServerError
		return
	}

	// Output the result.
	//  { "id": "foo",
	//   "size": 1234,
	//   "status": "ok",
	//  }
	out := fmt.Sprintf("{\"id\":\"%s\",\"status\":\"OK\",\"size\":%d}", id, len(content))
	fmt.Fprintf(res, string(out))
}

// blobServer is our entry-point to the sub-command.
func blobServer(options blobServerCmd) {
	// Create a storage system.
	STORAGE = new(FilesystemStorage)
	STORAGE.Setup(options.store)

	// See https://github.com/gorilla/mux.
	router := mux.NewRouter()
	router.HandleFunc("/alive", HealthHandler).Methods("GET")
	router.HandleFunc("/blob/{id}", GetHandler).Methods("GET")
	router.HandleFunc("/blob/{id}", GetHandler).Methods("HEAD")
	router.HandleFunc("/blob/{id}", UploadHandler).Methods("POST")
	router.HandleFunc("/blobs", ListHandler).Methods("GET")
	router.PathPrefix("/").HandlerFunc(MissingHandler)
	http.Handle("/", router)

	// Launch the server
	fmt.Printf("blob-server available at http://%s:%d/\nUploads will be written beneath: %s\n",
		options.host, options.port, options.store)
	log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%d", options.host, options.port), nil))
}
