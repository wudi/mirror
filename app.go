package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v7"
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
	// File  =>  Cached Info
	Cached   map[string]SaveCached   `json:"cached"`
	Metadata map[string]SaveMetadata `json:"metadata"`
}

type SaveCached struct {
	ETag         string `json:"etag"`
	LastModified string `json:"lastModified"`
}

type SaveMetadata struct {
	URL  string `json:"url"`
	Type string `json:"type"`
}

func run(cfg Config) (err error) {

	if _, err = os.Stat(cfg.DataDir + "/404.json"); err == nil {
		if err = fileGetContents(cfg.DataDir+"/404.json", &Error404Data); err != nil {
			return
		}
	}

	if _, err = os.Stat(cfg.DataDir + "/dist.json"); err == nil {
		if err = fileGetContents(cfg.DataDir+"/dist.json", &DistData); err != nil {
			return
		}
	}

	var mainPack *MainPackage
	if mainPack, err = fetchMainResponse(cfg); err != nil {
		return
	}
	incs := mainPack.ProviderIncludeURLs()

	go func() {
		tk := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-tk.C:
				log.Printf("remaining tasks: %d\n", Remaining.Load())
			}
		}
	}()

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

	if err = saveDistData(cfg); err != nil {
		log.Printf("saveDistData err: %s\n", err)
		return
	}

	if err = filePutContents(cfg.DataDir+"/404.json", Error404Data); err != nil {
		log.Printf("filePutContents 404.json err: %s\n", err)
		return
	}

	if err = filePutContents(cfg.DataDir+"/dist.json", DistData); err != nil {
		log.Printf("filePutContents dist.json err: %s\n", err)
		return
	}

	mainPack.Mirrors = []Mirror{
		{
			DistURL:   cfg.DistURL,
			Preferred: true,
		},
	}
	mainPack.MetadataURL = cfg.MetadataURL
	mainPack.ProvidersURL = cfg.ProvidersURL
	mainPack.Time = time.Now()
	if err = filePutContents(cfg.DataDir+"/packages.json", mainPack); err != nil {
		log.Printf("filePutContents packages.json err: %s\n", err)
		return
	}

	return nil
}

func processProvider(mainPack *MainPackage, cfg Config, providerUrl string) (err error) {
	var reader io.ReadCloser
	var req *http.Request

	if req, err = http.NewRequest(http.MethodGet, cfg.fullUrl(providerUrl), nil); err != nil {
		return
	}

	if _, reader, err = httpGet(cfg, req); err != nil {
		return
	}
	defer reader.Close()

	var data []byte
	if data, err = ioutil.ReadAll(reader); err != nil {
		return
	}

	var providerIncs ProviderIncludes
	if err = json.Unmarshal(data, &providerIncs); err != nil {
		return
	}

	wp := workpool.New(cfg.Concurrency)
	lock404.Lock()
	var i = 0
	for name, h := range providerIncs.Providers {
		if _, ok := Error404Data[name]; ok {
			continue
		}
		Remaining.Add(2)

		mUrl := strings.ReplaceAll(mainPack.MetadataURL, "%package%", name)
		pUrl := strings.ReplaceAll(strings.ReplaceAll(mainPack.ProvidersURL, "%package%", name), "%hash%", h.SHA256)

		// metadata
		wp.Do(doWorker(cfg, name, mUrl, ""))
		// providerIncs
		wp.Do(doWorker(cfg, name, pUrl, h.SHA256))

		if i++; i > 10 {
			break
		}
	}
	lock404.Unlock()
	wp.Wait()

	if cfg.Dump {
		if err = filePutContents(cfg.DataDir+providerUrl, data); err != nil {
			return
		}
	}

	return nil
}

func doWorker(cfg Config, name, url, sum string) workpool.TaskHandler {
	return func() error {
		start := time.Now()

		var err error
		defer func() {
			Remaining.Sub(1)
			if err == ErrNotFound {
				lock404.Lock()
				Error404Data[name] = true
				lock404.Unlock()
			}
			if cfg.Verbose {
				log.Printf("url: %s latency: %s err: %s\n", url, time.Now().Sub(start), err)
			}
		}()

		var rsp *http.Response
		var req *http.Request
		var reader io.ReadCloser
		if req, err = http.NewRequest("GET", cfg.fullUrl(url), nil); err != nil {
			return nil
		}

		lockDist.Lock()
		// Cached
		if v, ok := DistData[name]; ok {
			if cached, ok := v.Cached[url]; ok {
				if cached.ETag != "" {
					req.Header.Set("If-None-Match", cached.ETag)
				}

				if cached.LastModified != "" {
					req.Header.Set("If-Modified-Since", cached.LastModified)
				}
			}
		}
		lockDist.Unlock()

		if rsp, reader, err = httpGet(cfg, req); err != nil {
			log.Printf("name: %s err: %s\n", name, err)
			return nil
		}
		defer reader.Close()

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

		var data []byte
		if data, err = ioutil.ReadAll(reader); err != nil {
			return nil
		}

		// checksum
		if len(sum) > 0 {
			if !sha256Checksum(data, sum) {
				log.Printf("%s sha256 check failed. expect: %s", url, sum)
				// Do nothing
				return nil
			}
		}

		if cfg.Dump {
			if err = filePutContents(cfg.DataDir+url, data); err != nil {
				return nil
			}
		}

		var m Metadata

		if len(sum) == 0 {
			if err = json.Unmarshal(data, &m); err != nil {
				log.Printf("ERROR %s => %s\n", url, err)
				return nil
			}
		} else {
			var p Provider
			if err = json.Unmarshal(data, &p); err != nil {
				log.Printf("ERROR %s => %s\n", url, err)
				return nil
			}

			m.Packages = map[string][]Package{}
			if v, ok := p.Packages[name]; ok {
				for _, v2 := range v {
					m.Packages[name] = append(m.Packages[name], v2)
				}
			}
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
				Cached:   map[string]SaveCached{},
				Metadata: map[string]SaveMetadata{},
			}
		}

		fmt.Println("--------cached: ", url)
		DistData[name].Cached[url] = SaveCached{
			ETag:         rsp.Header.Get("Etag"),
			LastModified: rsp.Header.Get("Last-Modified"),
		}

		for _, pkg := range pkgs {
			if pkg.Dist == nil || pkg.Dist.Type == "" {
				continue
			}

			DistData[name].Metadata[pkg.Dist.Reference] = SaveMetadata{
				URL:  pkg.Dist.URL,
				Type: pkg.Dist.Type,
			}
		}

		return nil
	}
}

func sha256Checksum(data []byte, sum string) bool {
	h := sha256.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil)) == sum
}

func fetchMainResponse(cfg Config) (pkg *MainPackage, err error) {
	log.Printf("fetch main packages.json: %s\n", cfg.getMainUrl())

	var req *http.Request
	var reader io.ReadCloser

	if req, err = http.NewRequest("GET", cfg.getMainUrl(), nil); err != nil {
		return
	}
	if _, reader, err = httpGet(cfg, req); err != nil {
		return
	}
	defer reader.Close()

	pkg = new(MainPackage)
	err = json.NewDecoder(reader).Decode(pkg)
	return
}

func httpGet(cfg Config, req *http.Request) (rsp *http.Response, reader io.ReadCloser, err error) {
	client := http.Client{}
	req.Header.Add("Accept-Encoding", "gzip")

	for i := 0; i < cfg.Attempts; i++ {
		if rsp, err = client.Do(req); err != nil {
			n := backoff(i + 1)
			log.Printf("%s backoff: %s\n", req.URL, n)
			time.Sleep(n)
			continue
		}

		switch rsp.Header.Get("Content-Encoding") {
		case "gzip":
			reader, err = gzip.NewReader(rsp.Body)
		default:
			reader = rsp.Body
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
	log.Printf("load file: %s into: %v\n", file, v)

	data, err := ioutil.ReadFile(file)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	return nil
}

func filePutContents(file string, v interface{}) (err error) {
	log.Printf("write file: %s\n", file)

	if p := strings.LastIndex(file, "/"); p > 0 {
		path := file[0:p]
		if _, err = os.Stat(path); os.IsNotExist(err) {
			log.Printf("create directory: %s\n", path)
			if err = os.MkdirAll(file[0:p], 0755); err != nil {
				return err
			}
		}
	}

	var data []byte
	if b, ok := v.([]byte); ok {
		data = b
	} else if s, ok := v.(string); ok {
		data = []byte(s)
	} else {
		if data, err = json.Marshal(v); err != nil {
			return err
		}
	}

	if err = ioutil.WriteFile(file, data, 0755); err != nil {
		return err
	}
	return nil
}

func saveDistData(cfg Config) (err error) {
	log.Println("save dist to redis")

	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
	})
	defer client.Close()

	for name, dist := range DistData {
		if len(dist.Metadata) == 0 {
			continue
		}
		values := make(map[string]interface{}, len(dist.Metadata))
		for v, meta := range dist.Metadata {
			s, _ := json.Marshal(meta)
			values[v] = string(s)
		}
		if err = client.HSet(name, values).Err(); err != nil {
			return
		}
	}
	return nil
}
