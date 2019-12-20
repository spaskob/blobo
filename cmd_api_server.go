package main

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/skx/sos/libconfig"
)

var OPTIONS apiServerCmd

// Start the upload/download servers running.
func apiServer(options apiServerCmd) {
	// If we received blob-servers on the command-line use them too.
	// NOTE: blob-servers added on the command-line are placed in the
	// "default" group.
	if options.blob != "" {
		servers := strings.Split(options.blob, ",")
		for _, entry := range servers {
			libconfig.AddServer("default", entry)
		}
	} else {
		libconfig.InitServers()
	}

	if options.dump {
		fmt.Printf("\t% 10s - %s\n", "group", "server")
		for _, entry := range libconfig.Servers() {
			fmt.Printf("\t% 10s - %s\n", entry.Group, entry.Location)
		}
		return
	}

	OPTIONS = options

	fmt.Printf("[Launching API-server]\n")
	fmt.Printf("\nUpload service\nhttp://%s:%d/upload\n", options.host, options.uport)
	fmt.Printf("\nDownload service\nhttp://%s:%d/fetch/:id\n", options.host, options.dport)

	fmt.Printf("\nBlob-servers:\n")
	fmt.Printf("\t% 10s - %s\n", "group", "server")
	for _, entry := range libconfig.Servers() {
		fmt.Printf("\t% 10s - %s\n", entry.Group, entry.Location)
	}
	fmt.Printf("\n")

	upRouter := mux.NewRouter()
	upRouter.HandleFunc("/upload", APIUploadHandler).Methods("POST")
	upRouter.PathPrefix("/").HandlerFunc(APIMissingHandler)

	downRouter := mux.NewRouter()
	downRouter.HandleFunc("/fetch/{id}", APIDownloadHandler).Methods("GET")
	downRouter.HandleFunc("/fetch/{id}", APIDownloadHandler).Methods("HEAD")
	downRouter.PathPrefix("/").HandlerFunc(APIMissingHandler)

	// The following code is a hack to allow us to run two distinct HTTP-servers on different ports.
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%d", options.host, options.uport), upRouter))
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%d", options.host, options.dport), downRouter))
		wg.Done()
	}()
	wg.Wait()
}

// This is a helper for allowing us to consume a HTTP-body more than once.
type myReader struct {
	*bytes.Buffer
}

// So that it implements the io.ReadCloser interface
func (m myReader) Close() error { return nil }

// APIUploadHandler handles uploads to the API server.
//
// This should attempt to upload against the blob-servers and return when that is complete.  If
// there is a failure then it should repeat the process until all known servers are exhausted.
//
// The retry logic is described in the file `SCALING.md` in the repository, but in brief there are
// two cases:
//  * All the servers are in the group `default`.
//  * There are N defined groups.
//
// Both cases are handled by the call to OrderedServers() which returns the known blob-servers in a
// suitable order to minimize lookups.  See `SCALING.md` for more details.
func APIUploadHandler(res http.ResponseWriter, req *http.Request) {
	buf, _ := ioutil.ReadAll(req.Body)

	// Create a copy of the buffer, so that we can consume it initially to hash the data.
	rdr1 := myReader{bytes.NewBuffer(buf)}
	hasher := sha1.New()
	b, _ := ioutil.ReadAll(rdr1)
	hasher.Write([]byte(b))
	hash := hasher.Sum(nil)

	// We try each blob-server in turn, and if/when we receive a successful result we'll return it to
	// the caller.
	for _, s := range libconfig.OrderedServers() {
		rdr2 := myReader{bytes.NewBuffer(buf)}
		req.Body = rdr2

		// This is where we'll POST to.
		url := fmt.Sprintf("%s%s%x", s.Location, "/blob/", hash)
		child, _ := http.NewRequest("POST", url, req.Body)
		client := &http.Client{}
		r, err := client.Do(child)
		if err != nil {
			continue
		}

		response, _ := ioutil.ReadAll(r.Body)
		if response != nil {
			fmt.Fprintf(res, string(response))
			return
		}
	}
	res.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(res, "{\"error\":\"upload failed\"}")
	return
}

// APIDownloadHandler handles downloads from the API server.
//
// This should attempt to download against the blob-servers and return when that is complete.  If
// there is a failure then it should repeat the process until all known servers are exhausted..
//
// The retry logic is described in the file `SCALING.md` in the repository, but in brief there are
// two cases:
//  * All the servers are in the group `default`.
//  * There are N defined groups.
//
// Both cases are handled by the call to OrderedServers() which returns the known blob-servers in a
// suitable order to minimize lookups.  See `SCALING.md` for more details.
func APIDownloadHandler(res http.ResponseWriter, req *http.Request) {
	// The ID of the file we're to retrieve.
	vars := mux.Vars(req)
	id := vars["id"]

	// Strip any extension which might be present on the ID.
	extension := filepath.Ext(id)
	id = id[0 : len(id)-len(extension)]

	for _, s := range libconfig.OrderedServers() {
		if OPTIONS.verbose {
			fmt.Printf("Attempting retrieval from %s%s%s\n", s.Location, "/blob/", id)
		}

		response, err := http.Get(fmt.Sprintf("%s%s%s", s.Location, "/blob/", id))
		if err != nil || response.StatusCode != 200 {
			if err != nil && OPTIONS.verbose {
				fmt.Printf("\tError fetching: %s\n", err.Error())
			} else {
				// If there was no error then the HTTP-connection to the back-end succeeded, but that didn't
				// return a 200 OK. This might happen if a file was uploaded to only one host, but we've hit
				// another.
				if OPTIONS.verbose {
					fmt.Printf("\tStatus Code : %d\n", response.StatusCode)
				}
			}
		} else {
			body, _ := ioutil.ReadAll(response.Body)
			if body != nil {
				if OPTIONS.verbose {
					fmt.Printf("\tFound, read %d bytes\n", len(body))
				}
				if req.Method == "HEAD" {
					res.Header().Set("Connection", "close")
					res.WriteHeader(http.StatusOK)
					return
				}
				io.Copy(res, bytes.NewReader(body))
				return
			}
		}
	}
	res.Header().Set("Connection", "close")
	res.WriteHeader(http.StatusNotFound)
}

// APIMissingHandler is a fall-back handler for all requests which are neither upload nor download.
func APIMissingHandler(res http.ResponseWriter, req *http.Request) {
	res.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(res, "Invalid method or location.")
}
