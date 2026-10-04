package bootstrapgen

const runtimeTemplateSupport = `
// Apply each raw layer only after its lower ancestry. Normalizing a delta first
// would discard tombstones and complete-set boundaries needed by that ancestry.
func composeRuntimeTemplateDocument(baseline runtimebaseline.Document, rootData, selectedData, overlayData []byte) ([]byte, error) {
	root, err := decodeRuntimeDocument(rootData, defaultRuntimeDocument)
	if err != nil { return nil, err }
	rootFields, err := runtimeMapping(root, "root configuration", runtimeKeySet("template", "http", "timeouts", "capabilities", "interfaces", "config", "resources", "data"))
	if err != nil { return nil, err }
	relationship := ""
	if node := rootFields["template"]; node != nil {
		relationship, err = runtimeString(node)
		if err != nil || modulepath.CheckProject(relationship) != nil { return nil, runtimeConfigurationError("template must be an exact Project module path") }
	}
	layers, err := runtimeTemplateLayers(baseline, relationship)
	if err != nil { return nil, err }
	delete(rootFields, "template")
	current := runtimeMappingNode(rootFields)
	if selectedData != nil {
		current, err = decodeRuntimeDocument(selectedData, "replacement configuration")
		if err != nil { return nil, err }
	}
	currentFields, err := runtimeApplicationLayer(current, false)
	if err != nil { return nil, err }
	layers = append(layers, currentFields)
	if overlayData != nil {
		overlay, err := decodeRuntimeDocument(overlayData, "environment overlay")
		if err != nil { return nil, err }
		fields, err := runtimeApplicationLayer(overlay, false)
		if err != nil { return nil, err }
		layers = append(layers, fields)
	}
	result := runtimeMappingNode(nil)
	configurations := make([]*yaml.Node, 0, len(layers))
	for _, layer := range layers {
		configurations = append(configurations, layer["config"])
		delete(layer, "config")
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
	fields["config"], err = composeRuntimeTemplateConfigurations(configurations, inventory, owners)
	if err != nil { return nil, err }
	return encodeRuntimeDocument(runtimeMappingNode(fields))
}

func runtimeTemplateLayers(baseline runtimebaseline.Document, relationship string) ([]map[string]*yaml.Node, error) {
	var contract struct {
		Module string
		Template string
		Templates []struct { Module string; Version string; Template string } ` + "`json:\"template_ancestry\"`" + `
	}
	if json.Unmarshal(baseline.Contract, &contract) != nil || modulepath.CheckProject(contract.Module) != nil || contract.Templates == nil || len(contract.Templates) != len(baseline.Templates) { return nil, runtimebaseline.ErrBaseline }
	if relationship != contract.Template {
		return nil, fmt.Errorf("%w: root template relationship changed; regenerate and rebuild with the same selector", ErrRuntimeCompatibility)
	}
	seen := map[string]bool{contract.Module: true}
	previous := ""
	layers := make([]map[string]*yaml.Node, 0, len(baseline.Templates))
	for i, template := range baseline.Templates {
		expected := contract.Templates[i]
		if template.Module != expected.Module || template.Version != expected.Version || template.Template != expected.Template || modulepath.CheckProject(template.Module) != nil || seen[template.Module] || template.Template != previous { return nil, runtimebaseline.ErrBaseline }
		seen[template.Module] = true
		previous = template.Module
		document, err := decodeRuntimeDocument([]byte(template.YAML), "private template baseline")
		if err != nil { return nil, runtimebaseline.ErrBaseline }
		fields, err := runtimeApplicationLayer(document, true)
		if err != nil { return nil, err }
		layers = append(layers, fields)
	}
	if contract.Template != previous { return nil, runtimebaseline.ErrBaseline }
	return layers, nil
}

func runtimeApplicationLayer(document *yaml.Node, inherited bool) (map[string]*yaml.Node, error) {
	fields, err := runtimeMapping(document, "application configuration", runtimeKeySet("http", "timeouts", "interfaces", "config", "resources", "data"))
	if err != nil { return nil, err }
	if fields["resources"] != nil || fields["data"] != nil { return nil, runtimeConfigurationError("Resources and Data are not supported by this runtime") }
	if inherited {
		if fields["timeouts"] != nil { return nil, runtimeConfigurationError("private template baseline cannot contain process settings") }
		http, err := runtimeOptionalMapping(fields["http"], "template http", runtimeKeySet("cors", "expose"))
		if err != nil { return nil, err }
		if fields["http"] != nil { fields["http"] = runtimeMappingNode(http) }
	}
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

func composeRuntimeTemplateConfigurations(layers []*yaml.Node, inventory map[string]runtimeConstructorInventoryEntry, owners map[string]bool) (*yaml.Node, error) {
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
