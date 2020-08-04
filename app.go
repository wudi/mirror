package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/xxjwxc/gowp/workpool"
	"go.uber.org/atomic"
)

var (
	Error404Data = make(map[string]bool)
	lock404      sync.Mutex

	DistData = make(map[string]SavePackage)
	lockDist sync.Mutex

	Remaining atomic.Int32

	ErrNotFound = errors.New("package not found")
)

type SavePackage struct {
	ETag         string                  `json:"etag"`
	LastModified string                  `json:"lastModified"`
	Metadata     map[string]SaveMetadata `json:"metadata"`
}

type SaveMetadata struct {
	URL  string `json:"url"`
	Type string `json:"type"`
}

func run(cfg Config) (err error) {
	if err = fileGetContents("404.json", &Error404Data); err != nil {
		return
	}

	if err = fileGetContents("dist.json", &DistData); err != nil {
		return
	}

	var mainPack *MainPackage
	if mainPack, err = fetchMainResponse(cfg); err != nil {
		return
	}
	incs := mainPack.ProviderIncludeURLs()

	var wg sync.WaitGroup
	wg.Add(len(incs))
	for _, u := range incs {
		go func(u string) {
			defer func() {
				wg.Done()
				log.Printf("DONE %s\n", u)
			}()
			if err := processProvider(mainPack, cfg, u); err != nil {
				log.Println(err)
			}
		}(u)
	}
	wg.Wait()

	if err = filePutContents("404.json", Error404Data); err != nil {
		return
	}

	if err = filePutContents("dist.json", DistData); err != nil {
		return
	}

	return nil
}

func processProvider(mainPack *MainPackage, cfg Config, purl string) (err error) {
	var rsp *http.Response
	rsp, err = http.Get(cfg.Mirror + purl)
	if err != nil {
		return
	}

	var provider Provider
	if err = json.NewDecoder(rsp.Body).Decode(&provider); err != nil {
		return
	}
	names, urls := provider.PackageURLs(mainPack.MetadataURL)
	log.Printf("provider: %s nums: %d", purl, len(urls))
	Remaining.Add(int32(len(urls)))

	wp := workpool.New(30)
	for n, u := range urls {
		name := names[n]
		if _, ok := Error404Data[name]; ok {
			continue
		}
		url := cfg.Mirror + u
		wp.Do(doWorker(cfg, name, url))
	}
	wp.Wait()

	return nil
}

func doWorker(cfg Config, name, url string) workpool.TaskHandler {
	return func() error {
		start := time.Now()

		var err error
		defer func() {
			Remaining.Sub(1)
			if err != nil {
				if err == ErrNotFound {
					lock404.Lock()
					Error404Data[name] = true
					lock404.Unlock()
				}

				if cfg.Verbose {
					latency := time.Now().Sub(start)
					log.Printf("url: %s latency: %s err: %s\n", url, latency, err)
				}
			}
		}()

		var rsp *http.Response
		var req *http.Request
		if req, err = http.NewRequest("GET", url, nil); err != nil {
			return nil
		}

		lockDist.Lock()
		if v, ok := DistData[name]; ok {
			if v.ETag != "" {
				req.Header.Set("If-None-Match", v.ETag)
			}

			if v.LastModified != "" {
				req.Header.Set("If-Modified-Since", v.LastModified)
			}
		}
		lockDist.Unlock()

		if rsp, err = httpGet(cfg, req); err != nil {
			log.Printf("name: %s err: %s", name, err)
			return nil
		}
		defer rsp.Body.Close()

		if rsp.StatusCode == http.StatusNotFound {
			err = ErrNotFound
			return nil
		}

		if rsp.StatusCode == http.StatusNotModified {
			//log.Printf("%s not modified", name)
			return nil
		}

		if rsp.StatusCode != http.StatusOK {
			err = fmt.Errorf("http status: %d", rsp.StatusCode)
			return nil
		}

		if ct := rsp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			err = fmt.Errorf("invalid content type: %s", ct)
			return nil
		}

		var m Metadata
		if err = json.NewDecoder(rsp.Body).Decode(&m); err != nil {
			return nil
		}

		var pkgs []Package
		var ok bool

		if pkgs, ok = m.Packages[name]; !ok {
			err = fmt.Errorf("no package named: %s in: %v", name, m.Packages)
			return nil
		}
		lockDist.Lock()
		defer lockDist.Unlock()

		if _, ok := DistData[name]; !ok {
			DistData[name] = SavePackage{
				ETag:         rsp.Header.Get("Etag"),
				LastModified: rsp.Header.Get("Last-Modified"),
				Metadata:     map[string]SaveMetadata{},
			}
		}

		for _, pkg := range pkgs {
			if pkg.Dist != nil && pkg.Dist.Type != "" {
				DistData[name].Metadata[pkg.Dist.Reference] = SaveMetadata{
					URL:  pkg.Dist.URL,
					Type: pkg.Dist.Type,
				}
			}
		}

		return nil
	}
}

func fetchMainResponse(cfg Config) (pkg *MainPackage, err error) {
	var req *http.Request
	var rsp *http.Response
	if req, err = http.NewRequest("GET", cfg.getMainUrl(), nil); err != nil {
		return
	}
	if rsp, err = httpGet(cfg, req); err != nil {
		return
	}
	defer rsp.Body.Close()

	pkg = new(MainPackage)
	err = json.NewDecoder(rsp.Body).Decode(pkg)
	return
}

func httpGet(cfg Config, req *http.Request) (rsp *http.Response, err error) {
	client := http.Client{}
	for i := 0; i < cfg.Attempts; i++ {
		if rsp, err = client.Do(req); err != nil {
			time.Sleep(backoff(i + 1))
			continue
		}
		break
	}
	return
}

func backoff(attempts int) time.Duration {
	if attempts > 13 {
		return 2 * time.Minute
	}
	return time.Duration(math.Pow(float64(attempts), math.E)) * time.Millisecond * 100
}

func fileGetContents(file string, v interface{}) error {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	return nil
}

func filePutContents(file string, v interface{}) error {
	fp, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0)
	if err != nil {
		return err
	}
	defer fp.Close()

	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = fp.Write(data); err != nil {
		return err
	}
	return nil
}
