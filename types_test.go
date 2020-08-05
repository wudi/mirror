package main

import (
	"encoding/json"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/assert"
)

type distTest struct {
	Dist *Dist `json:"dist"`
}

func TestDist(t *testing.T) {
	var a distTest
	var err error
	err = json.Unmarshal([]byte(`{"dist": {"type":"test"}}`), &a)
	assert.NoError(t, err)
	assert.Equal(t, "test", a.Dist.Type)

	var b distTest
	err = json.Unmarshal([]byte(`{"dist": "__unset"}`), &b)
	assert.NoError(t, err)
	assert.Equal(t, "", b.Dist.Type)
}

func TestMainPackage(t *testing.T) {
	b, err := ioutil.ReadFile("testdata/packages.json")
	assert.NoError(t, err)

	var mp MainPackage
	err = json.Unmarshal(b, &mp)
	assert.NoError(t, err)

	assert.Greater(t, len(mp.ProviderIncludes), 0)
}

func TestProvider(t *testing.T) {
	b, err := ioutil.ReadFile("testdata/provider.json")
	assert.NoError(t, err)

	var p ProviderIncludes
	err = json.Unmarshal(b, &p)
	assert.NoError(t, err)

	assert.Greater(t, len(p.Providers), 0)
}

func TestMetadata(t *testing.T) {
	b, err := ioutil.ReadFile("testdata/metadata.json")
	assert.NoError(t, err)

	var m Metadata
	err = json.Unmarshal(b, &m)
	assert.NoError(t, err)

	assert.Greater(t, len(m.Packages), 0)

	//b, err = json.MarshalIndent(m, "", "  ")
	//assert.NoError(t, err)
	//fmt.Println(string(b))
}
