package bootstrapgen

const runtimeConfigurationCompositionSupport = `
// Compose the current Project root with one selected overlay, or use one
// complete replacement as the only application configuration layer.
func composeRuntimeDocument(baseline runtimebaseline.Document, rootData, selectedData, overlayData []byte) ([]byte, error) {
	root, err := decodeRuntimeDocument(rootData, defaultRuntimeDocument)
	if err != nil { return nil, err }
	rootFields, err := runtimeMapping(root, "root configuration", runtimeKeySet("http", "timeouts", "capabilities", "interfaces", "config", "resources", "data"))
	if err != nil { return nil, err }
	current := runtimeMappingNode(rootFields)
	layers := make([]map[string]*yaml.Node, 0, 2)
	if selectedData != nil {
		current, err = decodeRuntimeDocument(selectedData, "replacement configuration")
		if err != nil { return nil, err }
		currentFields, err := runtimeApplicationLayer(current)
		if err != nil { return nil, err }
		layers = append(layers, currentFields)
	} else {
		currentFields, err := runtimeApplicationLayer(current)
		if err != nil { return nil, err }
		layers = append(layers, currentFields)
		if overlayData != nil {
			overlay, err := decodeRuntimeDocument(overlayData, "environment overlay")
			if err != nil { return nil, err }
			fields, err := runtimeApplicationLayer(overlay)
			if err != nil { return nil, err }
			layers = append(layers, fields)
		}
	}
	result := runtimeMappingNode(nil)
	configurations := make([]*yaml.Node, 0, len(layers))
	resources := make([]*yaml.Node, 0, len(layers))
	for _, layer := range layers {
		configurations = append(configurations, layer["config"])
		delete(layer, "config")
		resources = append(resources, layer["resources"])
		delete(layer, "resources")
		result, err = mergeRuntimeDocument(result, runtimeMappingNode(layer))
		if err != nil { return nil, err }
	}
	fields, err := runtimeMapping(result, "effective configuration", nil)
	if err != nil { return nil, err }
	if http := fields["http"]; http != nil {
		if err := validateRuntimeEffectiveCORS(http); err != nil { return nil, err }
	}
	inventory, err := runtimeConstructorInventory(baseline)
	if err != nil { return nil, err }
	owners, err := runtimeConfigurationOwners(fields["interfaces"], inventory)
	if err != nil { return nil, err }
	fields["config"], err = composeRuntimeConfigurations(configurations, inventory, owners)
	if err != nil { return nil, err }
	providers, err := runtimeResourceInventory(baseline)
	if err != nil { return nil, err }
	fields["resources"], err = composeRuntimeResources(resources, providers, inventory)
	if err != nil { return nil, err }
	return encodeRuntimeDocument(runtimeMappingNode(fields))
}

func runtimeApplicationLayer(document *yaml.Node) (map[string]*yaml.Node, error) {
	fields, err := runtimeMapping(document, "application configuration", runtimeKeySet("http", "timeouts", "interfaces", "config", "resources", "data"))
	if err != nil { return nil, err }
	if fields["data"] != nil { return nil, runtimeConfigurationError("Data is not supported by this runtime") }
	return fields, nil
}

func runtimeConfigurationOwners(interfaces *yaml.Node, inventory map[string]runtimeConstructorInventoryEntry) (map[string]bool, error) {
	fields, err := runtimeOptionalMapping(interfaces, "interfaces", nil)
	if err != nil { return nil, err }
	uses, err := runtimeOptionalMapping(fields["use"], "interfaces.use", nil)
	if err != nil { return nil, err }
	owners := make(map[string]bool)
	ids := make([]string, 0, len(uses))
	for id := range uses { ids = append(ids, id) }
	sort.Strings(ids)
	for _, id := range ids {
		// Intrinsic choices are rejected by executable compatibility and own no Config.
		if strings.HasPrefix(id, "kernel.") { continue }
		symbol := uses[id].Value
		if _, active := runtimeExecutableInterfaceChoices[id]; !active {
			entry, exists := inventory[symbol]
			index := sort.SearchStrings(entry.Interfaces, id)
			if !exists || index == len(entry.Interfaces) || entry.Interfaces[index] != id {
				return nil, runtimeConfigurationError("interfaces.use[%q] does not match the constructor inventory", id)
			}
		}
		owners[symbol] = true
	}
	return owners, nil
}

func composeRuntimeConfigurations(layers []*yaml.Node, inventory map[string]runtimeConstructorInventoryEntry, owners map[string]bool) (*yaml.Node, error) {
	objects := make(map[string][]*yaml.Node)
	for i, layer := range layers {
		entries, err := runtimeOptionalMapping(layer, "config", nil)
		if err != nil { return nil, err }
		for symbol, value := range entries {
			if objects[symbol] == nil { objects[symbol] = make([]*yaml.Node, len(layers)) }
			objects[symbol][i] = value
		}
	}
	symbols := make([]string, 0, len(objects))
	for symbol := range objects { symbols = append(symbols, symbol) }
	sort.Strings(symbols)
	result := make(map[string]*yaml.Node)
	for _, symbol := range symbols {
		schema, active, err := runtimeConstructorSchema(symbol)
		if err != nil { return nil, err }
		entry, visible := inventory[symbol]
		if !active && entry.Schema != nil { schema = *entry.Schema }
		if active || entry.Schema != nil {
			node, err := constructorconfig.ComposeLayers(schema, objects[symbol]...)
			if err != nil { return nil, fmt.Errorf("%w: constructor %s: %w", ErrRuntimeConfiguration, symbol, err) }
			if active {
				if node != nil { result[symbol] = node }
			} else if node != nil {
				if !owners[symbol] { return nil, runtimeConfigurationError("config for %s has no effective constructor owner", symbol) }
				if _, err := constructorconfig.Normalize(schema, node); err != nil { return nil, fmt.Errorf("%w: constructor %s: %w", ErrRuntimeConfiguration, symbol, err) }
			}
			continue
		}
		if visible || validRuntimeConstructorSymbol(symbol) { return nil, runtimeConfigurationError("config for %s has no schema in the constructor inventory", symbol) }
		var composed *yaml.Node
		for _, node := range objects[symbol] {
			if node == nil { continue }
			composed, _, err = mergeRuntimeConfigurations(composed, runtimeMappingNode(map[string]*yaml.Node{symbol: node}))
			if err != nil { return nil, err }
		}
		values, err := runtimeOptionalMapping(composed, "config", nil)
		if err != nil { return nil, err }
		if values[symbol] != nil { result[symbol] = values[symbol] }
	}
	return runtimeMappingNode(result), nil
}
`
