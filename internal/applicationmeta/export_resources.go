package applicationmeta

import (
	"go/token"
	"regexp"

	"github.com/plystra/cli/internal/constructorsymbol"
	"go.yaml.in/yaml/v3"
)

var resourceInstanceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*$`)

// Validate authored shape only; requiredness, provider types, and binding
// targets need the composed model and must not activate an inert export.
func validateExportResourceSyntax(node *yaml.Node, path string) error {
	fields, err := exportResourceMapping(node, path)
	if err != nil {
		return err
	}
	for _, key := range sortedNodeKeys(fields) {
		switch key {
		case "instances":
			if err := validateExportResourceInstances(fields[key], path+".instances"); err != nil {
				return err
			}
		case "bind":
			if err := validateExportResourceBindings(fields[key], path+".bind"); err != nil {
				return err
			}
		default:
			return invalid("%s may contain only instances and bind", path)
		}
	}
	return nil
}

func validateExportResourceInstances(node *yaml.Node, path string) error {
	instances, err := exportResourceMapping(node, path)
	if err != nil {
		return err
	}
	for _, name := range sortedNodeKeys(instances) {
		if !validResourceInstanceName(name) {
			return invalid("%s keys must be Resource instance names of at most 128 ASCII bytes in dot-separated lower-kebab segments", path)
		}
		fields, err := exportResourceMapping(instances[name], path+" entry")
		if err != nil {
			return err
		}
		for _, key := range sortedNodeKeys(fields) {
			switch key {
			case "use":
				value, stringErr := strictString(fields[key])
				if _, symbolErr := constructorsymbol.Parse(value); stringErr != nil || symbolErr != nil {
					return invalid("%s entry use must be a fully qualified constructor symbol", path)
				}
			case "config":
				if _, err := exportResourceMapping(fields[key], path+" entry config"); err != nil {
					return err
				}
			default:
				return invalid("%s entries may contain only use and config", path)
			}
		}
	}
	return nil
}

func validateExportResourceBindings(node *yaml.Node, path string) error {
	namespaces, err := exportResourceMapping(node, path)
	if err != nil {
		return err
	}
	for _, namespace := range sortedNodeKeys(namespaces) {
		if namespace != "implementations" && namespace != "instances" {
			return invalid("%s may contain only implementations and instances", path)
		}
		namespacePath := path + "." + namespace
		consumers, err := exportResourceMapping(namespaces[namespace], namespacePath)
		if err != nil {
			return err
		}
		for _, consumer := range sortedNodeKeys(consumers) {
			if namespace == "implementations" {
				if _, err := constructorsymbol.Parse(consumer); err != nil {
					return invalid("%s keys must be fully qualified constructor symbols", namespacePath)
				}
			} else if !validResourceInstanceName(consumer) {
				return invalid("%s keys must be Resource instance names of at most 128 ASCII bytes in dot-separated lower-kebab segments", namespacePath)
			}
			parameters, err := exportResourceMapping(consumers[consumer], namespacePath+" entry")
			if err != nil {
				return err
			}
			for _, parameter := range sortedNodeKeys(parameters) {
				if parameter == "_" || !token.IsIdentifier(parameter) {
					return invalid("%s entry keys must be nonblank Go parameter identifiers", namespacePath)
				}
				target, err := strictString(parameters[parameter])
				if err != nil || !validResourceInstanceName(target) {
					return invalid("%s binding values must be Resource instance names of at most 128 ASCII bytes in dot-separated lower-kebab segments", namespacePath)
				}
			}
		}
	}
	return nil
}

func validResourceInstanceName(value string) bool {
	return len(value) <= 128 && resourceInstanceNamePattern.MatchString(value)
}

func exportResourceMapping(node *yaml.Node, path string) (map[string]*yaml.Node, error) {
	values, err := mapping(node, path)
	if err != nil {
		// Do not forward mapping errors containing arbitrary authored keys.
		return nil, invalid("%s must be a mapping with unique string keys", path)
	}
	return values, nil
}
