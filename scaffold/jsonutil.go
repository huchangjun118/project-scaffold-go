package scaffold

import (
	"bytes"
	"encoding/json"
	"strings"
)

// jsonStrictReader returns a decoder that rejects unknown fields.
func jsonStrictDecoder(data []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec
}

// isUnknownFieldErr reports whether err is a "json: unknown field" error.
func isUnknownFieldErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "json: unknown field")
}
