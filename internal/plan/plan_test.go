package plan

import (
	"encoding/json"
	"testing"
)

func TestManifestJSONAlwaysWritesWhatOpsKeepsBetweenPackages(t *testing.T) {
	b, err := json.Marshal(Manifest{Program: "/web"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Program":"/web","RebootOnExit":false,"RunConfig":{"Ports":[],"UDPPorts":[]}}`
	if string(b) != want {
		t.Errorf("manifest = %s, want %s", b, want)
	}
}

func TestManifestJSONFull(t *testing.T) {
	m := Manifest{
		Program:         "/web",
		Args:            []string{"/web", "-x"},
		Env:             map[string]string{"A": "1"},
		RebootOnExit:    true,
		DisableArgsCopy: true,
		RunConfig:       RunConfig{Ports: []string{"80"}, UDPPorts: []string{"53"}},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Program":"/web","Args":["/web","-x"],"Env":{"A":"1"},"RebootOnExit":true,"DisableArgsCopy":true,"RunConfig":{"Ports":["80"],"UDPPorts":["53"]}}`
	if string(b) != want {
		t.Errorf("manifest = %s, want %s", b, want)
	}
}
