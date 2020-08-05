package main

import (
	"encoding/json"
	"strings"
	"time"
)

type MainPackage struct {
	Time             time.Time `json:"time"`
	Mirrors          []Mirror  `json:"mirrors"`
	MetadataURL      string    `json:"metadata-url"`
	Notify           string    `json:"notify"`
	NotifyBatch      string    `json:"notify-batch"`
	Search           string    `json:"search"`
	List             string    `json:"list"`
	ProvidersApi     string    `json:"providers-api"`
	ProvidersURL     string    `json:"providers-url"`
	ProviderIncludes map[string]struct {
		SHA256 string `json:"sha256"`
	} `json:"provider-includes"`
}

type Mirror struct {
	DistURL   string `json:"dist-url"`
	Preferred bool   `json:"preferred"`
}

// https://packagist.org/p/provider-2013$04542b01d31a9ffa89744955aadb4d7cfe7bca61e1794406582cae4ed31fbb52.json
type ProviderIncludes struct {
	Providers map[string]struct {
		SHA256 string `json:"sha256"`
	} `json:"providers"`
}

type Metadata struct {
	Packages map[string][]Package `json:"packages"`
	Minified string               `json:"minified,omitempty"`
}

type Provider struct {
	Packages map[string]map[string]Package `json:"packages"`
}

// Ref: https://getcomposer.org/doc/04-schema.md
type Package struct {
	Name              string    `json:"name"`
	Version           string    `json:"version"`
	VersionNormalized string    `json:"version_normalized,omitempty"`
	Dist              *Dist     `json:"dist,omitempty"`
	Time              time.Time `json:"time"`
}

type Dist struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Reference string `json:"reference"`
	SHASum    string `json:"shasum,omitempty"`
}

type NullDist Dist

func (d *Dist) UnmarshalJSON(data []byte) (err error) {
	if data[0] != '{' || data[len(data)-1] != '}' {
		return nil
	}

	var dist NullDist
	if err = json.Unmarshal(data, &dist); err != nil {
		return
	}
	d.Type = dist.Type
	d.URL = dist.URL
	d.Reference = dist.Reference
	d.SHASum = dist.SHASum
	return
}

func (p *MainPackage) ProviderIncludeURLs() (urls []string) {
	for u, h := range p.ProviderIncludes {
		u = "/" + strings.ReplaceAll(u, "%hash%", h.SHA256)
		urls = append(urls, u)
	}
	return
}

func (p *ProviderIncludes) PackageURLs(metadataURL string) (names []string, urls []string, hashs []string) {
	for name, h := range p.Providers {
		u := strings.ReplaceAll(metadataURL, "%package%", name)
		urls = append(urls, u)
		names = append(names, name)
		hashs = append(hashs, h.SHA256)
	}
	return
}
