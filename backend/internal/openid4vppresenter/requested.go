package openid4vppresenter

import (
	"encoding/json"
	"fmt"
	"strings"
)

// claimPathSeparator joins a DCQL claim path for display.
const claimPathSeparator = "."

// RequestedCredential is what an admin is shown of one DCQL credential query
// before approving: the acceptable credential types and the claim paths the
// answer would disclose. Claims is empty when the query asks for the
// credential without any selectively disclosable claim.
type RequestedCredential struct {
	VCTs   []string `json:"vcts"`
	Claims []string `json:"claims"`
}

type dcqlSummary struct {
	Credentials []struct {
		Meta struct {
			VCTValues []string `json:"vct_values"`
		} `json:"meta"`
		Claims []struct {
			Path []any `json:"path"`
		} `json:"claims"`
	} `json:"credentials"`
}

// RequestedCredentials summarizes a validated DCQL query for the approval
// queue. The query was checked when it was queued, so a query that no longer
// decodes yields no summary rather than an error: the queue still lists the
// request, and the admin can decline it.
func RequestedCredentials(raw json.RawMessage) []RequestedCredential {
	var q dcqlSummary
	if err := json.Unmarshal(raw, &q); err != nil {
		return []RequestedCredential{}
	}
	out := make([]RequestedCredential, 0, len(q.Credentials))
	for _, c := range q.Credentials {
		rc := RequestedCredential{VCTs: c.Meta.VCTValues, Claims: make([]string, 0, len(c.Claims))}
		if rc.VCTs == nil {
			rc.VCTs = []string{}
		}
		for _, claim := range c.Claims {
			parts := make([]string, 0, len(claim.Path))
			for _, p := range claim.Path {
				// A null element selects every array element (DCQL §7.1).
				if p == nil {
					parts = append(parts, "*")
					continue
				}
				parts = append(parts, fmt.Sprint(p))
			}
			rc.Claims = append(rc.Claims, strings.Join(parts, claimPathSeparator))
		}
		out = append(out, rc)
	}
	return out
}
