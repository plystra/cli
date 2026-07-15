package generationexec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maximumProtocolJSONDepth = 64

func validateSingleJSONValue(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := consumeJSONValue(decoder, token, 1); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains multiple values")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, token json.Token, depth int) error {
	if depth > maximumProtocolJSONDepth {
		return fmt.Errorf("JSON exceeds maximum depth %d", maximumProtocolJSONDepth)
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		switch token.(type) {
		case string, bool, nil, json.Number:
			return nil
		default:
			return fmt.Errorf("unsupported JSON token %T", token)
		}
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode object key: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("object contains duplicate key %q", key)
			}
			keys[key] = struct{}{}
			valueToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode object value %q: %w", key, err)
			}
			if err := consumeJSONValue(decoder, valueToken, depth+1); err != nil {
				return fmt.Errorf("object value %q: %w", key, err)
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("JSON object is not closed")
		}
		return nil
	case '[':
		for index := 0; decoder.More(); index++ {
			valueToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode array item %d: %w", index, err)
			}
			if err := consumeJSONValue(decoder, valueToken, depth+1); err != nil {
				return fmt.Errorf("array item %d: %w", index, err)
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("JSON array is not closed")
		}
		return nil
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}
