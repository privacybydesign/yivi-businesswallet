package openid4vppresenter

import (
	"reflect"
	"testing"
)

func TestRequestedCredentialsSummarizesTheQuery(t *testing.T) {
	raw := []byte(`{"credentials":[
		{"id":"a","format":"dc+sd-jwt","meta":{"vct_values":["nl.kvk.registration"]},
		 "claims":[{"path":["legalName"]},{"path":["address","city"]},{"path":["directors",null,"name"]},{"path":["codes",0]}]},
		{"id":"b","format":"dc+sd-jwt","meta":{"vct_values":["nl.a","nl.b"]}}
	]}`)
	want := []RequestedCredential{
		{VCTs: []string{"nl.kvk.registration"}, Claims: []string{"legalName", "address.city", "directors.*.name", "codes.0"}},
		{VCTs: []string{"nl.a", "nl.b"}, Claims: []string{}},
	}
	if got := RequestedCredentials(raw); !reflect.DeepEqual(got, want) {
		t.Errorf("RequestedCredentials = %+v, want %+v", got, want)
	}
}

func TestRequestedCredentialsToleratesAnUndecodableQuery(t *testing.T) {
	if got := RequestedCredentials([]byte(`not json`)); got == nil || len(got) != 0 {
		t.Errorf("RequestedCredentials = %#v, want an empty, non-nil list", got)
	}
}
