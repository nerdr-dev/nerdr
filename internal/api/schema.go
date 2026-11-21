package api

import _ "embed"

// Schema is the API as JSON Schema, for clients in other languages to
// make their types from: what a client sends (hello, the requests), what
// the core sends (welcome, the answers, the events), and in x-nerdr-ops
// which result each op answers with. It is made from the types here and
// in core by TestSchemaIsCurrent, which fails when one changes and the
// schema does not; see that test for how to make it anew.
//
//go:embed schema.json
var Schema []byte
