// Package rulebook is the catalogue of WE BUILD attestation rulebooks the business
// wallet can issue and hold: the published SD-JWT VC JSON schemas, a sample payload
// per rulebook, and validation against them.
//
// The schemas and samples under schemas/ and samples/ are vendored from
// github.com/webuild-consortium/eudi-wallet-rulebooks-and-schemas at UpstreamCommit
// under the Apache License 2.0 (LICENCE-webuild, which must stay with them) and
// must not be edited by hand (TestVendoredFilesUnchanged). WE BUILD publishes no
// JSON schema for the LEI rulebook, so lei-sd-jwt.json is ours, written from rb-lei.
package rulebook

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// UpstreamCommit is the eudi-wallet-rulebooks-and-schemas commit the vendored files
// come from.
const UpstreamCommit = "223b5ea0a55914584f99c749dc94569ce8b2cb65"

//go:embed schemas/*.json samples/*.json
var files embed.FS

// Subject types, mirroring attestation.SubjectNaturalPerson / SubjectOrganization.
const (
	subjectOrganization  = "organization"
	subjectNaturalPerson = "natural_person"
)

// Claim value types, mirroring the attestation.AttributeType* names.
const (
	TypeString  = "string"
	TypeInteger = "integer"
	TypeNumber  = "number"
	TypeBoolean = "boolean"
	TypeDate    = "date"
	TypeObject  = "object"
	TypeArray   = "array"
)

// protocolClaims are the JWT / SD-JWT VC claims the issuer sets, not the person
// issuing. They are left out of the attribute list and of the claims validation.
var protocolClaims = map[string]struct{}{
	"vct": {}, "iss": {}, "sub": {}, "iat": {}, "nbf": {}, "exp": {}, "jti": {}, "cnf": {}, "status": {},
}

// statusClaim is accepted in any shape: EUCC, EU-PoA, UBO and LEI define a W3C-style
// status object, while our issuer emits the IETF Token Status List shape that VAT-ID
// and SD-JWT VC use. Until WE BUILD settles that, the mismatch must not fail validation.
const statusClaim = "status"

// Entry is one rulebook in the catalogue.
type Entry struct {
	ID          string `json:"id"`
	VCT         string `json:"vct"`
	Name        string `json:"name"`
	SubjectType string `json:"subjectType"`
	// SelectivelyDisclosable is false when the rulebook says no claim needs
	// selective disclosure (the LEI data is public).
	SelectivelyDisclosable bool   `json:"selectivelyDisclosable"`
	Upstream               string `json:"upstream"`

	schemaFile string
	sampleFile string
	compiled   compiledSchemas
}

type compiledSchemas struct {
	claims  *jsonschema.Schema
	payload *jsonschema.Schema
	doc     map[string]any
	sample  map[string]any
}

// Attribute is one top-level claim of a rulebook, in the shape an attestation
// schema's attribute list uses.
type Attribute struct {
	Key      string
	Label    string
	Type     string
	Required bool
}

// Claim is one claim path for issuer metadata, with a null segment for "every array
// element" as SD-JWT VC type metadata spells it.
type Claim struct {
	Path     []any
	Label    string
	Required bool
}

var catalogue = []*Entry{
	{ID: "eucc", VCT: "uri:eu.eudi.eucc.1", Name: "EU Company Certificate", SubjectType: subjectOrganization, SelectivelyDisclosable: true, schemaFile: "ds004-eucc-sd-jwt.json", sampleFile: "ds004-eucc-sd-jwt-sample.json"},
	{ID: "eu-poa", VCT: "uri:eu.eudi.eu-poa.1", Name: "EU Power of Attorney", SubjectType: subjectNaturalPerson, SelectivelyDisclosable: true, schemaFile: "ds007-eu-poa-sd-jwt.json", sampleFile: "ds007-eu-poa-sd-jwt-sample.json"},
	{ID: "lei", VCT: "eu.we-build.lei.1", Name: "Legal Entity Identifier", SubjectType: subjectOrganization, SelectivelyDisclosable: false, schemaFile: "lei-sd-jwt.json", sampleFile: "lei-sd-jwt-sample.json"},
	{ID: "vat-id", VCT: "uri:eu.eudi.vat.2", Name: "VAT ID", SubjectType: subjectOrganization, SelectivelyDisclosable: true, schemaFile: "vat-id-sd-jwt.json", sampleFile: "vat-id-sd-jwt-sample.json"},
	{ID: "ubo", VCT: "eu.we-build:ubo:1", Name: "Ultimate Beneficial Owner", SubjectType: subjectOrganization, SelectivelyDisclosable: true, schemaFile: "ubo-sd-jwt.json", sampleFile: "ubo-sd-jwt-sample.json"},
}

var (
	byID  map[string]*Entry
	byVCT map[string]*Entry
)

func init() {
	byID = make(map[string]*Entry, len(catalogue))
	byVCT = make(map[string]*Entry, len(catalogue))
	for _, e := range catalogue {
		c, err := compile(e)
		if err != nil {
			panic(fmt.Sprintf("rulebook %s: %v", e.ID, err))
		}
		e.compiled = c
		if e.schemaFile == "lei-sd-jwt.json" {
			e.Upstream = "rulebooks/rb-lei (no published JSON schema)"
		} else {
			e.Upstream = "data-schemas/sd-jwt/" + e.schemaFile + "@" + UpstreamCommit
		}
		byID[e.ID] = e
		byVCT[e.VCT] = e
	}
}

// ErrInvalidClaims wraps every validation failure; Violations lists them.
var ErrInvalidClaims = errors.New("claims do not match the rulebook schema")

// ValidationError lists where and why claims fail a rulebook schema.
type ValidationError struct {
	Rulebook   string
	Violations []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s: %s", ErrInvalidClaims, e.Rulebook, strings.Join(e.Violations, "; "))
}

func (e *ValidationError) Unwrap() error { return ErrInvalidClaims }

// All returns the catalogue in display order.
func All() []*Entry { return slices.Clone(catalogue) }

// Get returns the rulebook with the given id.
func Get(id string) (*Entry, bool) {
	e, ok := byID[id]
	return e, ok
}

// ForVCT returns the rulebook a credential type belongs to.
func ForVCT(vct string) (*Entry, bool) {
	e, ok := byVCT[vct]
	return e, ok
}

// Sample returns a fresh copy of the rulebook's sample data claims (protocol
// claims and sample-file annotations removed), for prefilling the issue form.
func (e *Entry) Sample() map[string]any {
	out := make(map[string]any)
	props := properties(e.compiled.doc)
	for k, v := range e.compiled.sample {
		if _, protocol := protocolClaims[k]; protocol {
			continue
		}
		if _, declared := props[k]; !declared {
			continue
		}
		out[k] = deepCopy(v)
	}
	return out
}

// ValidateClaims checks the data claims an issuer supplies, before issuance:
// protocol claims are not expected yet.
func (e *Entry) ValidateClaims(claims map[string]any) error {
	return e.validate(e.compiled.claims, claims)
}

// ValidatePayload checks a full credential payload: the data claims plus the
// protocol claims the issuer added.
func (e *Entry) ValidatePayload(payload map[string]any) error {
	return e.validate(e.compiled.payload, payload)
}

func (e *Entry) validate(sch *jsonschema.Schema, claims map[string]any) error {
	inst, err := toInstance(claims)
	if err != nil {
		return err
	}
	err = sch.Validate(inst)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return fmt.Errorf("rulebook %s: validate: %w", e.ID, err)
	}
	return &ValidationError{Rulebook: e.ID, Violations: violations(ve)}
}

// Attributes lists the rulebook's top-level data claims, in schema order.
func (e *Entry) Attributes() []Attribute {
	doc := e.compiled.doc
	required := requiredSet(doc)
	out := []Attribute{}
	for _, key := range orderedKeys(e.schemaFile, doc) {
		if _, protocol := protocolClaims[key]; protocol {
			continue
		}
		prop := resolve(doc, properties(doc)[key])
		out = append(out, Attribute{Key: key, Label: label(key, prop), Type: claimType(prop), Required: required[key]})
	}
	return out
}

// Claims lists every data claim path, nested ones included, for the issuer's
// credential metadata.
func (e *Entry) Claims() []Claim {
	doc := e.compiled.doc
	w := &claimWalker{doc: doc, out: []Claim{}, seen: map[string]bool{}}
	required := requiredSet(doc)
	for _, key := range orderedKeys(e.schemaFile, doc) {
		if _, protocol := protocolClaims[key]; protocol {
			continue
		}
		w.walk(properties(doc)[key], []any{key}, required[key], 0)
	}
	return w.out
}

// maxClaimDepth stops the walk on a recursive $ref.
const maxClaimDepth = 8

type claimWalker struct {
	doc  map[string]any
	out  []Claim
	seen map[string]bool
}

// walk labels an array element after its array: the last name in its path.
func (w *claimWalker) walk(node any, path []any, required bool, depth int) {
	if depth > maxClaimDepth {
		return
	}
	n := resolve(w.doc, node)
	id := fmt.Sprint(path...)
	if !w.seen[id] {
		w.seen[id] = true
		w.out = append(w.out, Claim{Path: slices.Clone(path), Label: label(lastName(path), n), Required: required})
	}
	if items, ok := n["items"]; ok {
		w.walk(items, append(slices.Clone(path), nil), false, depth+1)
		return
	}
	req := requiredSet(n)
	allOf, _ := n["allOf"].([]any)
	for _, branch := range allOf {
		maps.Copy(req, requiredSet(resolve(w.doc, branch)))
	}
	props := mergedProperties(w.doc, n)
	for _, k := range sortedKeys(props) {
		w.walk(props[k], append(slices.Clone(path), k), required && req[k], depth+1)
	}
}

func lastName(path []any) string {
	for i := len(path) - 1; i >= 0; i-- {
		if s, ok := path[i].(string); ok {
			return s
		}
	}
	return ""
}

func compile(e *Entry) (compiledSchemas, error) {
	raw, err := files.ReadFile("schemas/" + e.schemaFile)
	if err != nil {
		return compiledSchemas{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return compiledSchemas{}, err
	}
	sampleRaw, err := files.ReadFile("samples/" + e.sampleFile)
	if err != nil {
		return compiledSchemas{}, err
	}
	var sample map[string]any
	if err := json.Unmarshal(sampleRaw, &sample); err != nil {
		return compiledSchemas{}, err
	}

	payloadDoc := acceptAnyStatus(doc)
	claimsDoc := acceptAnyStatus(doc)
	if req, ok := claimsDoc["required"].([]any); ok {
		claimsDoc["required"] = slices.DeleteFunc(req, func(v any) bool {
			s, _ := v.(string)
			_, protocol := protocolClaims[s]
			return protocol
		})
	}

	payload, err := compileDoc(e.ID+"-payload", payloadDoc)
	if err != nil {
		return compiledSchemas{}, err
	}
	claims, err := compileDoc(e.ID+"-claims", claimsDoc)
	if err != nil {
		return compiledSchemas{}, err
	}
	return compiledSchemas{claims: claims, payload: payload, doc: doc, sample: sample}, nil
}

// acceptAnyStatus returns a fresh copy of the schema whose status claim accepts any
// value (see statusClaim).
func acceptAnyStatus(src map[string]any) map[string]any {
	doc, _ := deepCopy(src).(map[string]any)
	if props, ok := doc["properties"].(map[string]any); ok {
		if _, has := props[statusClaim]; has {
			props[statusClaim] = map[string]any{}
		}
	}
	return doc
}

func compileDoc(name string, doc map[string]any) (*jsonschema.Schema, error) {
	// The vendored $id values point at example.org; compiling under our own URL
	// keeps every $ref local.
	delete(doc, "$id")
	url := "urn:rulebook:" + name
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	inst, err := toInstance(doc)
	if err != nil {
		return nil, err
	}
	if err := c.AddResource(url, inst); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// toInstance re-reads a value through the validator's own decoder so numbers are
// json.Number, as it expects.
func toInstance(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("rulebook: encode claims: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("rulebook: decode claims: %w", err)
	}
	return inst, nil
}

var printer = message.NewPrinter(language.English)

func violations(ve *jsonschema.ValidationError) []string {
	var out []string
	var collect func(e *jsonschema.ValidationError)
	collect = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			out = append(out, fmt.Sprintf("/%s: %s", strings.Join(e.InstanceLocation, "/"), e.ErrorKind.LocalizedString(printer)))
			return
		}
		for _, c := range e.Causes {
			collect(c)
		}
	}
	collect(ve)
	sort.Strings(out)
	return slices.Compact(out)
}

func properties(n map[string]any) map[string]any {
	p, _ := n["properties"].(map[string]any)
	return p
}

// mergedProperties are a node's own properties plus those its allOf/anyOf/oneOf
// branches declare.
func mergedProperties(doc, n map[string]any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, properties(n))
	for _, b := range branches(doc, n) {
		for k, v := range properties(b) {
			if _, ok := out[k]; !ok {
				out[k] = v
			}
		}
	}
	return out
}

func branches(doc, n map[string]any) []map[string]any {
	var out []map[string]any
	for _, kw := range []string{"allOf", "anyOf", "oneOf"} {
		list, _ := n[kw].([]any)
		for _, b := range list {
			out = append(out, resolve(doc, b))
		}
	}
	return out
}

func requiredSet(n map[string]any) map[string]bool {
	out := map[string]bool{}
	list, _ := n["required"].([]any)
	for _, v := range list {
		if s, ok := v.(string); ok {
			out[s] = true
		}
	}
	return out
}

// resolve follows a local "#/..." $ref; anything else is returned as is.
func resolve(doc map[string]any, node any) map[string]any {
	n, _ := node.(map[string]any)
	for range maxClaimDepth {
		ref, ok := n["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#/") {
			return n
		}
		var cur any = doc
		for seg := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
			m, _ := cur.(map[string]any)
			cur = m[seg]
		}
		n, _ = cur.(map[string]any)
	}
	return n
}

func claimType(n map[string]any) string {
	t, _ := n["type"].(string)
	if t == "" {
		if list, ok := n["type"].([]any); ok && len(list) > 0 {
			t, _ = list[0].(string)
		}
	}
	switch t {
	case TypeObject, TypeArray, TypeInteger, TypeNumber, TypeBoolean:
		return t
	}
	if f, _ := n["format"].(string); f == "date" {
		return TypeDate
	}
	if t == "" && (n["properties"] != nil || n["oneOf"] != nil) {
		return TypeObject
	}
	return TypeString
}

func label(key string, n map[string]any) string {
	if t, ok := n["title"].(string); ok && t != "" {
		return t
	}
	words := strings.ReplaceAll(key, "_", " ")
	if words == "" {
		return key
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// orderedKeys returns the root property names in the order the schema file lists
// them; a decoded map loses that order.
func orderedKeys(file string, doc map[string]any) []string {
	raw, err := files.ReadFile("schemas/" + file)
	if err != nil {
		return sortedKeys(properties(doc))
	}
	var root struct {
		Properties json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return sortedKeys(properties(doc))
	}
	dec := json.NewDecoder(bytes.NewReader(root.Properties))
	var keys []string
	if _, err := dec.Token(); err != nil {
		return sortedKeys(properties(doc))
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return sortedKeys(properties(doc))
		}
		key, _ := tok.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return sortedKeys(properties(doc))
		}
	}
	return keys
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func deepCopy(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	return out
}
