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
	want := []requestedCredential{
		{VCTs: []string{"nl.kvk.registration"}, Claims: []string{"legalName", "address.city", "directors.*.name", "codes.0"}},
		{VCTs: []string{"nl.a", "nl.b"}, Claims: []string{}},
	}
	if got := requestedCredentials(raw); !reflect.DeepEqual(got, want) {
		t.Errorf("requestedCredentials = %+v, want %+v", got, want)
	}
}

func TestRequestedCredentialsToleratesAnUndecodableQuery(t *testing.T) {
	if got := requestedCredentials([]byte(`not json`)); got == nil || len(got) != 0 {
		t.Errorf("requestedCredentials = %#v, want an empty, non-nil list", got)
	}
}
