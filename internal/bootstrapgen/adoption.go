package bootstrapgen

const runtimeAdoptionSupport = `
// Resolve adoptions before normalizing current intent: normalization would
// otherwise discard exclusions needed to suppress lower exports.
func composeRuntimeAdoptedDocument(baseline runtimebaseline.Document, rootData, selectedData, overlayData []byte) ([]byte, error) {
	root, err := decodeRuntimeDocument(rootData, defaultRuntimeDocument)
	if err != nil { return nil, err }
	current := root
	if selectedData != nil {
		current, err = decodeRuntimeDocument(selectedData, "replacement configuration")
		if err != nil { return nil, err }
	}
	var overlay *yaml.Node
	if overlayData != nil {
		overlay, err = decodeRuntimeDocument(overlayData, "environment overlay")
		if err != nil { return nil, err }
	}
	allowed := runtimeKeySet("composition", "http", "timeouts", "interfaces", "config")
	lower, err := runtimeMapping(current, "configuration", allowed)
	if err != nil { return nil, err }
	upper, err := runtimeOptionalMapping(overlay, "environment overlay", allowed)
	if err != nil { return nil, err }
	if selectedData != nil {
		if _, err := runtimeOptionalMapping(lower["composition"], "composition", runtimeKeySet("adopt")); err != nil { return nil, err }
	}
	composition, _, err := mergeRuntimeComposition(lower["composition"], upper["composition"])
	if err != nil { return nil, err }
	adoptions, err := runtimeApplicationModelAdoptions(composition)
	if err != nil { return nil, err }
	inventories, err := runtimeExportInventories(baseline, root)
	if err != nil { return nil, err }
	peers := make([]map[string]*yaml.Node, 0, len(adoptions))
	sources := make([]string, 0, len(adoptions))
	for _, adoption := range adoptions {
		module, name := adoption["module"].(string), adoption["export"].(string)
		exports, exists := inventories[module]
		if !exists { return nil, runtimeConfigurationError("adoption %s#%s refers to a module outside the runtime baseline", module, name) }
		export, exists := exports[name]
		if !exists { return nil, runtimeConfigurationError("adoption %s#%s has no export in the runtime inventory", module, name) }
		fields, err := runtimeMapping(export, "adopted export", runtimeKeySet("interfaces", "config"))
		if err != nil { return nil, runtimeConfigurationError("adoption %s#%s contains unsupported declarations", module, name) }
		if err := validateRuntimeExport(fields); err != nil { return nil, fmt.Errorf("%w: adoption %s#%s", err, module, name) }
		peers = append(peers, fields)
		sources = append(sources, module+":plystra.yaml:composition.exports["+strconv.Quote(name)+"]")
	}
	interfaces, err := composeRuntimeExportInterfaces(peers, lower["interfaces"], upper["interfaces"])
	if err != nil { return nil, runtimeAdoptionError(err, sources) }
	inventory, err := runtimeConstructorInventory(baseline)
	if err != nil { return nil, err }
	owners, err := runtimeConfigurationOwners(interfaces, inventory)
	if err != nil { return nil, err }
	configuration, err := composeRuntimeExportConfigurations(peers, lower["config"], upper["config"], inventory, owners)
	if err != nil { return nil, runtimeAdoptionError(err, sources) }
	// The remaining fields are owned exclusively by the current Project.
	delete(lower, "interfaces"); delete(upper, "interfaces")
	delete(lower, "config"); delete(upper, "config")
	result, err := mergeRuntimeDocument(runtimeMappingNode(lower), runtimeMappingNode(upper))
	if err != nil { return nil, err }
	fields, err := runtimeMapping(result, "effective configuration", nil)
	if err != nil { return nil, err }
	fields["interfaces"], fields["config"] = interfaces, configuration
	return encodeRuntimeDocument(runtimeMappingNode(fields))
}

func runtimeAdoptionError(err error, sources []string) error {
	if len(sources) == 0 { return err }
	return fmt.Errorf("%w; selected exports: %s", err, strings.Join(sources, ", "))
}

func runtimeExportInventories(baseline runtimebaseline.Document, root *yaml.Node) (map[string]map[string]*yaml.Node, error) {
	var contract struct {
		Module string
		Dependencies []struct { Module string; Version string } ` + "`json:\"dependency_modules\"`" + `
	}
	if json.Unmarshal(baseline.Contract, &contract) != nil || len(contract.Dependencies) != len(baseline.Exports) { return nil, runtimebaseline.ErrBaseline }
	local, err := runtimeExportInventory(root, false)
	if err != nil { return nil, err }
	inventories := map[string]map[string]*yaml.Node{contract.Module: local}
	for i, dependency := range baseline.Exports {
		expected := contract.Dependencies[i]
		if dependency.Module != expected.Module || dependency.Version != expected.Version { return nil, runtimebaseline.ErrBaseline }
		if _, exists := inventories[dependency.Module]; exists { return nil, runtimebaseline.ErrBaseline }
		document, err := decodeRuntimeDocument([]byte(dependency.YAML), "private dependency export inventory")
		if err != nil { return nil, runtimebaseline.ErrBaseline }
		exports, err := runtimeExportInventory(document, true)
		if err != nil { return nil, runtimebaseline.ErrBaseline }
		inventories[dependency.Module] = exports
	}
	return inventories, nil
}

func runtimeExportInventory(root *yaml.Node, dependency bool) (map[string]*yaml.Node, error) {
	allowed := runtimeKeySet("composition", "http", "timeouts", "interfaces", "config", "resources", "data", "capabilities")
	if dependency { allowed = runtimeKeySet("composition") }
	fields, err := runtimeMapping(root, "export inventory", allowed)
	if err != nil { return nil, err }
	allowed = runtimeKeySet("adopt", "exports")
	if dependency { allowed = runtimeKeySet("exports") }
	composition, err := runtimeOptionalMapping(fields["composition"], "composition", allowed)
	if err != nil { return nil, err }
	exports, err := runtimeOptionalMapping(composition["exports"], "composition.exports", nil)
	if err != nil { return nil, err }
	for name, export := range exports {
		if !validRuntimeExportName(name) { return nil, runtimeConfigurationError("invalid export name in inventory") }
		fields, err := runtimeMapping(export, "export", runtimeKeySet("interfaces", "config", "resources"))
		if err != nil { return nil, runtimeConfigurationError("export %s contains unsupported declarations", name) }
		if err := validateRuntimeExport(fields); err != nil { return nil, runtimeConfigurationError("export %s has invalid declarations or reserved removal mappings", name) }
	}
	return exports, nil
}

func validateRuntimeExport(fields map[string]*yaml.Node) error {
	interfaces, err := runtimeOptionalMapping(fields["interfaces"], "adopted interfaces", runtimeKeySet("require", "use", "policies"))
	if err != nil { return err }
	if _, err := runtimeInterfaceSequence(interfaces["require"], "adopted interfaces.require"); err != nil { return err }
	if _, _, err := mergeRuntimeImplementationChoices(interfaces["use"], nil); err != nil { return err }
	if _, _, err := mergeRuntimeInterfacePolicies(interfaces["policies"], nil); err != nil { return err }
	if node := fields["resources"]; node != nil {
		if err := validateRuntimeExportResources(node); err != nil { return err }
	}
	// Exports have no lower layer. Reserved removals remain invalid inside
	// dormant or suppressed objects as well as active typed values.
	for _, node := range fields {
		stack := []*yaml.Node{node}
		for len(stack) > 0 {
			value := stack[len(stack)-1]; stack = stack[:len(stack)-1]
			if value.Kind == yaml.MappingNode {
				if len(value.Content) == 2 && value.Content[0].Value == "$remove" { return runtimeConfigurationError("adopted exports cannot contain reserved removal mappings") }
				if _, err := runtimeMapping(value, "export mapping", nil); err != nil { return runtimeConfigurationError("export mappings require unique string keys") }
			}
			stack = append(stack, value.Content...)
		}
	}
	objects, err := runtimeOptionalMapping(fields["config"], "adopted config", nil)
	if err != nil { return err }
	for symbol, node := range objects {
		if !validRuntimeConstructorSymbol(symbol) { return runtimeConfigurationError("adopted config contains an invalid constructor symbol") }
		if _, err := runtimeMapping(node, "adopted config", nil); err != nil { return runtimeConfigurationError("adopted config contains an invalid object") }
	}
	return nil
}

// Resource inventories remain inert. Validate only authored syntax, without
// requiring complete providers or resolving cross-export binding targets.
func validateRuntimeExportResources(node *yaml.Node) error {
	fields, err := runtimeMapping(node, "export resources", runtimeKeySet("instances", "bind"))
	if err != nil { return err }
	instances, err := runtimeOptionalMapping(fields["instances"], "resource instances", nil)
	if err != nil { return err }
	for name, instance := range instances {
		if !validRuntimeResourceInstanceName(name) { return runtimeConfigurationError("invalid Resource instance name") }
		declaration, err := runtimeMapping(instance, "resource instance", runtimeKeySet("use", "config"))
		if err != nil { return err }
		if use := declaration["use"]; use != nil {
			symbol, err := runtimeString(use)
			if err != nil || !validRuntimeConstructorSymbol(symbol) { return runtimeConfigurationError("invalid Resource provider symbol") }
		}
		if config := declaration["config"]; config != nil {
			if _, err := runtimeMapping(config, "resource configuration", nil); err != nil { return err }
		}
	}
	bindings, err := runtimeOptionalMapping(fields["bind"], "resource bindings", runtimeKeySet("implementations", "instances"))
	if err != nil { return err }
	for namespace, node := range bindings {
		consumers, err := runtimeMapping(node, "resource binding namespace", nil)
		if err != nil { return err }
		for consumer, node := range consumers {
			if namespace == "implementations" {
				if !validRuntimeConstructorSymbol(consumer) { return runtimeConfigurationError("invalid Resource consumer constructor") }
			} else if !validRuntimeResourceInstanceName(consumer) { return runtimeConfigurationError("invalid Resource consumer instance") }
			parameters, err := runtimeMapping(node, "resource binding parameters", nil)
			if err != nil { return err }
			for parameter, node := range parameters {
				if parameter == "_" || !token.IsIdentifier(parameter) { return runtimeConfigurationError("invalid Resource parameter identifier") }
				target, err := runtimeString(node)
				if err != nil || !validRuntimeResourceInstanceName(target) { return runtimeConfigurationError("invalid Resource binding target") }
			}
		}
	}
	return nil
}

func validRuntimeResourceInstanceName(value string) bool {
	if !validRuntimeExportName(value) { return false }
	for _, segment := range strings.Split(value, ".") {
		if segment[0] < 'a' || segment[0] > 'z' { return false }
	}
	return true
}

func composeRuntimeExportInterfaces(peers []map[string]*yaml.Node, lowerNode, upperNode *yaml.Node) (*yaml.Node, error) {
	allowed := runtimeKeySet("require", "use", "policies")
	lower, err := runtimeOptionalMapping(lowerNode, "interfaces", allowed)
	if err != nil { return nil, err }
	upper, err := runtimeOptionalMapping(upperNode, "interfaces", allowed)
	if err != nil { return nil, err }
	requirements := make(map[string]struct{})
	var inheritedUse, inheritedPolicies []*yaml.Node
	for _, peer := range peers {
		fields, err := runtimeOptionalMapping(peer["interfaces"], "adopted interfaces", allowed)
		if err != nil { return nil, err }
		values, err := runtimeInterfaceSequence(fields["require"], "adopted interfaces.require")
		if err != nil { return nil, err }
		for id := range values { requirements[id] = struct{}{} }
		inheritedUse = append(inheritedUse, fields["use"])
		inheritedPolicies = append(inheritedPolicies, fields["policies"])
	}
	for _, node := range []*yaml.Node{lower["require"], upper["require"]} {
		if err := applyRuntimeInterfaceSet(requirements, node, "interfaces.require"); err != nil { return nil, err }
	}
	ids := make([]string, 0, len(requirements))
	for id := range requirements { ids = append(ids, id) }
	sort.Strings(ids)
	uses, err := composeRuntimeExportEntries("interfaces.use", inheritedUse, lower["use"], upper["use"], mergeRuntimeImplementationChoices)
	if err != nil { return nil, err }
	policies, err := composeRuntimeExportEntries("interfaces.policies", inheritedPolicies, lower["policies"], upper["policies"], mergeRuntimeInterfacePolicies)
	if err != nil { return nil, err }
	return runtimeMappingNode(map[string]*yaml.Node{"require": runtimeStringSequence(ids), "use": uses, "policies": policies}), nil
}

func composeRuntimeExportEntries(path string, peers []*yaml.Node, lowerNode, upperNode *yaml.Node, normalize func(*yaml.Node, *yaml.Node) (*yaml.Node, bool, error)) (*yaml.Node, error) {
	local := make(map[string]*yaml.Node)
	for _, layer := range []*yaml.Node{lowerNode, upperNode} {
		if _, _, err := normalize(layer, nil); err != nil { return nil, err }
		entries, err := runtimeOptionalMapping(layer, path, nil)
		if err != nil { return nil, err }
		for key, value := range entries { local[key] = value }
	}
	inherited := make(map[string]*yaml.Node)
	for _, peer := range peers {
		normalized, _, err := normalize(peer, nil)
		if err != nil { return nil, err }
		entries, err := runtimeOptionalMapping(normalized, path, nil)
		if err != nil { return nil, err }
		keys := make([]string, 0, len(entries))
		for key := range entries { keys = append(keys, key) }
		sort.Strings(keys)
		for _, key := range keys {
			if local[key] != nil { continue }
			if previous := inherited[key]; previous != nil && !runtimeEqualNodes(previous, entries[key]) {
				return nil, runtimeConfigurationError("adopted exports conflict at %s[%q]; set or remove that exact key", path, key)
			}
			inherited[key] = entries[key]
		}
	}
	result, _, err := normalize(runtimeMappingNode(inherited), runtimeMappingNode(local))
	return result, err
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

func composeRuntimeExportConfigurations(peers []map[string]*yaml.Node, lowerNode, upperNode *yaml.Node, inventory map[string]runtimeConstructorInventoryEntry, owners map[string]bool) (*yaml.Node, error) {
	lower, err := runtimeOptionalMapping(lowerNode, "config", nil)
	if err != nil { return nil, err }
	upper, err := runtimeOptionalMapping(upperNode, "config", nil)
	if err != nil { return nil, err }
	inherited := make(map[string][]*yaml.Node)
	for _, peer := range peers {
		objects, err := runtimeOptionalMapping(peer["config"], "adopted config", nil)
		if err != nil { return nil, err }
		for symbol, node := range objects { inherited[symbol] = append(inherited[symbol], node) }
	}
	symbols := make(map[string]struct{})
	for symbol := range inherited { symbols[symbol] = struct{}{} }
	for symbol := range lower { symbols[symbol] = struct{}{} }
	for symbol := range upper { symbols[symbol] = struct{}{} }
	ordered := make([]string, 0, len(symbols))
	for symbol := range symbols { ordered = append(ordered, symbol) }
	sort.Strings(ordered)
	result := make(map[string]*yaml.Node)
	for _, symbol := range ordered {
		schema, typed, err := runtimeConstructorSchema(symbol)
		if err != nil { return nil, err }
		entry, visible := inventory[symbol]
		if !typed && entry.Schema != nil { schema = *entry.Schema }
		if typed || entry.Schema != nil {
			node, err := constructorconfig.ComposeAdopted(schema, inherited[symbol], lower[symbol], upper[symbol])
			if err != nil { return nil, fmt.Errorf("%w: constructor %s: %w", ErrRuntimeConfiguration, symbol, err) }
			if typed {
				if node != nil { result[symbol] = node }
			} else if node != nil {
				if !owners[symbol] { return nil, runtimeConfigurationError("config for %s has no effective constructor owner", symbol) }
				if _, err := constructorconfig.Normalize(schema, node); err != nil { return nil, fmt.Errorf("%w: constructor %s: %w", ErrRuntimeConfiguration, symbol, err) }
			}
			continue
		}
		if visible || validRuntimeConstructorSymbol(symbol) { return nil, runtimeConfigurationError("config for %s has no schema in the constructor inventory", symbol) }
		for _, node := range inherited[symbol] {
			if _, _, err := mergeRuntimeConfigurations(runtimeMappingNode(map[string]*yaml.Node{symbol: node}), nil); err != nil { return nil, err }
		}
		lo, hi := make(map[string]*yaml.Node), make(map[string]*yaml.Node)
		if lower[symbol] != nil { lo[symbol] = lower[symbol] }
		if upper[symbol] != nil { hi[symbol] = upper[symbol] }
		node, _, err := mergeRuntimeConfigurations(runtimeMappingNode(lo), runtimeMappingNode(hi))
		if err != nil { return nil, err }
		values, err := runtimeMapping(node, "config", nil)
		if err != nil { return nil, err }
		if values[symbol] != nil { result[symbol] = values[symbol] }
	}
	return runtimeMappingNode(result), nil
}

func runtimeEqualNodes(a, b *yaml.Node) bool {
	if a == nil || b == nil { return a == b }
	if a.Kind != b.Kind || a.Tag != b.Tag || a.Value != b.Value || len(a.Content) != len(b.Content) { return false }
	for i := range a.Content { if !runtimeEqualNodes(a.Content[i], b.Content[i]) { return false } }
	return true
}
`
