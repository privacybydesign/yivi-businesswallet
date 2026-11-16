package apidocs

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// internalProofingTag marks the wallet's own identity proofing routes, which
// only its frontend calls. The docs leave them out, so identity proofing reads
// as the one API an integrator uses.
const internalProofingTag = "identity-proofing-internal"

// served is the document the docs page reads: spec without the operations
// tagged internalProofingTag.
var served = mustServed(spec)

func mustServed(full []byte) []byte {
	out, err := withoutTag(full, internalProofingTag)
	if err != nil {
		panic(fmt.Sprintf("apidocs: served spec: %v", err))
	}
	return out
}

// withoutTag is the document full without tag and the operations under it.
func withoutTag(full []byte, tag string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(full, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping document")
	}
	root := doc.Content[0]
	if paths := mapValue(root, "paths"); paths != nil {
		dropOperations(paths, tag)
	}
	if tags := mapValue(root, "tags"); tags != nil {
		tags.Content = slices.DeleteFunc(tags.Content, func(t *yaml.Node) bool {
			name := mapValue(t, "name")
			return name != nil && name.Value == tag
		})
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// dropOperations removes from paths every operation tagged tag, and every
// path left without an operation.
func dropOperations(paths *yaml.Node, tag string) {
	var kept []*yaml.Node
	for i := 0; i+1 < len(paths.Content); i += 2 {
		item := paths.Content[i+1]
		var fields []*yaml.Node
		operations := 0
		for j := 0; j+1 < len(item.Content); j += 2 {
			key, value := item.Content[j], item.Content[j+1]
			if !isOperation(key.Value) {
				fields = append(fields, key, value)
				continue
			}
			if tags := mapValue(value, "tags"); tags != nil && slices.ContainsFunc(tags.Content, func(t *yaml.Node) bool { return t.Value == tag }) {
				continue
			}
			operations++
			fields = append(fields, key, value)
		}
		if operations > 0 {
			item.Content = fields
			kept = append(kept, paths.Content[i], item)
		}
	}
	paths.Content = kept
}

func isOperation(key string) bool {
	return slices.Contains([]string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}, key)
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
