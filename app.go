package main

import (
	"compress/gzip"
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
	ETag         string                  `json:"etag"`
	LastModified string                  `json:"lastModified"`
	Metadata     map[string]SaveMetadata `json:"metadata"`
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

	//go func() {
	//	tk := time.NewTicker(5 * time.Second)
	//	for {
	//		select {
	//		case <-tk.C:
	//			log.Printf("remaining tasks: %d\n", Remaining.Load())
	//		}
	//	}
	//}()

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
		return
	}

	if err = filePutContents(cfg.DataDir+"/404.json", Error404Data); err != nil {
		return
	}

	if err = filePutContents(cfg.DataDir+"/dist.json", DistData); err != nil {
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
		return
	}

	return nil
}

func processProvider(mainPack *MainPackage, cfg Config, providerUrl string) (err error) {
	var rsp *http.Response
	rsp, err = http.Get(cfg.Mirror + providerUrl)
	if err != nil {
		return
	}
	defer rsp.Body.Close()

	var data []byte
	if data, err = ioutil.ReadAll(rsp.Body); err != nil {
		return
	}

	var provider Provider
	if err = json.Unmarshal(data, &provider); err != nil {
		return
	}
	names, urls, hashs := provider.PackageURLs(mainPack.MetadataURL)
	urls = urls[:10]

	log.Printf("provider: %s nums: %d", providerUrl, len(urls))
	Remaining.Add(int32(len(urls)))

	wp := workpool.New(30)
	for n, u := range urls {
		name := names[n]
		hash := hashs[n]
		if _, ok := Error404Data[name]; ok {
			continue
		}
		url := cfg.Mirror + u
		wp.Do(doWorker(cfg, mainPack, name, url, hash))
	}
	wp.Wait()

	if cfg.Dump {
		if err = filePutContents(cfg.DataDir+providerUrl, data); err != nil {
			return
		}
	}

	return nil
}

func doWorker(cfg Config, mainpack *MainPackage, name, url, hash string) workpool.TaskHandler {
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

		if rsp, reader, err = httpGet(cfg, req); err != nil {
			log.Printf("name: %s err: %s", name, err)
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

		if cfg.Dump {
			// metadata
			metadataFile := strings.ReplaceAll(mainpack.MetadataURL, "%package%", name)
			if err = filePutContents(cfg.DataDir+"/"+metadataFile, data); err != nil {
				return nil
			}

			// provider
			providersUrl := strings.ReplaceAll(strings.ReplaceAll(mainpack.ProvidersURL, "%package%", name), "%hash%", hash)
			if err = filePutContents(cfg.DataDir+"/"+providersUrl, data); err != nil {
				return nil
			}
		}

		var m Metadata
		if err = json.Unmarshal(data, &m); err != nil {
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
			log.Printf("%s backoff: %s", req.URL, n)
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
			log.Printf("create directory: %s", path)
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
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
	})
	defer client.Close()

	for name, dist := range DistData {
		values := map[string]interface{}{
			"etag":         dist.ETag,
			"lastModified": dist.LastModified,
		}
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
