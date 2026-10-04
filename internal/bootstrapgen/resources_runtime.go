package bootstrapgen

const runtimeResourceSupport = `
type runtimeResourceDependency struct {
	Parameter string
	Resource string
}

type runtimeResourceProvider struct {
	Symbol string
	Resource string
	Schema *constructorconfig.Schema
	Dependencies []runtimeResourceDependency
}

type runtimeResourceIdentity struct {
	Name string ` + "`json:\"name\"`" + `
	Provider string ` + "`json:\"provider\"`" + `
}

type runtimeResourceBinding struct {
	Namespace string ` + "`json:\"namespace\"`" + `
	Consumer string ` + "`json:\"consumer\"`" + `
	Parameter string ` + "`json:\"parameter\"`" + `
	Target string ` + "`json:\"target\"`" + `
}

func validRuntimeResourceName(name string) bool {
	if len(name) == 0 || len(name) > 128 { return false }
	for _, segment := range strings.Split(name, ".") {
		if segment == "" || segment[0] < 'a' || segment[0] > 'z' { return false }
		for i := 1; i < len(segment); i++ {
			c := segment[i]
			if c == '-' {
				if i+1 == len(segment) || segment[i+1] == '-' { return false }
			} else if (c < 'a' || c > 'z') && (c < '0' || c > '9') { return false }
		}
	}
	return true
}

func runtimeResourceInventory(document runtimebaseline.Document) (map[string]runtimeResourceProvider, error) {
	var contract struct { Providers []runtimeResourceProvider ` + "`json:\"resource_inventory\"`" + ` }
	if json.Unmarshal(document.Contract, &contract) != nil { return nil, runtimebaseline.ErrBaseline }
	providers := make(map[string]runtimeResourceProvider, len(contract.Providers))
	for _, provider := range contract.Providers {
		if _, duplicate := providers[provider.Symbol]; duplicate || !validRuntimeConstructorSymbol(provider.Symbol) || !validRuntimeInterfaceID(provider.Resource) { return nil, runtimebaseline.ErrBaseline }
		if provider.Schema != nil {
			if err := constructorconfig.RestoreDefaults(provider.Schema, document.Defaults[provider.Symbol]); err != nil { return nil, runtimebaseline.ErrBaseline }
		}
		providers[provider.Symbol] = provider
	}
	return providers, nil
}

func runtimeResourceMapping(node *yaml.Node, allowed map[string]struct{}) (map[string]*yaml.Node, error) {
	values, err := runtimeOptionalMapping(node, "Resource declaration", allowed)
	if err != nil { return nil, runtimeConfigurationError("invalid Resource declaration mapping") }
	return values, nil
}

// Provider boundaries are applied before composing their typed fields. No
// requiredness or default materialization occurs until the final selected layer.
func composeRuntimeResources(layers []*yaml.Node, providers map[string]runtimeResourceProvider, constructors map[string]runtimeConstructorInventoryEntry) (*yaml.Node, error) {
	type instance struct { provider string; configuration *yaml.Node }
	instances := make(map[string]instance)
	bindings := make(map[[3]string]string)
	for _, layer := range layers {
		fields, err := runtimeResourceMapping(layer, runtimeKeySet("instances", "bind"))
		if err != nil { return nil, err }
		entries, err := runtimeResourceMapping(fields["instances"], nil)
		if err != nil { return nil, err }
		for _, name := range runtimeResourceKeys(entries) {
			node := entries[name]
			if !validRuntimeResourceName(name) { return nil, runtimeConfigurationError("invalid Resource instance name") }
			if runtimeRemovalMapping(node) { delete(instances, name); continue }
			declaration, err := runtimeResourceMapping(node, runtimeKeySet("use", "config"))
			if err != nil { return nil, err }
			current := instances[name]
			if declaration["use"] != nil {
				provider, err := runtimeString(declaration["use"])
				if err != nil || !validRuntimeConstructorSymbol(provider) { return nil, runtimeConfigurationError("invalid Resource provider identity") }
				if current.provider != provider { current = instance{provider: provider} }
			}
			if node := declaration["config"]; node != nil {
				if !runtimeRemovalMapping(node) {
					fields, err := runtimeResourceMapping(node, nil)
					if err != nil || fields["$remove"] != nil { return nil, runtimeConfigurationError("Resource config must be a mapping or removal") }
					nodes := 0
					if err := validateRuntimeRawNode(node, &nodes, 0); err != nil { return nil, err }
				}
				// An unbound lower declaration can be removed or replaced later.
				// Assigning its first provider replaces that entire untyped entry.
				if current.provider != "" {
					provider, exists := providers[current.provider]
					if !exists || provider.Schema == nil { return nil, runtimeConfigurationError("Resource provider has no Config schema") }
					current.configuration, err = constructorconfig.ComposeLayers(*provider.Schema, current.configuration, node)
					if err != nil { return nil, fmt.Errorf("%w: Resource %s: %w", ErrRuntimeConfiguration, name, err) }
				}
			}
			instances[name] = current
		}
		namespaces, err := runtimeResourceMapping(fields["bind"], runtimeKeySet("implementations", "instances"))
		if err != nil { return nil, err }
		for _, namespace := range []string{"implementations", "instances"} {
			consumers, err := runtimeResourceMapping(namespaces[namespace], nil)
			if err != nil { return nil, err }
			for _, consumer := range runtimeResourceKeys(consumers) {
				if namespace == "implementations" && !validRuntimeConstructorSymbol(consumer) || namespace == "instances" && !validRuntimeResourceName(consumer) { return nil, runtimeConfigurationError("invalid Resource binding consumer") }
				parameters, err := runtimeResourceMapping(consumers[consumer], nil)
				if err != nil { return nil, err }
				for _, parameter := range runtimeResourceKeys(parameters) {
					if parameter == "_" || !token.IsIdentifier(parameter) { return nil, runtimeConfigurationError("invalid Resource dependency parameter") }
					key := [3]string{namespace, consumer, parameter}
					if runtimeRemovalMapping(parameters[parameter]) { delete(bindings, key); continue }
					target, err := runtimeString(parameters[parameter])
					if err != nil || !validRuntimeResourceName(target) { return nil, runtimeConfigurationError("invalid Resource binding target") }
					bindings[key] = target
				}
			}
		}
	}
	selected := make(map[string]*yaml.Node)
	instanceNames := runtimeResourceKeys(instances)
	for _, name := range instanceNames {
		instance := instances[name]
		provider, exists := providers[instance.provider]
		if !exists { return nil, runtimeConfigurationError("Resource instance has no visible provider") }
		fields := map[string]*yaml.Node{"use": runtimeStringNode(instance.provider)}
		if provider.Schema != nil {
			configuration, err := constructorconfig.Normalize(*provider.Schema, instance.configuration)
			if err != nil { return nil, fmt.Errorf("%w: Resource %s: %w", ErrRuntimeConfiguration, name, err) }
			fields["config"] = configuration
		}
		selected[name] = runtimeMappingNode(fields)
	}
	dependencies := func(namespace, consumer string) ([]runtimeResourceDependency, bool) {
		if namespace == "implementations" {
			entry, exists := constructors[consumer]
			return entry.Dependencies, exists
		}
		entry, exists := instances[consumer]
		return providers[entry.provider].Dependencies, exists
	}
	// Even dormant explicit addresses must resolve. Only active consumers receive
	// implicit resolution and enter the normalized executable binding projection.
	for _, key := range runtimeResourceBindingKeys(bindings) {
		target := bindings[key]
		parameters, exists := dependencies(key[0], key[1])
		if !exists { return nil, runtimeConfigurationError("Resource binding consumer is not visible or selected") }
		resource := ""
		for _, parameter := range parameters { if parameter.Parameter == key[2] { resource = parameter.Resource } }
		instance, exists := instances[target]
		if !exists || resource == "" || providers[instance.provider].Resource != resource { return nil, runtimeConfigurationError("Resource binding target or parameter is incompatible") }
	}
	resolved := make(map[[3]string]string)
	resolve := func(namespace, consumer string, parameters []runtimeResourceDependency) error {
		for _, parameter := range parameters {
			key := [3]string{namespace, consumer, parameter.Parameter}
			target := bindings[key]
			if target == "" {
				for _, name := range instanceNames {
					if providers[instances[name].provider].Resource != parameter.Resource { continue }
					if target != "" { return runtimeConfigurationError("Resource dependency has multiple compatible selected instances") }
					target = name
				}
				if target == "" { return runtimeConfigurationError("Resource dependency has no compatible selected instance") }
			}
			resolved[key] = target
		}
		return nil
	}
	for _, name := range instanceNames {
		if err := resolve("instances", name, providers[instances[name].provider].Dependencies); err != nil { return nil, err }
	}
	for _, symbol := range runtimeResourceKeys(runtimeExecutableConstructors) {
		if err := resolve("implementations", symbol, constructors[symbol].Dependencies); err != nil { return nil, err }
	}
	namespaces := make(map[string]*yaml.Node)
	for _, namespace := range []string{"implementations", "instances"} {
		consumers := make(map[string]*yaml.Node)
		for _, key := range runtimeResourceBindingKeys(resolved) {
			if key[0] != namespace { continue }
			parameters, _ := runtimeResourceMapping(consumers[key[1]], nil)
			parameters[key[2]] = runtimeStringNode(resolved[key])
			consumers[key[1]] = runtimeMappingNode(parameters)
		}
		namespaces[namespace] = runtimeMappingNode(consumers)
	}
	return runtimeMappingNode(map[string]*yaml.Node{"instances": runtimeMappingNode(selected), "bind": runtimeMappingNode(namespaces)}), nil
}

func runtimeResourceKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values { keys = append(keys, key) }
	sort.Strings(keys)
	return keys
}

func runtimeResourceBindingKeys(values map[[3]string]string) [][3]string {
	keys := make([][3]string, 0, len(values))
	for key := range values { keys = append(keys, key) }
	sort.Slice(keys, func(i, j int) bool {
		for n := 0; n < 3; n++ { if keys[i][n] != keys[j][n] { return keys[i][n] < keys[j][n] } }
		return false
	})
	return keys
}

func runtimeApplicationModelResources(node *yaml.Node) ([]runtimeResourceIdentity, []runtimeResourceBinding, error) {
	identities := []runtimeResourceIdentity{}
	bindings := []runtimeResourceBinding{}
	fields, err := runtimeResourceMapping(node, runtimeKeySet("instances", "bind"))
	if err != nil { return nil, nil, err }
	instances, err := runtimeResourceMapping(fields["instances"], nil)
	if err != nil { return nil, nil, err }
	for _, name := range runtimeResourceKeys(instances) {
		if !validRuntimeResourceName(name) { return nil, nil, runtimeConfigurationError("invalid Resource instance name") }
		instance, err := runtimeResourceMapping(instances[name], runtimeKeySet("use", "config"))
		if err != nil { return nil, nil, err }
		provider, err := runtimeString(instance["use"])
		if err != nil || !validRuntimeConstructorSymbol(provider) { return nil, nil, runtimeConfigurationError("invalid Resource provider") }
		identities = append(identities, runtimeResourceIdentity{name, provider})
	}
	namespaces, err := runtimeResourceMapping(fields["bind"], runtimeKeySet("implementations", "instances"))
	if err != nil { return nil, nil, err }
	for _, namespace := range []string{"implementations", "instances"} {
		consumers, err := runtimeResourceMapping(namespaces[namespace], nil)
		if err != nil { return nil, nil, err }
		for _, consumer := range runtimeResourceKeys(consumers) {
			parameters, err := runtimeResourceMapping(consumers[consumer], nil)
			if err != nil { return nil, nil, err }
			for _, parameter := range runtimeResourceKeys(parameters) {
				target, err := runtimeString(parameters[parameter])
				if err != nil || !validRuntimeResourceName(target) { return nil, nil, runtimeConfigurationError("invalid Resource binding target") }
				bindings = append(bindings, runtimeResourceBinding{namespace, consumer, parameter, target})
			}
		}
	}
	return identities, bindings, nil
}
`
