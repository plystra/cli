package bootstrapgen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/pluginid"
	"github.com/plystra/kernel/plugin/manifest"
)

const (
	// YAMLModulePath is the direct runtime module used by generated bootstrap
	// for typed current-Project environment composition.
	YAMLModulePath = "go.yaml.in/yaml/v3"
	// YAMLModuleVersion is the minimum supported generated-bootstrap YAML runtime.
	YAMLModuleVersion = "v3.0.4"
)

// ConfigurationSchema identifies one selected Plugin configuration declaration
// whose field-specific overlay behavior must be projected into bootstrap.
type ConfigurationSchema struct {
	PluginID string
	Schema   manifest.Config
}

type runtimeConfigurationSchema struct {
	pluginID string
	fields   []runtimeConfigurationField
}

type runtimeConfigurationField struct {
	name string
	kind manifest.ConfigType
}

func planRuntimeConfigurationSchemas(inputs []ConfigurationSchema) ([]runtimeConfigurationSchema, error) {
	byPlugin := make(map[string]runtimeConfigurationSchema, len(inputs))
	for _, input := range inputs {
		if err := pluginid.Validate(input.PluginID); err != nil {
			return nil, fmt.Errorf("configuration schema Plugin ID %q is invalid: %v", input.PluginID, err)
		}
		if _, duplicate := byPlugin[input.PluginID]; duplicate {
			return nil, fmt.Errorf("configuration schema repeats Plugin ID %q", input.PluginID)
		}
		fields := input.Schema.Fields()
		planned := make([]runtimeConfigurationField, len(fields))
		for index, field := range fields {
			planned[index] = runtimeConfigurationField{
				name: field.Name(),
				kind: field.Type(),
			}
		}
		sort.Slice(planned, func(left, right int) bool { return planned[left].name < planned[right].name })
		byPlugin[input.PluginID] = runtimeConfigurationSchema{pluginID: input.PluginID, fields: planned}
	}
	pluginIDs := make([]string, 0, len(byPlugin))
	for pluginID := range byPlugin {
		pluginIDs = append(pluginIDs, pluginID)
	}
	sort.Strings(pluginIDs)
	result := make([]runtimeConfigurationSchema, len(pluginIDs))
	for index, pluginID := range pluginIDs {
		result[index] = byPlugin[pluginID]
	}
	return result, nil
}

func renderRuntimeConfigurationSupport(schemas []runtimeConfigurationSchema) (string, error) {
	var source strings.Builder
	source.WriteString("type runtimeConfigurationFieldKind uint8\n\n")
	source.WriteString("const (\n")
	source.WriteString("\truntimeConfigurationString runtimeConfigurationFieldKind = iota + 1\n")
	source.WriteString("\truntimeConfigurationInteger\n")
	source.WriteString("\truntimeConfigurationNumber\n")
	source.WriteString("\truntimeConfigurationBoolean\n")
	source.WriteString("\truntimeConfigurationObject\n")
	source.WriteString("\truntimeConfigurationArray\n")
	source.WriteString("\truntimeConfigurationSecret\n")
	source.WriteString(")\n\n")
	source.WriteString("var runtimeConfigurationSchemas = map[string]map[string]runtimeConfigurationFieldKind{\n")
	for _, schema := range schemas {
		fmt.Fprintf(&source, "\t%s: {\n", strconv.Quote(schema.pluginID))
		for _, field := range schema.fields {
			kind := ""
			switch field.kind {
			case manifest.ConfigString, manifest.ConfigDuration, manifest.ConfigURL:
				kind = "runtimeConfigurationString"
			case manifest.ConfigInteger:
				kind = "runtimeConfigurationInteger"
			case manifest.ConfigNumber:
				kind = "runtimeConfigurationNumber"
			case manifest.ConfigBoolean:
				kind = "runtimeConfigurationBoolean"
			case manifest.ConfigObject:
				kind = "runtimeConfigurationObject"
			case manifest.ConfigArray:
				kind = "runtimeConfigurationArray"
			case manifest.ConfigSecret:
				kind = "runtimeConfigurationSecret"
			default:
				return "", fmt.Errorf("configuration schema for Plugin %q field %q has unsupported type %q", schema.pluginID, field.name, field.kind)
			}
			fmt.Fprintf(&source, "\t\t%s: %s,\n", strconv.Quote(field.name), kind)
		}
		source.WriteString("\t},\n")
	}
	source.WriteString("}\n\n")
	source.WriteString(runtimeConfigurationSupport)
	return source.String(), nil
}

const runtimeConfigurationSupport = `const (
	runtimeEnvironmentVariable   = "PLYSTRA_ENV"
	runtimeConfigurationVariable = "PLYSTRA_CONFIG"
	runtimeMaximumDocumentSize    = 1 << 20
)

type runtimeSelectionMode uint8

const (
	runtimeSelectionDefault runtimeSelectionMode = iota + 1
	runtimeSelectionEnvironment
	runtimeSelectionExplicit
)

type runtimeSelection struct {
	mode        runtimeSelectionMode
	path        string
	environment string
}

func validateRuntimeApplicationModel(document []byte) error {
	digest, err := runtimeApplicationModelCompatibilityDigest(document)
	if err != nil {
		return fmt.Errorf("%w: inspect build-affecting declarations: %v", ErrRuntimeCompatibility, err)
	}
	if digest != compiledApplicationModelCompatibilityDigest {
		return fmt.Errorf("%w: selected runtime configuration changes build-affecting application declarations; rebuild with the same --env or --config selection before starting this binary", ErrRuntimeCompatibility)
	}
	return nil
}

func runtimeApplicationModelCompatibilityDigest(document []byte) (string, error) {
	root, err := decodeRuntimeDocument(document, "effective runtime configuration")
	if err != nil {
		return "", err
	}
	values, err := runtimeMapping(root, "effective runtime configuration", runtimeKeySet("http", "timeouts", "interfaces", "config"))
	if err != nil {
		return "", err
	}
	transports, cors, exposures, err := runtimeApplicationModelHTTP(values["http"])
	if err != nil {
		return "", err
	}
	requirements, implementations, err := runtimeApplicationModelInterfaces(values["interfaces"])
	if err != nil {
		return "", err
	}
	canonical, err := json.Marshal(map[string]any{
		"application_model_digest": compiledApplicationModelDigest,
		"projection": map[string]any{
			"http_cors":              cors,
			"http_exposures":         exposures,
			"http_transports":        transports,
			"implementation_choices": implementations,
			"interface_requirements": requirements,
		},
		"version": 1,
	})
	if err != nil {
		return "", runtimeConfigurationError("encode build-affecting runtime projection")
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func runtimeApplicationModelHTTP(node *yaml.Node) (map[string]any, any, []string, error) {
	values, err := runtimeOptionalMapping(node, "http", runtimeKeySet("address", "transports", "cors", "expose"))
	if err != nil {
		return nil, nil, nil, err
	}
	connect := true
	rest := false
	transports, err := runtimeOptionalMapping(values["transports"], "http.transports", runtimeKeySet("connect", "rest"))
	if err != nil {
		return nil, nil, nil, err
	}
	if selected := transports["connect"]; selected != nil {
		connect, err = runtimeApplicationModelBoolean(selected, "http.transports.connect")
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if selected := transports["rest"]; selected != nil {
		rest, err = runtimeApplicationModelBoolean(selected, "http.transports.rest")
		if err != nil {
			return nil, nil, nil, err
		}
	}
	var cors any
	if corsNode := values["cors"]; corsNode != nil {
		fields, mappingErr := runtimeMapping(corsNode, "http.cors", runtimeKeySet("allowed_origins", "allow_credentials"))
		if mappingErr != nil {
			return nil, nil, nil, mappingErr
		}
		origins, sequenceErr := runtimeApplicationModelStrings(fields["allowed_origins"], "http.cors.allowed_origins")
		if sequenceErr != nil {
			return nil, nil, nil, sequenceErr
		}
		credentials := false
		if selected := fields["allow_credentials"]; selected != nil {
			credentials, err = runtimeApplicationModelBoolean(selected, "http.cors.allow_credentials")
			if err != nil {
				return nil, nil, nil, err
			}
		}
		cors = map[string]any{
			"allow_credentials": credentials,
			"allowed_origins":   origins,
		}
	}
	exposures, err := runtimeApplicationModelStrings(values["expose"], "http.expose")
	if err != nil {
		return nil, nil, nil, err
	}
	return map[string]any{"connect": connect, "rest": rest}, cors, exposures, nil
}

func runtimeApplicationModelInterfaces(node *yaml.Node) ([]string, []map[string]any, error) {
	values, err := runtimeOptionalMapping(node, "interfaces", runtimeKeySet("require", "use"))
	if err != nil {
		return nil, nil, err
	}
	requirements, err := runtimeApplicationModelInterfaceIDs(values["require"], "interfaces.require")
	if err != nil {
		return nil, nil, err
	}
	choices, err := runtimeOptionalMapping(values["use"], "interfaces.use", nil)
	if err != nil {
		return nil, nil, err
	}
	interfaces := make([]string, 0, len(choices))
	for interfaceID := range choices {
		interfaces = append(interfaces, interfaceID)
	}
	sort.Strings(interfaces)
	implementations := make([]map[string]any, 0, len(interfaces))
	for _, interfaceID := range interfaces {
		if !validRuntimeSelectableInterfaceID(interfaceID) {
			return nil, nil, runtimeConfigurationError("interfaces.use key %q is not a selectable canonical Interface ID", interfaceID)
		}
		constructor, valueErr := runtimeString(choices[interfaceID])
		if valueErr != nil || !validRuntimeConstructorSymbol(constructor) {
			return nil, nil, runtimeConfigurationError("interfaces.use[%q] must be a fully qualified Implementation constructor symbol", interfaceID)
		}
		implementations = append(implementations, map[string]any{
			"constructor": constructor,
			"interface":   interfaceID,
		})
	}
	return requirements, implementations, nil
}

func runtimeApplicationModelInterfaceIDs(node *yaml.Node, path string) ([]string, error) {
	values, err := runtimeInterfaceSequence(node, path)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func runtimeApplicationModelStrings(node *yaml.Node, path string) ([]string, error) {
	if node == nil {
		return []string{}, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, runtimeConfigurationError("%s must be a sequence", path)
	}
	result := make([]string, len(node.Content))
	for index, item := range node.Content {
		value, err := runtimeString(item)
		if err != nil {
			return nil, runtimeConfigurationError("%s[%d] must be a string", path, index)
		}
		result[index] = value
	}
	sort.Strings(result)
	return result, nil
}

func runtimeApplicationModelBoolean(node *yaml.Node, path string) (bool, error) {
	if node == nil {
		return false, runtimeConfigurationError("%s is required", path)
	}
	normalized, err := validateRuntimeBoolean(node, path)
	if err != nil {
		return false, err
	}
	return normalized.Value == "true", nil
}

func loadRuntimeDocument(options RuntimeOptions) ([]byte, error) {
	selection, err := selectRuntimeConfiguration(options)
	if err != nil {
		return nil, err
	}
	root, err := kernelconfiguration.LoadDocument(defaultRuntimeDocument)
	if err != nil {
		return nil, fmt.Errorf("%w: load default %s: %w", ErrRuntimeSelector, defaultRuntimeDocument, err)
	}
	defer clear(root)
	switch selection.mode {
	case runtimeSelectionDefault:
		document, err := normalizeRuntimeDocument(root, defaultRuntimeDocument)
		if err != nil {
			return nil, fmt.Errorf("%w: default %s: %v", ErrRuntimeConfiguration, defaultRuntimeDocument, err)
		}
		return document, nil
	case runtimeSelectionEnvironment:
		overlay, err := kernelconfiguration.LoadDocument(selection.path)
		if err != nil {
			return nil, fmt.Errorf("%w: environment %q requires %s; create that sparse overlay or select an existing environment: %w", ErrRuntimeSelector, selection.environment, filepath.ToSlash(selection.path), err)
		}
		defer clear(overlay)
		document, err := composeRuntimeDocuments(root, overlay)
		if err != nil {
			return nil, fmt.Errorf("%w: apply environment %q from %s: %v", ErrRuntimeConfiguration, selection.environment, filepath.ToSlash(selection.path), err)
		}
		return document, nil
	case runtimeSelectionExplicit:
		before, err := inspectRuntimeConfigurationPath(selection.path)
		if err != nil {
			return nil, fmt.Errorf("%w: full-replacement configuration %s must be an existing regular Project file without symbolic path components", ErrRuntimeSelector, filepath.ToSlash(selection.path))
		}
		selected, err := kernelconfiguration.LoadDocument(selection.path)
		if err != nil {
			return nil, fmt.Errorf("%w: load full-replacement configuration %s: %w", ErrRuntimeSelector, filepath.ToSlash(selection.path), err)
		}
		defer clear(selected)
		after, err := inspectRuntimeConfigurationPath(selection.path)
		if err != nil || !sameRuntimeConfigurationPathStates(before, after) {
			return nil, fmt.Errorf("%w: full-replacement configuration %s changed while it was loaded", ErrRuntimeSelector, filepath.ToSlash(selection.path))
		}
		document, err := normalizeRuntimeDocument(selected, filepath.ToSlash(selection.path))
		if err != nil {
			return nil, fmt.Errorf("%w: full-replacement configuration %s: %v", ErrRuntimeConfiguration, filepath.ToSlash(selection.path), err)
		}
		return document, nil
	default:
		return nil, fmt.Errorf("%w: selector mode is invalid", ErrRuntimeSelector)
	}
}

func selectRuntimeConfiguration(options RuntimeOptions) (runtimeSelection, error) {
	arguments := options.Arguments
	if len(arguments) != 0 {
		hasEnvironment := false
		hasConfiguration := false
		for _, argument := range arguments {
			hasEnvironment = hasEnvironment || argument == "--env"
			hasConfiguration = hasConfiguration || argument == "--config"
		}
		if hasEnvironment && hasConfiguration {
			return runtimeSelection{}, fmt.Errorf("%w: --env and --config cannot be used together", ErrRuntimeSelector)
		}
		if len(arguments) != 2 {
			return runtimeSelection{}, fmt.Errorf("%w: expected no selector, --env <environment>, or --config <yaml-path>", ErrRuntimeSelector)
		}
		switch arguments[0] {
		case "--env":
			if err := validateRuntimeEnvironmentName(arguments[1], "--env"); err != nil {
				return runtimeSelection{}, err
			}
			return runtimeSelection{mode: runtimeSelectionEnvironment, path: "plystra." + arguments[1] + ".yaml", environment: arguments[1]}, nil
		case "--config":
			path, err := runtimeProjectRelativeConfigurationPath(arguments[1], "--config")
			if err != nil {
				return runtimeSelection{}, err
			}
			return runtimeSelection{mode: runtimeSelectionExplicit, path: path}, nil
		default:
			return runtimeSelection{}, fmt.Errorf("%w: expected no selector, --env <environment>, or --config <yaml-path>", ErrRuntimeSelector)
		}
	}
	configurationPath, hasConfiguration, err := runtimeSelectorEnvironmentValue(options.Environment, runtimeConfigurationVariable)
	if err != nil {
		return runtimeSelection{}, err
	}
	environment, hasEnvironment, err := runtimeSelectorEnvironmentValue(options.Environment, runtimeEnvironmentVariable)
	if err != nil {
		return runtimeSelection{}, err
	}
	if hasConfiguration && hasEnvironment {
		return runtimeSelection{}, fmt.Errorf("%w: %s and %s cannot be used together", ErrRuntimeSelector, runtimeConfigurationVariable, runtimeEnvironmentVariable)
	}
	if hasConfiguration {
		path, err := runtimeProjectRelativeConfigurationPath(configurationPath, runtimeConfigurationVariable)
		if err != nil {
			return runtimeSelection{}, err
		}
		return runtimeSelection{mode: runtimeSelectionExplicit, path: path}, nil
	}
	if hasEnvironment {
		if err := validateRuntimeEnvironmentName(environment, runtimeEnvironmentVariable); err != nil {
			return runtimeSelection{}, err
		}
		return runtimeSelection{mode: runtimeSelectionEnvironment, path: "plystra." + environment + ".yaml", environment: environment}, nil
	}
	return runtimeSelection{mode: runtimeSelectionDefault, path: defaultRuntimeDocument}, nil
}

func runtimeSelectorEnvironmentValue(environment []string, variable string) (string, bool, error) {
	var value string
	found := false
	for _, entry := range environment {
		name, current, exists := strings.Cut(entry, "=")
		if !exists || name != variable {
			continue
		}
		if found {
			return "", false, fmt.Errorf("%w: environment contains %s more than once", ErrRuntimeSelector, variable)
		}
		value, found = current, true
	}
	return value, found, nil
}

func validateRuntimeEnvironmentName(value, source string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s selects an empty environment name", ErrRuntimeSelector, source)
	}
	if len(value) > 200 {
		return fmt.Errorf("%w: %s environment name exceeds 200 bytes", ErrRuntimeSelector, source)
	}
	if value == "." || value == ".." || filepath.IsAbs(value) || filepath.VolumeName(value) != "" ||
		strings.ContainsAny(value, "/\\<>:\"|?*") || strings.IndexFunc(value, unicode.IsControl) >= 0 ||
		filepath.Clean(value) != value {
		return fmt.Errorf("%w: %s environment %q must be one safe filename component", ErrRuntimeSelector, source, value)
	}
	return nil
}

func runtimeProjectRelativeConfigurationPath(value, source string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: %s selects an empty configuration path", ErrRuntimeSelector, source)
	}
	if strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("%w: %s configuration path contains a NUL byte", ErrRuntimeSelector, source)
	}
	root, err := filepath.Abs(".")
	if err != nil {
		return "", fmt.Errorf("%w: resolve runtime Project directory", ErrRuntimeSelector)
	}
	candidate := value
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("%w: resolve selected configuration path", ErrRuntimeSelector)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", fmt.Errorf("%w: selected configuration must identify a file within the runtime Project directory", ErrRuntimeSelector)
	}
	clean := filepath.Clean(relative)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: selected configuration must identify a file within the runtime Project directory", ErrRuntimeSelector)
	}
	return clean, nil
}

type runtimeConfigurationPathState struct {
	name string
	info os.FileInfo
}

func inspectRuntimeConfigurationPath(path string) ([]runtimeConfigurationPathState, error) {
	components := strings.Split(filepath.Clean(path), string(filepath.Separator))
	states := make([]runtimeConfigurationPathState, 0, len(components))
	current := ""
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || info == nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("configuration path is unavailable or symbolic")
		}
		if index < len(components)-1 {
			if !info.IsDir() {
				return nil, errors.New("configuration path contains a non-directory component")
			}
		} else if !info.Mode().IsRegular() {
			return nil, errors.New("configuration path is not a regular file")
		}
		states = append(states, runtimeConfigurationPathState{name: current, info: info})
	}
	return states, nil
}

func sameRuntimeConfigurationPathStates(left, right []runtimeConfigurationPathState) bool {
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].name != right[index].name || left[index].info == nil || right[index].info == nil ||
			left[index].info.Mode() != right[index].info.Mode() || !os.SameFile(left[index].info, right[index].info) {
			return false
		}
		if index == len(left)-1 && (left[index].info.Size() != right[index].info.Size() || !left[index].info.ModTime().Equal(right[index].info.ModTime())) {
			return false
		}
	}
	return true
}

func composeRuntimeDocuments(rootData, overlayData []byte) ([]byte, error) {
	root, err := decodeRuntimeDocument(rootData, defaultRuntimeDocument)
	if err != nil {
		return nil, err
	}
	var overlay *yaml.Node
	if overlayData != nil {
		overlay, err = decodeRuntimeDocument(overlayData, "environment overlay")
		if err != nil {
			return nil, err
		}
	}
	merged, err := mergeRuntimeDocument(root, overlay)
	if err != nil {
		return nil, err
	}
	return encodeRuntimeDocument(merged)
}

func normalizeRuntimeDocument(data []byte, source string) ([]byte, error) {
	root, err := decodeRuntimeDocument(data, source)
	if err != nil {
		return nil, err
	}
	normalized, err := mergeRuntimeDocument(root, nil)
	if err != nil {
		return nil, err
	}
	return encodeRuntimeDocument(normalized)
}

func encodeRuntimeDocument(document *yaml.Node) ([]byte, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		_ = encoder.Close()
		return nil, runtimeConfigurationError("encode effective runtime document")
	}
	if err := encoder.Close(); err != nil {
		return nil, runtimeConfigurationError("close effective runtime document")
	}
	if output.Len() > runtimeMaximumDocumentSize {
		return nil, runtimeConfigurationError("effective runtime document exceeds %d bytes", runtimeMaximumDocumentSize)
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func decodeRuntimeDocument(data []byte, source string) (*yaml.Node, error) {
	if len(data) == 0 || len(data) > runtimeMaximumDocumentSize {
		return nil, runtimeConfigurationError("%s must contain one bounded YAML document", source)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, runtimeConfigurationError("decode %s YAML", source)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, runtimeConfigurationError("%s must contain exactly one YAML document", source)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, runtimeConfigurationError("%s root must be a mapping", source)
	}
	if err := rejectRuntimeYAMLReferences(&document); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

func rejectRuntimeYAMLReferences(root *yaml.Node) error {
	stack := []*yaml.Node{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]
		if node == nil {
			continue
		}
		if node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
			return runtimeConfigurationError("YAML anchors and aliases are not allowed")
		}
		stack = append(stack, node.Content...)
	}
	return nil
}

func mergeRuntimeDocument(root, overlay *yaml.Node) (*yaml.Node, error) {
	allowed := runtimeKeySet("http", "timeouts", "interfaces", "config")
	lower, err := runtimeMapping(root, "document", allowed)
	if err != nil {
		return nil, err
	}
	upper, err := runtimeOptionalMapping(overlay, "environment overlay", allowed)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*yaml.Node)

	http, present, err := mergeRuntimeHTTP(lower["http"], upper["http"])
	if err != nil {
		return nil, err
	}
	if present {
		result["http"] = http
	}
	timeouts, present, err := mergeRuntimeTimeouts(lower["timeouts"], upper["timeouts"])
	if err != nil {
		return nil, err
	}
	if present {
		result["timeouts"] = timeouts
	}
	interfaces, present, err := mergeRuntimeInterfaces(lower["interfaces"], upper["interfaces"])
	if err != nil {
		return nil, err
	}
	if present {
		result["interfaces"] = interfaces
	}
	configuration, present, err := mergeRuntimeConfigurations(lower["config"], upper["config"])
	if err != nil {
		return nil, err
	}
	if present {
		result["config"] = configuration
	}
	return runtimeMappingNode(result), nil
}

func mergeRuntimeHTTP(lowerNode, upperNode *yaml.Node) (*yaml.Node, bool, error) {
	if lowerNode == nil && upperNode == nil {
		return nil, false, nil
	}
	allowed := runtimeKeySet("address", "transports", "cors", "expose")
	lower, err := runtimeOptionalMapping(lowerNode, "http", allowed)
	if err != nil {
		return nil, false, err
	}
	upper, err := runtimeOptionalMapping(upperNode, "http", allowed)
	if err != nil {
		return nil, false, err
	}
	result := make(map[string]*yaml.Node)
	address, present, err := selectRuntimeValue(lower["address"], upper["address"], "http.address", validateRuntimeAddress)
	if err != nil {
		return nil, false, err
	}
	if present {
		result["address"] = address
	}
	transports, present, err := mergeRuntimeTransports(lower["transports"], upper["transports"])
	if err != nil {
		return nil, false, err
	}
	if present {
		result["transports"] = transports
	}
	cors, present, err := mergeRuntimeCORS(lower["cors"], upper["cors"])
	if err != nil {
		return nil, false, err
	}
	if present {
		result["cors"] = cors
	}
	expose, present, err := mergeRuntimeInterfaceSet(lower["expose"], upper["expose"], "http.expose")
	if err != nil {
		return nil, false, err
	}
	if present {
		result["expose"] = expose
	}
	return runtimeMappingNode(result), true, nil
}

func mergeRuntimeTransports(lowerNode, upperNode *yaml.Node) (*yaml.Node, bool, error) {
	if lowerNode == nil && upperNode == nil {
		return nil, false, nil
	}
	allowed := runtimeKeySet("connect", "rest")
	lower, err := runtimeOptionalMapping(lowerNode, "http.transports", allowed)
	if err != nil {
		return nil, false, err
	}
	upper, err := runtimeOptionalMapping(upperNode, "http.transports", allowed)
	if err != nil {
		return nil, false, err
	}
	result := make(map[string]*yaml.Node)
	for _, name := range []string{"connect", "rest"} {
		value, present, err := selectRuntimeValue(lower[name], upper[name], "http.transports."+name, validateRuntimeBoolean)
		if err != nil {
			return nil, false, err
		}
		if present {
			result[name] = value
		}
	}
	return runtimeMappingNode(result), true, nil
}

func mergeRuntimeCORS(lowerNode, upperNode *yaml.Node) (*yaml.Node, bool, error) {
	if upperNode != nil && runtimeNull(upperNode) {
		return nil, false, nil
	}
	if lowerNode != nil && runtimeNull(lowerNode) {
		lowerNode = nil
	}
	if lowerNode == nil && upperNode == nil {
		return nil, false, nil
	}
	allowed := runtimeKeySet("allowed_origins", "allow_credentials")
	lower, err := runtimeOptionalMapping(lowerNode, "http.cors", allowed)
	if err != nil {
		return nil, false, err
	}
	upper, err := runtimeOptionalMapping(upperNode, "http.cors", allowed)
	if err != nil {
		return nil, false, err
	}
	origins, hasOrigins, err := selectRuntimeValue(lower["allowed_origins"], upper["allowed_origins"], "http.cors.allowed_origins", validateRuntimeOrigins)
	if err != nil {
		return nil, false, err
	}
	if !hasOrigins {
		return nil, false, runtimeConfigurationError("http.cors.allowed_origins is required when http.cors is present")
	}
	credentials, hasCredentials, err := selectRuntimeValue(lower["allow_credentials"], upper["allow_credentials"], "http.cors.allow_credentials", validateRuntimeBoolean)
	if err != nil {
		return nil, false, err
	}
	if hasCredentials && credentials.Value == "true" && runtimeSequenceContains(origins, "*") {
		return nil, false, runtimeConfigurationError("http.cors cannot combine wildcard origin with allow_credentials: true")
	}
	result := map[string]*yaml.Node{"allowed_origins": origins}
	if hasCredentials {
		result["allow_credentials"] = credentials
	}
	return runtimeMappingNode(result), true, nil
}

func mergeRuntimeTimeouts(lowerNode, upperNode *yaml.Node) (*yaml.Node, bool, error) {
	if lowerNode == nil && upperNode == nil {
		return nil, false, nil
	}
	allowed := runtimeKeySet("startup")
	lower, err := runtimeOptionalMapping(lowerNode, "timeouts", allowed)
	if err != nil {
		return nil, false, err
	}
	upper, err := runtimeOptionalMapping(upperNode, "timeouts", allowed)
	if err != nil {
		return nil, false, err
	}
	result := make(map[string]*yaml.Node)
	startup, present, err := selectRuntimeValue(lower["startup"], upper["startup"], "timeouts.startup", validateRuntimeDuration)
	if err != nil {
		return nil, false, err
	}
	if present {
		result["startup"] = startup
	}
	return runtimeMappingNode(result), true, nil
}

func mergeRuntimeInterfaces(lowerNode, upperNode *yaml.Node) (*yaml.Node, bool, error) {
	if lowerNode == nil && upperNode == nil {
		return nil, false, nil
	}
	allowed := runtimeKeySet("require", "use")
	lower, err := runtimeOptionalMapping(lowerNode, "interfaces", allowed)
	if err != nil {
		return nil, false, err
	}
	upper, err := runtimeOptionalMapping(upperNode, "interfaces", allowed)
	if err != nil {
		return nil, false, err
	}
	result := make(map[string]*yaml.Node)
	requirements, hasRequirements, err := mergeRuntimeInterfaceSet(lower["require"], upper["require"], "interfaces.require")
	if err != nil {
		return nil, false, err
	}
	if hasRequirements {
		result["require"] = requirements
	}
	uses, hasUses, err := mergeRuntimeImplementationChoices(lower["use"], upper["use"])
	if err != nil {
		return nil, false, err
	}
	if hasUses {
		result["use"] = uses
	}
	return runtimeMappingNode(result), true, nil
}

func mergeRuntimeInterfaceSet(lowerNode, upperNode *yaml.Node, path string) (*yaml.Node, bool, error) {
	if lowerNode == nil && upperNode == nil {
		return nil, false, nil
	}
	values := make(map[string]struct{})
	if err := applyRuntimeInterfaceSet(values, lowerNode, path); err != nil {
		return nil, false, err
	}
	if err := applyRuntimeInterfaceSet(values, upperNode, path); err != nil {
		return nil, false, err
	}
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)
	return runtimeStringSequence(ordered), true, nil
}

func applyRuntimeInterfaceSet(values map[string]struct{}, node *yaml.Node, path string) error {
	if node == nil {
		return nil
	}
	var addNode, removeNode *yaml.Node
	switch node.Kind {
	case yaml.SequenceNode:
		addNode = node
	case yaml.MappingNode:
		mapping, err := runtimeMapping(node, path, runtimeKeySet("add", "remove"))
		if err != nil {
			return err
		}
		addNode, removeNode = mapping["add"], mapping["remove"]
	default:
		return runtimeConfigurationError("%s must be a sequence or sparse {add, remove} mapping", path)
	}
	adds, err := runtimeInterfaceSequence(addNode, path+".add")
	if err != nil {
		return err
	}
	removes, err := runtimeInterfaceSequence(removeNode, path+".remove")
	if err != nil {
		return err
	}
	for value := range adds {
		if _, conflict := removes[value]; conflict {
			return runtimeConfigurationError("%s cannot both add and remove Interface %q", path, value)
		}
		values[value] = struct{}{}
	}
	for value := range removes {
		delete(values, value)
	}
	return nil
}

func runtimeInterfaceSequence(node *yaml.Node, path string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	if node == nil {
		return result, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, runtimeConfigurationError("%s must be a sequence of canonical Interface IDs", path)
	}
	for index, item := range node.Content {
		value, err := runtimeString(item)
		if err != nil {
			return nil, runtimeConfigurationError("%s[%d] must be a canonical Interface ID string", path, index)
		}
		if !validRuntimeInterfaceID(value) {
			return nil, runtimeConfigurationError("%s[%d] is not a canonical Interface ID", path, index)
		}
		if _, duplicate := result[value]; duplicate {
			return nil, runtimeConfigurationError("%s repeats Interface %q", path, value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func mergeRuntimeImplementationChoices(lowerNode, upperNode *yaml.Node) (*yaml.Node, bool, error) {
	if lowerNode == nil && upperNode == nil {
		return nil, false, nil
	}
	values := make(map[string]*yaml.Node)
	for _, layer := range []*yaml.Node{lowerNode, upperNode} {
		mapping, err := runtimeOptionalMapping(layer, "interfaces.use", nil)
		if err != nil {
			return nil, false, err
		}
		for interfaceID, value := range mapping {
			if !validRuntimeSelectableInterfaceID(interfaceID) {
				return nil, false, runtimeConfigurationError("interfaces.use key %q is not a selectable canonical Interface ID", interfaceID)
			}
			if runtimeNull(value) {
				delete(values, interfaceID)
				continue
			}
			constructor, valueErr := runtimeString(value)
			if valueErr != nil || !validRuntimeConstructorSymbol(constructor) {
				return nil, false, runtimeConfigurationError("interfaces.use[%q] must be a fully qualified Implementation constructor symbol or null", interfaceID)
			}
			values[interfaceID] = runtimeClone(value)
		}
	}
	return runtimeMappingNode(values), true, nil
}

func validRuntimeInterfaceID(value string) bool {
	name, version, found := strings.Cut(value, "/")
	if !found || strings.Contains(version, "/") || len(version) < 2 || version[0] != 'v' || version[1] == '0' {
		return false
	}
	major, err := strconv.ParseUint(version[1:], 10, 64)
	if err != nil || major == 0 {
		return false
	}
	segments := strings.Split(name, ".")
	if len(segments) < 2 {
		return false
	}
	for _, segment := range segments {
		if segment == "" || segment[0] < 'a' || segment[0] > 'z' {
			return false
		}
		for index := 1; index < len(segment); index++ {
			character := segment[index]
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validRuntimeSelectableInterfaceID(value string) bool {
	if !validRuntimeInterfaceID(value) {
		return false
	}
	name, _, _ := strings.Cut(value, "/")
	return !strings.HasPrefix(name, "kernel.")
}

func validRuntimeConstructorSymbol(value string) bool {
	separator := strings.LastIndexByte(value, '.')
	if separator <= 0 || separator == len(value)-1 || !validRuntimeImportPath(value[:separator]) {
		return false
	}
	function := value[separator+1:]
	if !token.IsIdentifier(function) {
		return false
	}
	for _, first := range function {
		return unicode.IsUpper(first)
	}
	return false
}

func validRuntimeImportPath(value string) bool {
	if value == "" || value[0] == '-' || strings.Contains(value, "//") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, element := range strings.Split(value, "/") {
		if element == "" || strings.Count(element, ".") == len(element) || strings.HasSuffix(element, ".") {
			return false
		}
		for _, character := range element {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') &&
				!strings.ContainsRune("-._~+", character) {
				return false
			}
		}
		short := element
		if dot := strings.IndexByte(short, '.'); dot >= 0 {
			short = short[:dot]
		}
		for _, reserved := range []string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"} {
			if strings.EqualFold(short, reserved) {
				return false
			}
		}
		if tilde := strings.LastIndexByte(short, '~'); tilde >= 0 && tilde < len(short)-1 {
			digits := true
			for _, character := range short[tilde+1:] {
				if character < '0' || character > '9' {
					digits = false
					break
				}
			}
			if digits {
				return false
			}
		}
	}
	return true
}

func mergeRuntimeConfigurations(lowerNode, upperNode *yaml.Node) (*yaml.Node, bool, error) {
	if lowerNode == nil && upperNode == nil {
		return nil, false, nil
	}
	lower, err := runtimeOptionalMapping(lowerNode, "config", nil)
	if err != nil {
		return nil, false, err
	}
	upper, err := runtimeOptionalMapping(upperNode, "config", nil)
	if err != nil {
		return nil, false, err
	}
	pluginIDs := make(map[string]struct{}, len(lower)+len(upper))
	for pluginID := range lower {
		pluginIDs[pluginID] = struct{}{}
	}
	for pluginID := range upper {
		pluginIDs[pluginID] = struct{}{}
	}
	result := make(map[string]*yaml.Node)
	for pluginID := range pluginIDs {
		schema, selected := runtimeConfigurationSchemas[pluginID]
		if !selected {
			return nil, false, runtimeConfigurationError("config targets unselected Plugin %q", pluginID)
		}
		if parsed, parseErr := kernelplugin.ParseID(pluginID); parseErr != nil || parsed.String() != pluginID {
			return nil, false, runtimeConfigurationError("config key %q is not a canonical Plugin ID", pluginID)
		}
		upperValue, hasUpper := upper[pluginID]
		if hasUpper && runtimeNull(upperValue) {
			continue
		}
		lowerValue := lower[pluginID]
		if runtimeNull(lowerValue) {
			lowerValue = nil
		}
		merged, mergeErr := mergeRuntimePluginConfiguration(pluginID, lowerValue, upperValue, schema)
		if mergeErr != nil {
			return nil, false, mergeErr
		}
		if merged != nil {
			result[pluginID] = merged
		}
	}
	return runtimeMappingNode(result), true, nil
}

func mergeRuntimePluginConfiguration(pluginID string, lowerNode, upperNode *yaml.Node, schema map[string]runtimeConfigurationFieldKind) (*yaml.Node, error) {
	path := "config[" + strconv.Quote(pluginID) + "]"
	lower, err := runtimeOptionalMapping(lowerNode, path, nil)
	if err != nil {
		return nil, err
	}
	upper, err := runtimeOptionalMapping(upperNode, path, nil)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]struct{}, len(lower)+len(upper))
	for field := range lower {
		fields[field] = struct{}{}
	}
	for field := range upper {
		fields[field] = struct{}{}
	}
	result := make(map[string]*yaml.Node)
	for field := range fields {
		kind, exists := schema[field]
		if !exists {
			return nil, runtimeConfigurationError("%s contains unknown field %q", path, field)
		}
		selected := lower[field]
		if runtimeNull(selected) {
			selected = nil
		}
		if selected != nil {
			if validateErr := validateRuntimeConfigurationField(selected, kind, path+"["+strconv.Quote(field)+"]"); validateErr != nil {
				return nil, validateErr
			}
		}
		if overlay, hasOverlay := upper[field]; hasOverlay {
			if runtimeNull(overlay) {
				continue
			}
			if validateErr := validateRuntimeConfigurationField(overlay, kind, path+"["+strconv.Quote(field)+"]"); validateErr != nil {
				return nil, validateErr
			}
			if kind == runtimeConfigurationObject {
				merged, mergeErr := mergeRuntimeOpenObject(selected, overlay, path+"["+strconv.Quote(field)+"]")
				if mergeErr != nil {
					return nil, mergeErr
				}
				result[field] = merged
				continue
			}
			selected = overlay
		}
		if selected == nil {
			continue
		}
		if kind == runtimeConfigurationObject && selected.Kind != yaml.MappingNode {
			return nil, runtimeConfigurationError("%s[%q] must remain an object", path, field)
		}
		result[field] = runtimeClone(selected)
	}
	return runtimeMappingNode(result), nil
}

func validateRuntimeConfigurationField(node *yaml.Node, kind runtimeConfigurationFieldKind, path string) error {
	valid := false
	want := ""
	switch kind {
	case runtimeConfigurationString:
		valid, want = node.Kind == yaml.ScalarNode && node.Tag == "!!str", "string"
	case runtimeConfigurationInteger:
		valid, want = node.Kind == yaml.ScalarNode && node.Tag == "!!int", "integer"
	case runtimeConfigurationNumber:
		valid, want = node.Kind == yaml.ScalarNode && (node.Tag == "!!int" || node.Tag == "!!float"), "number"
	case runtimeConfigurationBoolean:
		valid, want = node.Kind == yaml.ScalarNode && node.Tag == "!!bool", "boolean"
	case runtimeConfigurationObject:
		valid, want = node.Kind == yaml.MappingNode, "object"
	case runtimeConfigurationArray:
		valid, want = node.Kind == yaml.SequenceNode, "array"
	case runtimeConfigurationSecret:
		valid, want = node.Kind == yaml.MappingNode, "Secret reference"
	default:
		return runtimeConfigurationError("%s has an unsupported declared type", path)
	}
	if !valid {
		return runtimeConfigurationError("%s must be a %s", path, want)
	}
	return nil
}

func mergeRuntimeOpenObject(lowerNode, upperNode *yaml.Node, path string) (*yaml.Node, error) {
	lower, err := runtimeOptionalMapping(lowerNode, path, nil)
	if err != nil {
		return nil, err
	}
	upper, err := runtimeOptionalMapping(upperNode, path, nil)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*yaml.Node, len(lower)+len(upper))
	for name, value := range lower {
		if !runtimeNull(value) {
			result[name] = runtimeClone(value)
		}
	}
	for name, value := range upper {
		if runtimeNull(value) {
			delete(result, name)
			continue
		}
		if existing := result[name]; existing != nil {
			if err := validateRuntimeOpenObjectType(existing, value, path+"["+strconv.Quote(name)+"]"); err != nil {
				return nil, err
			}
			if value.Kind == yaml.MappingNode {
				merged, mergeErr := mergeRuntimeOpenObject(existing, value, path+"["+strconv.Quote(name)+"]")
				if mergeErr != nil {
					return nil, mergeErr
				}
				result[name] = merged
				continue
			}
		}
		result[name] = runtimeClone(value)
	}
	return runtimeMappingNode(result), nil
}

func validateRuntimeOpenObjectType(lowerNode, upperNode *yaml.Node, path string) error {
	lowerType, err := runtimeOpenObjectValueType(lowerNode)
	if err != nil {
		return runtimeConfigurationError("%s has an unsupported lower value", path)
	}
	upperType, err := runtimeOpenObjectValueType(upperNode)
	if err != nil {
		return runtimeConfigurationError("%s has an unsupported overlay value", path)
	}
	if lowerType != upperType {
		return runtimeConfigurationError("%s cannot change type from %s to %s", path, lowerType, upperType)
	}
	return nil
}

func runtimeOpenObjectValueType(node *yaml.Node) (string, error) {
	if node == nil {
		return "", errors.New("value is absent")
	}
	switch node.Kind {
	case yaml.MappingNode:
		return "object", nil
	case yaml.SequenceNode:
		return "array", nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			return "string", nil
		case "!!bool":
			return "boolean", nil
		case "!!int", "!!float":
			return "number", nil
		default:
			return "", errors.New("unsupported scalar")
		}
	default:
		return "", errors.New("unsupported value")
	}
}

type runtimeValueValidator func(*yaml.Node, string) (*yaml.Node, error)

func selectRuntimeValue(lowerNode, upperNode *yaml.Node, path string, validate runtimeValueValidator) (*yaml.Node, bool, error) {
	selected := lowerNode
	if runtimeNull(selected) {
		selected = nil
	}
	if upperNode != nil {
		selected = upperNode
		if runtimeNull(selected) {
			return nil, false, nil
		}
	}
	if selected == nil {
		return nil, false, nil
	}
	value, err := validate(selected, path)
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func validateRuntimeAddress(node *yaml.Node, path string) (*yaml.Node, error) {
	value, err := runtimeString(node)
	if err != nil || value == "" || len(value) > 4096 || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return nil, runtimeConfigurationError("%s must be a non-empty trimmed string of at most 4096 bytes", path)
	}
	return runtimeClone(node), nil
}

func validateRuntimeBoolean(node *yaml.Node, path string) (*yaml.Node, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!bool" || node.Value != "true" && node.Value != "false" {
		return nil, runtimeConfigurationError("%s must be true or false", path)
	}
	return runtimeClone(node), nil
}

func validateRuntimeDuration(node *yaml.Node, path string) (*yaml.Node, error) {
	value, err := runtimeString(node)
	if err != nil || value == "" || len(value) > 64 || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return nil, runtimeConfigurationError("%s must be a positive Go duration string", path)
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return nil, runtimeConfigurationError("%s must be a positive Go duration string", path)
	}
	return runtimeClone(node), nil
}

func validateRuntimeOrigins(node *yaml.Node, path string) (*yaml.Node, error) {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, runtimeConfigurationError("%s must be a nonempty sequence of origins", path)
	}
	values := make(map[string]struct{}, len(node.Content))
	for index, item := range node.Content {
		value, err := runtimeString(item)
		if err != nil {
			return nil, runtimeConfigurationError("%s[%d] must be an origin string", path, index)
		}
		normalized, err := normalizeRuntimeOrigin(value)
		if err != nil {
			return nil, runtimeConfigurationError("%s[%d] is not a valid origin", path, index)
		}
		values[normalized] = struct{}{}
	}
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)
	return runtimeStringSequence(ordered), nil
}

func normalizeRuntimeOrigin(raw string) (string, error) {
	if raw == "*" {
		return raw, nil
	}
	if raw == "" || len(raw) > 4096 || strings.TrimSpace(raw) != raw || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", errors.New("invalid origin")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("invalid origin")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("invalid origin")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || strings.IndexFunc(host, unicode.IsControl) >= 0 || strings.IndexFunc(host, func(value rune) bool { return value > unicode.MaxASCII }) >= 0 || strings.ContainsAny(host, " /\\%") {
		return "", errors.New("invalid origin")
	}
	if parsedIP := net.ParseIP(host); parsedIP != nil {
		host = parsedIP.String()
	}
	port := parsed.Port()
	if strings.HasSuffix(parsed.Host, ":") {
		return "", errors.New("invalid origin")
	}
	if port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return "", errors.New("invalid origin")
		}
		if scheme == "http" && value == 80 || scheme == "https" && value == 443 {
			port = ""
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	return scheme + "://" + host, nil
}

func runtimeOptionalMapping(node *yaml.Node, path string, allowed map[string]struct{}) (map[string]*yaml.Node, error) {
	if node == nil {
		return map[string]*yaml.Node{}, nil
	}
	return runtimeMapping(node, path, allowed)
}

func runtimeMapping(node *yaml.Node, path string, allowed map[string]struct{}) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, runtimeConfigurationError("%s must be a mapping", path)
	}
	result := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, err := runtimeString(node.Content[index])
		if err != nil {
			return nil, runtimeConfigurationError("%s contains a non-string key", path)
		}
		if _, duplicate := result[key]; duplicate {
			return nil, runtimeConfigurationError("%s contains duplicate key %q", path, key)
		}
		if allowed != nil {
			if _, known := allowed[key]; !known {
				return nil, runtimeConfigurationError("%s contains unknown key %q", path, key)
			}
		}
		result[key] = node.Content[index+1]
	}
	return result, nil
}

func runtimeMappingNode(values map[string]*yaml.Node) *yaml.Node {
	result := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result.Content = append(result.Content, runtimeStringNode(key), runtimeClone(values[key]))
	}
	return result
}

func runtimeStringSequence(values []string) *yaml.Node {
	result := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		result.Content = append(result.Content, runtimeStringNode(value))
	}
	return result
}

func runtimeStringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func runtimeString(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", errors.New("not a string")
	}
	return node.Value, nil
}

func runtimeNull(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

func runtimeClone(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	result := *node
	result.Alias = nil
	result.Anchor = ""
	result.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		result.Content[index] = runtimeClone(child)
	}
	return &result
}

func runtimeKeySet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func runtimeSequenceContains(node *yaml.Node, value string) bool {
	if node == nil || node.Kind != yaml.SequenceNode {
		return false
	}
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode && item.Tag == "!!str" && item.Value == value {
			return true
		}
	}
	return false
}

func runtimeConfigurationError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrRuntimeConfiguration, fmt.Sprintf(format, arguments...))
}
`
