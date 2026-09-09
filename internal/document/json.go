package document

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// parseJSON reads JSON into an ordered node tree. It walks encoding/json's
// token stream rather than decoding into a map so that keys keep the order
// the author wrote them: a reviewer reading a re-sorted config is a reviewer
// who misses things.
func parseJSON(path string, src []byte) (*Document, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	root, err := jsonValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the top-level JSON value")
	}
	return dataDocument(path, FormatJSON, []dataNode{root}), nil
}

func jsonValue(dec *json.Decoder) (dataNode, error) {
	tok, err := dec.Token()
	if err != nil {
		return dataNode{}, err
	}
	return jsonValueFrom(dec, tok)
}

func jsonValueFrom(dec *json.Decoder, tok json.Token) (dataNode, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return jsonObject(dec)
		case '[':
			return jsonArray(dec)
		}
		return dataNode{}, fmt.Errorf("unexpected %q in JSON", t)
	case string:
		return dataNode{Kind: kindString, Value: t}, nil
	case json.Number:
		return dataNode{Kind: kindNumber, Value: t.String()}, nil
	case bool:
		return dataNode{Kind: kindBool, Value: strconv.FormatBool(t)}, nil
	case nil:
		return dataNode{Kind: kindNull, Value: "null"}, nil
	}
	return dataNode{}, fmt.Errorf("unsupported JSON token %v", tok)
}

func jsonObject(dec *json.Decoder) (dataNode, error) {
	n := dataNode{Kind: kindObject}
	for {
		tok, err := dec.Token()
		if err != nil {
			return n, err
		}
		if d, ok := tok.(json.Delim); ok && d == '}' {
			return n, nil
		}
		key, ok := tok.(string)
		if !ok {
			return n, fmt.Errorf("JSON object key is not a string: %v", tok)
		}
		child, err := jsonValue(dec)
		if err != nil {
			return n, err
		}
		child.Key, child.Keyed = key, true
		n.Children = append(n.Children, child)
	}
}

func jsonArray(dec *json.Decoder) (dataNode, error) {
	n := dataNode{Kind: kindArray}
	for {
		tok, err := dec.Token()
		if err != nil {
			return n, err
		}
		if d, ok := tok.(json.Delim); ok && d == ']' {
			return n, nil
		}
		child, err := jsonValueFrom(dec, tok)
		if err != nil {
			return n, err
		}
		n.Children = append(n.Children, child)
	}
}
