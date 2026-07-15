package generationexec

import (
	"strconv"
	"strings"
)

const extensionImportPlaceholder = "example.invalid/plystra-extension"

func renderHelperSource(extensionImport string) []byte {
	quotedImport := strconv.Quote(extensionImport)
	source := strings.Replace(helperSourceTemplate, strconv.Quote(extensionImportPlaceholder), quotedImport, 1)
	return []byte(source)
}

const helperSourceTemplate = `package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	extension "example.invalid/plystra-extension"
	generation "github.com/plystra/cli/generation/v1"
)

const maximumRequestSize = 64 << 20

var generate generation.GenerateFunc = extension.Generate

type request struct {
	API           string           ` + "`json:\"api\"`" + `
	ContextDigest string           ` + "`json:\"context_digest\"`" + `
	Context       generation.Input ` + "`json:\"context\"`" + `
}

type response struct {
	API    string            ` + "`json:\"api\"`" + `
	Status string            ` + "`json:\"status\"`" + `
	Output generation.Output ` + "`json:\"output\"`" + `
	Error  string            ` + "`json:\"error,omitempty\"`" + `
}

func main() {
	result := invoke()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		os.Exit(120)
	}
}

func invoke() (result response) {
	result.API = generation.Version
	defer func() {
		if recovered := recover(); recovered != nil {
			result = response{API: generation.Version, Status: "panic", Error: fmt.Sprint(recovered)}
		}
	}()

	request, context, err := readRequest()
	if err != nil {
		return response{API: generation.Version, Status: "protocol-error", Error: err.Error()}
	}
	if context.Digest() != request.ContextDigest {
		return response{API: generation.Version, Status: "protocol-error", Error: "normalized context digest differs from request"}
	}
	output, err := generate(context)
	if err != nil {
		return response{API: generation.Version, Status: "extension-error", Error: err.Error()}
	}
	if _, err := generation.NormalizeOutput(context, output); err != nil {
		return response{API: generation.Version, Status: "invalid-output", Error: err.Error()}
	}
	return response{API: generation.Version, Status: "success", Output: output}
}

func readRequest() (request, generation.Context, error) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maximumRequestSize+1))
	if err != nil {
		return request{}, generation.Context{}, fmt.Errorf("read request: %w", err)
	}
	if len(data) > maximumRequestSize {
		return request{}, generation.Context{}, errors.New("request exceeds protocol size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var input request
	if err := decoder.Decode(&input); err != nil {
		return request{}, generation.Context{}, fmt.Errorf("decode request: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return request{}, generation.Context{}, err
	}
	if input.API != generation.Version {
		return request{}, generation.Context{}, fmt.Errorf("request API %q does not match helper API %q", input.API, generation.Version)
	}
	context, err := generation.NewContext(input.Context)
	if err != nil {
		return request{}, generation.Context{}, fmt.Errorf("normalize request context: %w", err)
	}
	return input, context, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing request: %w", err)
	}
	return nil
}
`
