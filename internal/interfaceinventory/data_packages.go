package interfaceinventory

// DataSourceFile is one Go-selected, module-relative source identity from a
// package containing an active Data directive.
type DataSourceFile struct {
	path   string
	digest string
	bytes  int
}

func (f DataSourceFile) Path() string   { return f.path }
func (f DataSourceFile) Digest() string { return f.digest }
func (f DataSourceFile) Bytes() int     { return f.bytes }

// DataPackage is an eligible package from the same Go selection used for
// Interface, Resource, and Implementation discovery.
type DataPackage struct {
	modulePath    string
	moduleVersion string
	importPath    string
	files         []DataSourceFile
}

func (p DataPackage) ModulePath() string    { return p.modulePath }
func (p DataPackage) ModuleVersion() string { return p.moduleVersion }
func (p DataPackage) ImportPath() string    { return p.importPath }
func (p DataPackage) Files() []DataSourceFile {
	return append([]DataSourceFile(nil), p.files...)
}
