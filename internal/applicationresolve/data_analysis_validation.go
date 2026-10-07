package applicationresolve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/datacompiler"
)

const (
	dataMaximumNodes       = 65536
	dataMaximumImports     = 1024
	dataMaximumSymbolBytes = 128
	dataMaximumNesting     = 64
)

// DataAnalysisAcceptance is the immutable identity of a Data result accepted
// by Core before the application model can be frozen. The decoded logical
// models remain private until the emit and graph-projection boundaries exist.
type DataAnalysisAcceptance struct {
	resultDigest   string
	snapshotDigest string
	modelDigests   map[string]string
	members        map[string]dataAnalysisMember
}

// Valid reports whether Core retained a complete accepted analysis identity.
func (a DataAnalysisAcceptance) Valid() bool {
	return a.resultDigest != "" && a.snapshotDigest != "" && len(a.modelDigests) != 0 && len(a.members) == len(a.modelDigests)
}

type dataAnalysisMember struct {
	model         dataModel
	modelDigest   string
	accessPackage string
	accessType    string
	accessID      string
	migrationOnly bool
}

// DataAssignment is one immutable Core-owned activation of an accepted Data
// member. The generated access provider and ordinary graph edge are later
// phases; this value only records their explicit Resource assignment.
type DataAssignment struct {
	memberID      string
	resource      string
	namespace     string
	access        string
	accessPackage string
	accessType    string
	accessID      string
	modelDigest   string
}

// MemberID returns the exact accepted Data member identity.
func (a DataAssignment) MemberID() string { return a.memberID }

// Resource returns the exact configured database Resource instance.
func (a DataAssignment) Resource() string { return a.resource }

// Namespace returns the member-local PostgreSQL schema namespace.
func (a DataAssignment) Namespace() string { return a.namespace }

// Access returns the planned generated access instance, or an empty string
// for a migration-only member without an authored access contract.
func (a DataAssignment) Access() string { return a.access }

// AccessPackage returns the authored access Resource package, when present.
func (a DataAssignment) AccessPackage() string { return a.accessPackage }

// AccessType returns the authored access Resource type, when present.
func (a DataAssignment) AccessType() string { return a.accessType }

// AccessID returns the authored access Resource ID, when present.
func (a DataAssignment) AccessID() string { return a.accessID }

// ModelDigest returns the accepted logical-model digest for the member.
func (a DataAssignment) ModelDigest() string { return a.modelDigest }

// DataActivation is the complete deterministic activation set for one
// accepted Data analysis. Its assignments are sorted by member ID.
type DataActivation struct {
	assignments []DataAssignment
}

// Valid reports whether the activation contains a non-empty canonical set.
func (a DataActivation) Valid() bool {
	if len(a.assignments) == 0 {
		return false
	}
	for index, assignment := range a.assignments {
		if assignment.memberID == "" || assignment.resource == "" || assignment.namespace == "" || assignment.modelDigest == "" {
			return false
		}
		if index > 0 && a.assignments[index-1].memberID >= assignment.memberID {
			return false
		}
	}
	return true
}

// Assignments returns a defensive copy of the canonical activation set.
func (a DataActivation) Assignments() []DataAssignment {
	return append([]DataAssignment(nil), a.assignments...)
}

// ResultDigest returns the compiler result identity accepted by Core.
func (a DataAnalysisAcceptance) ResultDigest() string { return a.resultDigest }

// SnapshotDigest returns the finite source snapshot identity used by the
// accepted result.
func (a DataAnalysisAcceptance) SnapshotDigest() string { return a.snapshotDigest }

// ModelDigest returns the deterministic logical-model identity for one member.
func (a DataAnalysisAcceptance) ModelDigest(memberID string) string {
	return a.modelDigests[memberID]
}

type dataAnalyzeOutput struct {
	Roots     json.RawMessage `json:"roots"`
	Digest    string          `json:"digest"`
	Truncated bool            `json:"truncated"`
	Valid     bool            `json:"valid"`
}

type dataAnalyzeRoot struct {
	MemberID       string         `json:"member_id"`
	PackagePath    string         `json:"package_path"`
	FilePath       string         `json:"file_path"`
	Symbol         string         `json:"symbol"`
	Model          dataModel      `json:"model"`
	AccessPackage  string         `json:"access_package,omitempty"`
	AccessType     string         `json:"access_type,omitempty"`
	AccessID       string         `json:"access_id,omitempty"`
	MigrationOnly  bool           `json:"migration_only"`
	SourceDigest   string         `json:"source_digest"`
	DeclarationPos dataSourceSpan `json:"declaration_pos"`
}

type dataSourceSpan struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

type dataModel struct {
	Namespace  string          `json:"namespace"`
	Imports    []string        `json:"imports,omitempty"`
	Tables     []dataTable     `json:"tables,omitempty"`
	Queries    []dataQuery     `json:"queries,omitempty"`
	Plans      []dataPlan      `json:"plans,omitempty"`
	Migrations []dataMigration `json:"migrations,omitempty"`
}

type dataValue struct {
	Kind      string  `json:"kind"`
	Set       bool    `json:"set"`
	Parameter string  `json:"parameter,omitempty"`
	String    string  `json:"string,omitempty"`
	Int64     int64   `json:"int64,omitempty"`
	Uint64    uint64  `json:"uint64,omitempty"`
	Float64   float64 `json:"float64,omitempty"`
	Bool      bool    `json:"bool,omitempty"`
	Bytes     string  `json:"bytes,omitempty"`
}

type dataRef struct {
	Member string `json:"member,omitempty"`
	Symbol string `json:"symbol"`
}

type dataTable struct {
	ID          string           `json:"id"`
	Columns     []dataColumn     `json:"columns,omitempty"`
	Constraints []dataConstraint `json:"constraints,omitempty"`
	Indexes     []dataIndex      `json:"indexes,omitempty"`
}

type dataColumn struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Nullable bool      `json:"nullable,omitempty"`
	Default  dataValue `json:"default,omitempty"`
}

type dataConstraint struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Columns         []string `json:"columns"`
	ReferencedTable dataRef  `json:"referenced_table,omitempty"`
	ReferencedCols  []string `json:"referenced_columns,omitempty"`
}

type dataIndex struct {
	ID      string   `json:"id"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
}

type dataParameter struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type dataAssignment struct {
	Column string    `json:"column"`
	Value  dataValue `json:"value"`
}

type dataConflict struct {
	Constraint    string   `json:"constraint"`
	Action        string   `json:"action"`
	UpdateColumns []string `json:"update_columns,omitempty"`
}

type dataColumnRef struct {
	Table  dataRef `json:"table"`
	Column string  `json:"column"`
}

type dataJoinCondition struct {
	Left  dataColumnRef `json:"left"`
	Right dataColumnRef `json:"right"`
}

type dataJoin struct {
	Kind  string            `json:"kind"`
	Table dataRef           `json:"table"`
	On    dataJoinCondition `json:"on"`
}

type dataProjection struct {
	Table     dataRef `json:"table,omitempty"`
	Column    string  `json:"column,omitempty"`
	Alias     string  `json:"alias,omitempty"`
	Aggregate string  `json:"aggregate,omitempty"`
}

type dataResultField struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable,omitempty"`
}

type dataPredicate struct {
	Kind  string          `json:"kind"`
	Left  dataColumnRef   `json:"left,omitempty"`
	Right dataColumnRef   `json:"right,omitempty"`
	Value dataValue       `json:"value,omitempty"`
	Items []dataPredicate `json:"items,omitempty"`
}

type dataOrdering struct {
	Table     dataRef `json:"table,omitempty"`
	Column    string  `json:"column"`
	Direction string  `json:"direction"`
}

type dataQuery struct {
	ID          string            `json:"id"`
	Operation   string            `json:"operation"`
	Table       dataRef           `json:"table"`
	Joins       []dataJoin        `json:"joins,omitempty"`
	Parameters  []dataParameter   `json:"parameters,omitempty"`
	Assignments []dataAssignment  `json:"assignments,omitempty"`
	Conflict    dataConflict      `json:"conflict,omitempty"`
	Projection  []dataProjection  `json:"projection,omitempty"`
	Returning   []dataProjection  `json:"returning,omitempty"`
	Results     []dataResultField `json:"results,omitempty"`
	Predicate   dataPredicate     `json:"predicate,omitempty"`
	Ordering    []dataOrdering    `json:"ordering,omitempty"`
	Limit       uint64            `json:"limit,omitempty"`
	HasLimit    bool              `json:"has_limit,omitempty"`
	Offset      uint64            `json:"offset,omitempty"`
	HasOffset   bool              `json:"has_offset,omitempty"`
}

type dataPlan struct {
	ID         string              `json:"id"`
	Statements []dataPlanStatement `json:"statements"`
}

type dataPlanStatement struct {
	ID          string           `json:"id"`
	Query       dataRef          `json:"query"`
	Cardinality string           `json:"cardinality"`
	Affected    dataAffectedRows `json:"affected,omitempty"`
}

type dataAffectedRows struct {
	Set       bool   `json:"set"`
	Min       uint64 `json:"min"`
	Max       uint64 `json:"max,omitempty"`
	Unbounded bool   `json:"unbounded,omitempty"`
}

type dataMigration struct {
	ID           string                   `json:"id"`
	Version      uint64                   `json:"version"`
	Predecessors []dataRef                `json:"predecessors,omitempty"`
	Operations   []dataMigrationOperation `json:"operations"`
}

type dataMigrationOperation struct {
	Kind   string     `json:"kind"`
	Table  dataRef    `json:"table"`
	Column dataColumn `json:"column,omitempty"`
	Index  dataIndex  `json:"index,omitempty"`
}

// ValidateDataAnalysis independently accepts the complete typed analyze
// result against the same finite source snapshot sent to the compiler.
func ValidateDataAnalysis(response datacompiler.AnalyzeResponse, members []string, snapshot datacompiler.AnalyzeSourceSnapshot) (DataAnalysisAcceptance, error) {
	return validateDataAnalysisWithBounds(response, members, snapshot, defaultDataAnalyzeBounds(), true)
}

// ValidateDataRoots retains the narrow legacy helper for callers that only
// have a wire response. Resolve uses ValidateDataAnalysis with the snapshot.
func ValidateDataRoots(response datacompiler.AnalyzeResponse, members []string) error {
	_, err := validateDataAnalysisWithBounds(response, members, datacompiler.AnalyzeSourceSnapshot{}, defaultDataAnalyzeBounds(), false)
	return err
}

func validateDataAnalysisWithBounds(response datacompiler.AnalyzeResponse, members []string, snapshot datacompiler.AnalyzeSourceSnapshot, bounds datacompiler.Bounds, checkSource bool) (DataAnalysisAcceptance, error) {
	if response.Status != "succeeded" || response.Truncated {
		return DataAnalysisAcceptance{}, fmt.Errorf("Data analyze result is not a complete success")
	}
	if response.OutputDigest == "" || !validDataDigest(response.OutputDigest) || len(response.Diagnostics) != 0 {
		return DataAnalysisAcceptance{}, fmt.Errorf("Data analyze result has incomplete terminal facts")
	}
	var output dataAnalyzeOutput
	if err := decodeDataJSON(response.Output, &output); err != nil {
		return DataAnalysisAcceptance{}, fmt.Errorf("decode accepted Data output: %w", err)
	}
	if !output.Valid || output.Truncated || output.Digest != response.OutputDigest || len(output.Roots) == 0 {
		return DataAnalysisAcceptance{}, fmt.Errorf("Data analyze output is not valid, complete, and digest-bound")
	}
	var rawRoots []json.RawMessage
	if err := decodeDataJSON(output.Roots, &rawRoots); err != nil || len(rawRoots) == 0 {
		return DataAnalysisAcceptance{}, fmt.Errorf("Data analyze roots are malformed")
	}

	active := make(map[string]struct{}, len(members))
	for _, member := range members {
		if !validDataMemberID(member) {
			return DataAnalysisAcceptance{}, fmt.Errorf("active Data member %q is invalid", member)
		}
		if _, exists := active[member]; exists {
			return DataAnalysisAcceptance{}, fmt.Errorf("active Data member %q is duplicated", member)
		}
		active[member] = struct{}{}
	}
	if len(rawRoots) != len(active) {
		return DataAnalysisAcceptance{}, fmt.Errorf("analyze returned %d Data roots for %d active members", len(rawRoots), len(active))
	}

	eligible, resources := dataSnapshotIndexes(snapshot)
	modelDigests := make(map[string]string, len(rawRoots))
	analyzedMembers := make(map[string]dataAnalysisMember, len(rawRoots))
	previousID := ""
	for _, rawRoot := range rawRoots {
		if err := requireDataFields(rawRoot, "member_id", "package_path", "file_path", "symbol", "model", "migration_only", "source_digest", "declaration_pos"); err != nil {
			return DataAnalysisAcceptance{}, fmt.Errorf("malformed Data root: %w", err)
		}
		var root dataAnalyzeRoot
		if err := decodeDataJSON(rawRoot, &root); err != nil {
			return DataAnalysisAcceptance{}, fmt.Errorf("decode Data root: %w", err)
		}
		if previousID != "" && root.MemberID < previousID {
			return DataAnalysisAcceptance{}, fmt.Errorf("Data roots are not in canonical member order")
		}
		previousID = root.MemberID
		if _, exists := active[root.MemberID]; !exists {
			return DataAnalysisAcceptance{}, fmt.Errorf("analyze returned unassigned Data member %q", root.MemberID)
		}
		if _, exists := modelDigests[root.MemberID]; exists {
			return DataAnalysisAcceptance{}, fmt.Errorf("analyze returned duplicate Data member %q", root.MemberID)
		}
		if root.PackagePath == "" || root.FilePath == "" || !ast.IsExported(root.Symbol) || !validDataDigest(root.SourceDigest) {
			return DataAnalysisAcceptance{}, fmt.Errorf("Data root %q has invalid identity or provenance", root.MemberID)
		}
		if err := validateDataModel(root.Model, bounds); err != nil {
			return DataAnalysisAcceptance{}, fmt.Errorf("Data root %q model is not a normalized typed model: %w", root.MemberID, err)
		}
		modelDigest, err := digestDataModel(root.Model)
		if err != nil {
			return DataAnalysisAcceptance{}, fmt.Errorf("Data root %q model digest: %w", root.MemberID, err)
		}
		modelDigests[root.MemberID] = modelDigest
		analyzedMembers[root.MemberID] = dataAnalysisMember{
			model: root.Model, modelDigest: modelDigest,
			accessPackage: root.AccessPackage, accessType: root.AccessType,
			accessID: root.AccessID, migrationOnly: root.MigrationOnly,
		}
		if err := validateDataAccess(root, resources); err != nil {
			return DataAnalysisAcceptance{}, fmt.Errorf("Data root %q access contract: %w", root.MemberID, err)
		}
		if checkSource {
			if err := validateDataProvenance(root, eligible); err != nil {
				return DataAnalysisAcceptance{}, fmt.Errorf("Data root %q provenance: %w", root.MemberID, err)
			}
		}
	}
	for member := range active {
		if _, exists := modelDigests[member]; !exists {
			return DataAnalysisAcceptance{}, fmt.Errorf("analyze did not return active Data member %q", member)
		}
	}
	return DataAnalysisAcceptance{
		resultDigest:   response.OutputDigest,
		snapshotDigest: snapshot.Digest(),
		modelDigests:   modelDigests,
		members:        analyzedMembers,
	}, nil
}

// BuildDataActivation checks and materializes the Core-owned mapping from
// accepted Data roots to ordinary configured Resource instances. It does not
// create graph nodes or install generated access artifacts.
func BuildDataActivation(manifest applicationmeta.Manifest, acceptance DataAnalysisAcceptance) (DataActivation, error) {
	if !acceptance.Valid() {
		return DataActivation{}, fmt.Errorf("accepted Data analysis is incomplete")
	}
	resources := make(map[string]struct{}, len(manifest.ResourceInstances()))
	for _, instance := range manifest.ResourceInstances() {
		resources[instance.Name()] = struct{}{}
	}
	members := manifest.DataMembers()
	configured := make(map[string]applicationmeta.DataMember, len(members))
	accesses := make(map[string]string)
	namespaces := make(map[string]string)
	for _, member := range members {
		if member.ID() == "" || member.Resource() == "" {
			return DataActivation{}, fmt.Errorf("Data member activation has an incomplete identity")
		}
		if _, exists := configured[member.ID()]; exists {
			return DataActivation{}, fmt.Errorf("Data member %q is configured more than once", member.ID())
		}
		configured[member.ID()] = member
	}
	if len(configured) != len(acceptance.members) {
		for memberID := range acceptance.members {
			if _, exists := configured[memberID]; !exists {
				return DataActivation{}, fmt.Errorf("accepted Data member %q has no explicit activation", memberID)
			}
		}
	}
	assignments := make([]DataAssignment, 0, len(members))
	for _, member := range members {
		if _, exists := resources[member.Resource()]; !exists {
			return DataActivation{}, fmt.Errorf("Data member %q references missing Resource instance %q", member.ID(), member.Resource())
		}
		analyzed, exists := acceptance.members[member.ID()]
		if !exists {
			return DataActivation{}, fmt.Errorf("Data member %q has no accepted analyze result", member.ID())
		}
		if analyzed.modelDigest == "" || !validDataDigest(analyzed.modelDigest) {
			return DataActivation{}, fmt.Errorf("Data member %q has an invalid accepted model digest", member.ID())
		}
		if analyzed.model.Namespace == "" {
			return DataActivation{}, fmt.Errorf("Data member %q has no accepted namespace", member.ID())
		}
		namespaceKey := member.Resource() + "\x00" + analyzed.model.Namespace
		if previous, exists := namespaces[namespaceKey]; exists {
			return DataActivation{}, fmt.Errorf("Data member %q namespace %q collides with %q on Resource %q", member.ID(), analyzed.model.Namespace, previous, member.Resource())
		}
		namespaces[namespaceKey] = member.ID()
		hasAccessContract := analyzed.accessPackage != "" || analyzed.accessType != "" || analyzed.accessID != ""
		if hasAccessContract && (analyzed.accessPackage == "" || analyzed.accessType == "" || analyzed.accessID == "") {
			return DataActivation{}, fmt.Errorf("Data member %q has an incomplete accepted Access contract", member.ID())
		}
		if member.Access() == "" {
			if hasAccessContract || !analyzed.migrationOnly {
				return DataActivation{}, fmt.Errorf("Data member %q requires an explicit access instance", member.ID())
			}
		} else {
			if !hasAccessContract || analyzed.migrationOnly {
				return DataActivation{}, fmt.Errorf("Data member %q configures access without an accepted Access contract", member.ID())
			}
			if _, exists := resources[member.Access()]; exists {
				return DataActivation{}, fmt.Errorf("Data access instance %q collides with a Resource instance", member.Access())
			}
			if strings.HasPrefix(member.Access(), "kernel.") {
				return DataActivation{}, fmt.Errorf("Data access instance %q uses a reserved intrinsic name", member.Access())
			}
			if previous, exists := accesses[member.Access()]; exists {
				return DataActivation{}, fmt.Errorf("Data access instance %q is assigned to both %q and %q", member.Access(), previous, member.ID())
			}
			accesses[member.Access()] = member.ID()
		}
		for _, imported := range analyzed.model.Imports {
			other, exists := configured[imported]
			if !exists {
				return DataActivation{}, fmt.Errorf("Data member %q imports unassigned member %q", member.ID(), imported)
			}
			if other.Resource() != member.Resource() {
				return DataActivation{}, fmt.Errorf("Data member %q crosses Resource boundary to %q", member.ID(), imported)
			}
		}
		assignments = append(assignments, DataAssignment{
			memberID: member.ID(), resource: member.Resource(), namespace: analyzed.model.Namespace, access: member.Access(),
			accessPackage: analyzed.accessPackage, accessType: analyzed.accessType, accessID: analyzed.accessID,
			modelDigest: analyzed.modelDigest,
		})
	}
	for memberID := range acceptance.members {
		if _, exists := configured[memberID]; !exists {
			return DataActivation{}, fmt.Errorf("accepted Data member %q has no explicit activation", memberID)
		}
	}
	sort.Slice(assignments, func(left, right int) bool { return assignments[left].memberID < assignments[right].memberID })
	activation := DataActivation{assignments: assignments}
	if !activation.Valid() {
		return DataActivation{}, fmt.Errorf("Data activation is not canonical")
	}
	return activation, nil
}

// ValidateDataAssignments checks the Core-owned mapping from accepted Data
// roots to ordinary configured Resource instances without retaining it.
func ValidateDataAssignments(manifest applicationmeta.Manifest, acceptance DataAnalysisAcceptance) error {
	_, err := BuildDataActivation(manifest, acceptance)
	return err
}

func defaultDataAnalyzeBounds() datacompiler.Bounds {
	return datacompiler.Bounds{
		MaxRoots: datacompiler.MaxRoots, MaxNodes: datacompiler.MaxNodes,
		MaxImports: datacompiler.MaxImports, MaxNesting: datacompiler.MaxNesting,
		MaxSymbolBytes: datacompiler.MaxSymbolBytes,
		MaxDiagnostics: datacompiler.MaxDiagnostics, MaxDiagnosticBytes: datacompiler.MaxDiagnosticBytes,
	}
}

func dataSnapshotIndexes(snapshot datacompiler.AnalyzeSourceSnapshot) (map[string]datacompiler.AnalyzeSourcePackage, map[string]datacompiler.AnalyzeResourceContract) {
	eligible := make(map[string]datacompiler.AnalyzeSourcePackage)
	for _, pkg := range snapshot.Packages() {
		if pkg.RootEligibility() == datacompiler.RootEligibilityEligible {
			eligible[pkg.ImportPath()] = pkg
		}
	}
	resources := make(map[string]datacompiler.AnalyzeResourceContract)
	for _, resource := range snapshot.ResourceContracts() {
		resources[resource.ImportPath+"\x00"+resource.TypeName+"\x00"+resource.ID] = resource
	}
	return eligible, resources
}

func validateDataAccess(root dataAnalyzeRoot, resources map[string]datacompiler.AnalyzeResourceContract) error {
	allEmpty := root.AccessPackage == "" && root.AccessType == "" && root.AccessID == ""
	if allEmpty {
		if !root.MigrationOnly {
			return fmt.Errorf("missing access identity must be migration-only")
		}
		return nil
	}
	if root.MigrationOnly || root.AccessPackage == "" || root.AccessType == "" || root.AccessID == "" {
		return fmt.Errorf("access identity is incomplete")
	}
	if _, exists := resources[root.AccessPackage+"\x00"+root.AccessType+"\x00"+root.AccessID]; !exists {
		return fmt.Errorf("access Resource contract is not present in the source snapshot")
	}
	return nil
}

func validateDataProvenance(root dataAnalyzeRoot, packages map[string]datacompiler.AnalyzeSourcePackage) error {
	pkg, ok := packages[root.PackagePath]
	if !ok {
		return fmt.Errorf("root package is not an eligible snapshot package")
	}
	var source datacompiler.AnalyzeSourceFile
	found := false
	for _, file := range pkg.Files() {
		if file.Path() == root.FilePath {
			source, found = file, true
			break
		}
	}
	if !found {
		return fmt.Errorf("root file is not in the selected package snapshot")
	}
	sum := sha256.Sum256(source.Content())
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if root.SourceDigest != source.Digest() || root.SourceDigest != digest || root.DeclarationPos.Path != root.FilePath {
		return fmt.Errorf("source digest or path differs from the selected source")
	}
	if err := validateDataDeclarationPosition(root, source.Content()); err != nil {
		return err
	}
	return nil
}

func validateDataDeclarationPosition(root dataAnalyzeRoot, content []byte) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, root.FilePath, content, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("root source does not parse: %w", err)
	}
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, specNode := range gen.Specs {
			spec, ok := specNode.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 || spec.Names[0].Name != root.Symbol {
				continue
			}
			if !ast.IsExported(root.Symbol) || !dataDirectiveAttached(file, fset, spec, root.MemberID) {
				continue
			}
			start := fset.Position(spec.Values[0].Pos())
			end := fset.Position(spec.Values[0].End())
			if root.DeclarationPos.StartLine != start.Line || root.DeclarationPos.StartColumn != start.Column || root.DeclarationPos.EndLine != end.Line || root.DeclarationPos.EndColumn != end.Column {
				return fmt.Errorf("declaration span differs from the selected source")
			}
			return nil
		}
	}
	return fmt.Errorf("declared Data root is absent from the selected source")
}

func dataDirectiveAttached(file *ast.File, fset *token.FileSet, spec *ast.ValueSpec, memberID string) bool {
	want := "//plystra:data " + memberID
	line := fset.Position(spec.Pos()).Line
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.TrimSpace(comment.Text) == want && fset.Position(comment.End()).Line+1 == line && comment.End() <= spec.Pos() {
				return true
			}
		}
	}
	return false
}

func validateDataModel(model dataModel, bounds datacompiler.Bounds) error {
	if !validDataNamespace(model.Namespace) {
		return fmt.Errorf("invalid namespace")
	}
	if len(model.Imports) > bounds.MaxImports || len(model.Imports) > dataMaximumImports || !sortedUniqueStrings(model.Imports) {
		return fmt.Errorf("imports are not bounded and canonical")
	}
	imports := make(map[string]struct{}, len(model.Imports))
	for _, member := range model.Imports {
		if !validDataMemberID(member) || member == model.Namespace {
			return fmt.Errorf("invalid imported member %q", member)
		}
		imports[member] = struct{}{}
	}
	if !sortedDataTables(model.Tables) || !sortedDataQueries(model.Queries) || !sortedDataPlans(model.Plans) || !sortedDataMigrations(model.Migrations) {
		return fmt.Errorf("model declarations are not in canonical order")
	}
	tables := make(map[string]dataTable, len(model.Tables))
	queries := make(map[string]struct{}, len(model.Queries))
	symbols := make(map[string]string)
	for _, table := range model.Tables {
		if err := addDataSymbol(symbols, table.ID, "table"); err != nil {
			return err
		}
		if err := validateDataTable(table, imports); err != nil {
			return err
		}
		tables[table.ID] = table
	}
	for _, query := range model.Queries {
		if err := addDataSymbol(symbols, query.ID, "query"); err != nil {
			return err
		}
		queries[query.ID] = struct{}{}
		if err := validateDataQuery(query, imports, tables, bounds.MaxNesting); err != nil {
			return err
		}
	}
	for _, plan := range model.Plans {
		if err := addDataSymbol(symbols, plan.ID, "plan"); err != nil {
			return err
		}
		if err := validateDataPlan(plan, imports, queries, bounds.MaxNesting); err != nil {
			return err
		}
	}
	for _, migration := range model.Migrations {
		if err := addDataSymbol(symbols, migration.ID, "migration"); err != nil {
			return err
		}
		if err := validateDataMigration(migration, imports, tables, symbols); err != nil {
			return err
		}
	}
	if dataModelNodeCount(model) > bounds.MaxNodes || dataModelNodeCount(model) > dataMaximumNodes || dataModelHasLongSymbol(model, bounds.MaxSymbolBytes) {
		return fmt.Errorf("model node limit exceeded")
	}
	return nil
}

func validateDataTable(table dataTable, imports map[string]struct{}) error {
	if !validDataSymbol(table.ID) || !sortedDataColumns(table.Columns) || !sortedDataConstraints(table.Constraints) || !sortedDataIndexes(table.Indexes) {
		return fmt.Errorf("table %q is not canonical", table.ID)
	}
	columns := make(map[string]struct{}, len(table.Columns))
	for _, column := range table.Columns {
		if !validDataSymbol(column.ID) || !validDataScalar(column.Type) {
			return fmt.Errorf("table %q has invalid column", table.ID)
		}
		if _, exists := columns[column.ID]; exists {
			return fmt.Errorf("table %q has duplicate column %q", table.ID, column.ID)
		}
		columns[column.ID] = struct{}{}
		if err := validateDataValue(column.Default, false, nil); err != nil {
			return err
		}
	}
	for _, constraint := range table.Constraints {
		if !validDataSymbol(constraint.ID) || (constraint.Kind != "primary-key" && constraint.Kind != "unique" && constraint.Kind != "foreign-key") || len(constraint.Columns) == 0 || !uniqueStrings(constraint.Columns) {
			return fmt.Errorf("table %q has invalid constraint", table.ID)
		}
		for _, column := range constraint.Columns {
			if _, exists := columns[column]; !exists {
				return fmt.Errorf("constraint %q references missing column", constraint.ID)
			}
		}
		if constraint.Kind == "foreign-key" {
			if err := validateDataRef(constraint.ReferencedTable, nil); err != nil || len(constraint.ReferencedCols) == 0 || !uniqueStrings(constraint.ReferencedCols) {
				return fmt.Errorf("constraint %q has invalid foreign reference", constraint.ID)
			}
		}
	}
	for _, index := range table.Indexes {
		if !validDataSymbol(index.ID) || len(index.Columns) == 0 || !uniqueStrings(index.Columns) {
			return fmt.Errorf("table %q has invalid index", table.ID)
		}
		for _, column := range index.Columns {
			if _, exists := columns[column]; !exists {
				return fmt.Errorf("index %q references missing column", index.ID)
			}
		}
	}
	return nil
}

func validateDataQuery(query dataQuery, imports map[string]struct{}, tables map[string]dataTable, maxNesting int) error {
	if !validDataSymbol(query.ID) || !validDataOperation(query.Operation) {
		return fmt.Errorf("query %q has invalid identity", query.ID)
	}
	if err := validateDataRef(query.Table, imports); err != nil {
		return err
	}
	if query.Table.Member == "" {
		if _, exists := tables[query.Table.Symbol]; !exists {
			return fmt.Errorf("query %q references missing table", query.ID)
		}
	}
	introduced := map[string]struct{}{dataRefKey(query.Table): {}}
	for _, join := range query.Joins {
		if join.Kind != "inner" && join.Kind != "left" {
			return fmt.Errorf("query %q has invalid join", query.ID)
		}
		if err := validateDataRef(join.Table, imports); err != nil {
			return err
		}
		if join.Table.Member == "" {
			if _, exists := tables[join.Table.Symbol]; !exists {
				return fmt.Errorf("query %q references missing joined table", query.ID)
			}
		}
		key := dataRefKey(join.Table)
		if _, exists := introduced[key]; exists {
			return fmt.Errorf("query %q repeats joined table", query.ID)
		}
		if err := validateDataColumnRef(join.On.Left, imports, introduced, query.Table); err != nil {
			return err
		}
		if join.On.Right.Table != join.Table || !validDataSymbol(join.On.Right.Column) {
			return fmt.Errorf("query %q has invalid join right side", query.ID)
		}
		introduced[key] = struct{}{}
	}
	if len(query.Joins) != 0 && query.Operation != "select" {
		return fmt.Errorf("query %q has joins outside select", query.ID)
	}
	if !sortedDataParameters(query.Parameters) || !sortedDataAssignments(query.Assignments) || !sortedUniqueStrings(query.Conflict.UpdateColumns) {
		return fmt.Errorf("query %q has non-canonical parameter, assignment, or conflict order", query.ID)
	}
	parameters := make(map[string]string, len(query.Parameters))
	for _, parameter := range query.Parameters {
		if !validDataSymbol(parameter.ID) || !validDataScalar(parameter.Type) {
			return fmt.Errorf("query %q has invalid parameter", query.ID)
		}
		if _, exists := parameters[parameter.ID]; exists {
			return fmt.Errorf("query %q repeats parameter", query.ID)
		}
		parameters[parameter.ID] = parameter.Type
	}
	assignments := make(map[string]struct{}, len(query.Assignments))
	for _, assignment := range query.Assignments {
		if !validDataSymbol(assignment.Column) {
			return fmt.Errorf("query %q has invalid assignment", query.ID)
		}
		if _, exists := assignments[assignment.Column]; exists {
			return fmt.Errorf("query %q repeats assignment", query.ID)
		}
		if err := validateDataValue(assignment.Value, true, parameters); err != nil {
			return err
		}
		assignments[assignment.Column] = struct{}{}
	}
	if query.Conflict.Constraint != "" || query.Conflict.Action != "" || len(query.Conflict.UpdateColumns) != 0 {
		if query.Operation != "insert" || !validDataSymbol(query.Conflict.Constraint) || (query.Conflict.Action != "do-nothing" && query.Conflict.Action != "do-update") {
			return fmt.Errorf("query %q has invalid conflict", query.ID)
		}
		if query.Conflict.Action == "do-nothing" && len(query.Conflict.UpdateColumns) != 0 || query.Conflict.Action == "do-update" && len(query.Conflict.UpdateColumns) == 0 {
			return fmt.Errorf("query %q has invalid conflict update columns", query.ID)
		}
		if query.Table.Member == "" {
			found := false
			for _, constraint := range tables[query.Table.Symbol].Constraints {
				if constraint.ID == query.Conflict.Constraint && (constraint.Kind == "primary-key" || constraint.Kind == "unique") {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("query %q references missing conflict constraint", query.ID)
			}
		}
		for _, column := range query.Conflict.UpdateColumns {
			if !validDataSymbol(column) {
				return fmt.Errorf("query %q has invalid conflict column", query.ID)
			}
			if _, exists := assignments[column]; !exists {
				return fmt.Errorf("query %q conflict column is not assigned", query.ID)
			}
		}
	}
	if query.Operation == "select" && (len(query.Assignments) != 0 || len(query.Returning) != 0) || query.Operation == "insert" && (len(query.Predicate.Items) != 0 || query.Predicate.Kind != "" || len(query.Ordering) != 0 || query.HasLimit || query.HasOffset || len(query.Projection) != 0) || query.Operation == "update" && (len(query.Assignments) == 0 || query.Predicate.Kind == "" || len(query.Ordering) != 0 || query.HasLimit || query.HasOffset || len(query.Projection) != 0) || query.Operation == "delete" && (len(query.Assignments) != 0 || query.Predicate.Kind == "" || len(query.Ordering) != 0 || query.HasLimit || query.HasOffset || len(query.Projection) != 0) {
		return fmt.Errorf("query %q has an invalid operation shape", query.ID)
	}
	outputs := query.Projection
	if query.Operation != "select" {
		outputs = query.Returning
	}
	if len(query.Returning) != 0 && len(query.Results) == 0 || len(query.Results) != 0 && (len(outputs) == 0 || len(query.Results) != len(outputs)) {
		return fmt.Errorf("query %q has incomplete result mapping", query.ID)
	}
	if err := validateDataProjections(outputs, imports, introduced); err != nil {
		return err
	}
	if err := validateDataResults(query.Results, outputs); err != nil {
		return err
	}
	aggregates := 0
	for _, projection := range outputs {
		if projection.Aggregate != "" {
			aggregates++
		}
	}
	if aggregates != 0 {
		if query.Operation != "select" || aggregates != 1 || len(outputs) != 1 || len(query.Results) != 1 || query.Results[0].Type != "int64" || query.Results[0].Nullable || query.Results[0].Source != projectionOutputName(outputs[0]) {
			return fmt.Errorf("query %q has invalid aggregate result mapping", query.ID)
		}
		if len(query.Ordering) != 0 || query.HasLimit || query.HasOffset {
			return fmt.Errorf("query %q has aggregate modifiers", query.ID)
		}
	}
	if err := validateDataPredicate(query.Predicate, imports, parameters, introduced, query.Table, 0, maxNesting); err != nil {
		return err
	}
	for _, ordering := range query.Ordering {
		if !validDataSymbol(ordering.Column) || (ordering.Direction != "asc" && ordering.Direction != "desc") {
			return fmt.Errorf("query %q has invalid ordering", query.ID)
		}
		if ordering.Table.Member != "" || ordering.Table.Symbol != "" {
			if err := validateDataRef(ordering.Table, imports); err != nil {
				return err
			}
			if _, exists := introduced[dataRefKey(ordering.Table)]; !exists {
				return fmt.Errorf("query %q orders by an absent table", query.ID)
			}
		}
	}
	return nil
}

func validateDataProjections(outputs []dataProjection, imports map[string]struct{}, introduced map[string]struct{}) error {
	names := make(map[string]struct{}, len(outputs))
	aggregates := 0
	for _, projection := range outputs {
		if projection.Aggregate != "" {
			aggregates++
			if projection.Aggregate != "count" || projection.Alias == "" || projection.Column == "" && (projection.Table.Member != "" || projection.Table.Symbol != "") || projection.Column != "" && !validDataSymbol(projection.Column) {
				return fmt.Errorf("invalid aggregate projection")
			}
		} else if !validDataSymbol(projection.Column) {
			return fmt.Errorf("invalid projection column")
		}
		if projection.Alias != "" && !validDataSymbol(projection.Alias) {
			return fmt.Errorf("invalid projection alias")
		}
		name := projection.Column
		if projection.Alias != "" {
			name = projection.Alias
		}
		if _, exists := names[name]; exists {
			return fmt.Errorf("duplicate projection output")
		}
		names[name] = struct{}{}
		if projection.Table.Member != "" || projection.Table.Symbol != "" {
			if err := validateDataRef(projection.Table, imports); err != nil {
				return err
			}
			if _, exists := introduced[dataRefKey(projection.Table)]; !exists {
				return fmt.Errorf("projection references an absent table")
			}
		}
	}
	if aggregates != 0 && (aggregates != 1 || len(outputs) != 1) {
		return fmt.Errorf("aggregate projection must be the only output")
	}
	return nil
}

func validateDataResults(results []dataResultField, outputs []dataProjection) error {
	if len(results) == 0 {
		return nil
	}
	if len(results) != len(outputs) {
		return fmt.Errorf("result mapping does not cover query outputs")
	}
	ids, sources := map[string]struct{}{}, map[string]struct{}{}
	for _, result := range results {
		if !validDataSymbol(result.ID) || !validDataSymbol(result.Source) || !validDataScalar(result.Type) {
			return fmt.Errorf("invalid typed result mapping")
		}
		if _, exists := ids[result.ID]; exists {
			return fmt.Errorf("duplicate result ID")
		}
		if _, exists := sources[result.Source]; exists {
			return fmt.Errorf("duplicate result source")
		}
		ids[result.ID], sources[result.Source] = struct{}{}, struct{}{}
		found := false
		for _, output := range outputs {
			name := projectionOutputName(output)
			if result.Source == name {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("result source is absent from query outputs")
		}
	}
	return nil
}

func projectionOutputName(projection dataProjection) string {
	if projection.Alias != "" {
		return projection.Alias
	}
	return projection.Column
}

func validateDataPlan(plan dataPlan, imports map[string]struct{}, queries map[string]struct{}, maxNesting int) error {
	if !validDataSymbol(plan.ID) || len(plan.Statements) == 0 || len(plan.Statements) > maxNesting || len(plan.Statements) > dataMaximumNesting {
		return fmt.Errorf("plan %q is invalid", plan.ID)
	}
	seen := map[string]struct{}{}
	for _, statement := range plan.Statements {
		if !validDataSymbol(statement.ID) {
			return fmt.Errorf("plan %q has invalid statement", plan.ID)
		}
		if _, exists := seen[statement.ID]; exists {
			return fmt.Errorf("plan %q repeats a statement", plan.ID)
		}
		seen[statement.ID] = struct{}{}
		if err := validateDataRef(statement.Query, imports); err != nil {
			return err
		}
		if statement.Query.Member == "" {
			if _, exists := queries[statement.Query.Symbol]; !exists {
				return fmt.Errorf("plan %q references an absent query", plan.ID)
			}
		}
		if statement.Cardinality != "none" && statement.Cardinality != "exactly_one" && statement.Cardinality != "zero_or_one" && statement.Cardinality != "many" {
			return fmt.Errorf("plan %q has invalid cardinality", plan.ID)
		}
		if statement.Affected.Set && !statement.Affected.Unbounded && statement.Affected.Min > statement.Affected.Max {
			return fmt.Errorf("plan %q has invalid affected-row range", plan.ID)
		}
	}
	return nil
}

func validateDataMigration(migration dataMigration, imports map[string]struct{}, tables map[string]dataTable, symbols map[string]string) error {
	if !validDataSymbol(migration.ID) || migration.Version == 0 || len(migration.Operations) == 0 || !sortedDataRefs(migration.Predecessors) {
		return fmt.Errorf("migration %q is invalid", migration.ID)
	}
	for _, predecessor := range migration.Predecessors {
		if err := validateDataRef(predecessor, imports); err != nil {
			return err
		}
		if predecessor.Member == "" {
			if _, exists := symbols[predecessor.Symbol]; !exists {
				return fmt.Errorf("migration %q references an absent predecessor", migration.ID)
			}
		}
	}
	for _, operation := range migration.Operations {
		if operation.Kind != "create-table" && operation.Kind != "add-column" && operation.Kind != "create-index" {
			return fmt.Errorf("migration %q has invalid operation", migration.ID)
		}
		if err := validateDataRef(operation.Table, imports); err != nil {
			return err
		}
		if operation.Table.Member == "" {
			if _, exists := tables[operation.Table.Symbol]; !exists {
				return fmt.Errorf("migration %q references an absent table", migration.ID)
			}
		}
		if operation.Kind == "add-column" && (!validDataSymbol(operation.Column.ID) || !validDataScalar(operation.Column.Type)) || operation.Kind == "create-index" && (!validDataSymbol(operation.Index.ID) || len(operation.Index.Columns) == 0) {
			return fmt.Errorf("migration %q has invalid operation payload", migration.ID)
		}
	}
	return nil
}

func validateDataColumnRef(ref dataColumnRef, imports map[string]struct{}, introduced map[string]struct{}, base dataRef) error {
	if ref.Table.Member == "" && ref.Table.Symbol == "" {
		ref.Table = base
	}
	if err := validateDataRef(ref.Table, imports); err != nil {
		return err
	}
	if _, exists := introduced[dataRefKey(ref.Table)]; !exists || !validDataSymbol(ref.Column) {
		return fmt.Errorf("invalid column reference")
	}
	return nil
}

func validateDataPredicate(predicate dataPredicate, imports map[string]struct{}, parameters map[string]string, introduced map[string]struct{}, base dataRef, depth, maxNesting int) error {
	if predicate.Kind == "" {
		return nil
	}
	if depth > maxNesting || depth > dataMaximumNesting {
		return fmt.Errorf("predicate nesting limit exceeded")
	}
	switch predicate.Kind {
	case "equal", "not-equal", "less", "less-equal", "greater", "greater-equal":
		if err := validateDataColumnRef(predicate.Left, imports, introduced, base); err != nil {
			return err
		}
		columnSet := predicate.Right.Table.Member != "" || predicate.Right.Table.Symbol != "" || predicate.Right.Column != ""
		if columnSet == predicate.Value.Set {
			return fmt.Errorf("comparison requires one right operand")
		}
		if columnSet {
			return validateDataColumnRef(predicate.Right, imports, introduced, base)
		}
		return validateDataValue(predicate.Value, true, parameters)
	case "is-null", "is-not-null":
		if err := validateDataColumnRef(predicate.Left, imports, introduced, base); err != nil {
			return err
		}
		if predicate.Right.Table.Member != "" || predicate.Right.Table.Symbol != "" || predicate.Right.Column != "" || predicate.Value.Set {
			return fmt.Errorf("null predicate carries an operand")
		}
	case "and", "or":
		if len(predicate.Items) < 2 {
			return fmt.Errorf("logical predicate needs two items")
		}
		for _, item := range predicate.Items {
			if err := validateDataPredicate(item, imports, parameters, introduced, base, depth+1, maxNesting); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported predicate kind %q", predicate.Kind)
	}
	return nil
}

func validateDataValue(value dataValue, queryValue bool, parameters map[string]string) error {
	if !value.Set {
		if value.Parameter != "" {
			return fmt.Errorf("unset value carries a parameter")
		}
		return nil
	}
	if !validDataScalar(value.Kind) || value.Kind == "uuid" || value.Kind == "timestamp" {
		return fmt.Errorf("unsupported value kind %q", value.Kind)
	}
	if value.Parameter != "" {
		if !queryValue || !validDataSymbol(value.Parameter) || parameters[value.Parameter] != value.Kind || value.String != "" || value.Bytes != "" || value.Bool || value.Int64 != 0 || value.Uint64 != 0 || value.Float64 != 0 {
			return fmt.Errorf("invalid parameter value")
		}
		return nil
	}
	if !utf8.ValidString(value.String) || (queryValue && value.Kind == "") {
		return fmt.Errorf("invalid literal value")
	}
	return nil
}

func validateDataRef(ref dataRef, imports map[string]struct{}) error {
	if !validDataSymbol(ref.Symbol) {
		return fmt.Errorf("invalid reference symbol %q", ref.Symbol)
	}
	if ref.Member != "" {
		if !validDataMemberID(ref.Member) {
			return fmt.Errorf("invalid reference member %q", ref.Member)
		}
		if _, exists := imports[ref.Member]; !exists {
			return fmt.Errorf("reference member %q is not imported", ref.Member)
		}
	}
	return nil
}

func digestDataModel(model dataModel) (string, error) {
	data, err := json.Marshal(model)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("plystra.data.logical-model/v1\x00"), data...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeDataJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON")
		}
		return err
	}
	return nil
}

func requireDataFields(data []byte, names ...string) error {
	var object map[string]json.RawMessage
	if err := decodeDataJSON(data, &object); err != nil || object == nil {
		return fmt.Errorf("root is not an object")
	}
	for _, name := range names {
		value, exists := object[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("required field %q is absent", name)
		}
	}
	return nil
}

func validDataDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validDataMemberID(value string) bool {
	name, version, ok := strings.Cut(value, "/v")
	if !ok || name == "" || version == "" || strings.HasPrefix(version, "0") {
		return false
	}
	if _, err := strconv.ParseUint(version, 10, 64); err != nil {
		return false
	}
	for _, segment := range strings.Split(name, ".") {
		if segment == "" || segment[0] < 'a' || segment[0] > 'z' {
			return false
		}
		for _, character := range segment[1:] {
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return strings.Count(name, ".") >= 1
}

func validDataNamespace(value string) bool {
	if value == "" || len(value) > 63 || !utf8.ValidString(value) || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validDataSymbol(value string) bool {
	if value == "" || len(value) > dataMaximumSymbolBytes || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, part := range strings.Split(value, "-") {
		if part == "" || (part[0] < 'a' || part[0] > 'z') && (part[0] < '0' || part[0] > '9') {
			return false
		}
		for _, character := range part[1:] {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func validDataScalar(value string) bool {
	switch value {
	case "bool", "bytes", "float64", "int64", "string", "timestamp", "uint64", "uuid":
		return true
	default:
		return false
	}
}

func validDataOperation(value string) bool {
	return value == "select" || value == "insert" || value == "update" || value == "delete"
}

func sortedUniqueStrings(values []string) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i] < values[j] }) && uniqueStrings(values)
}

func uniqueStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			return false
		}
	}
	return true
}

func addDataSymbol(symbols map[string]string, value, kind string) error {
	if !validDataSymbol(value) {
		return fmt.Errorf("invalid %s symbol %q", kind, value)
	}
	if previous, exists := symbols[value]; exists {
		return fmt.Errorf("symbol %q is both %s and %s", value, previous, kind)
	}
	symbols[value] = kind
	return nil
}

func dataRefKey(ref dataRef) string { return ref.Member + "\x00" + ref.Symbol }

func sortedDataTables(values []dataTable) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i].ID < values[j].ID }) && uniqueDataIDs(values, func(value dataTable) string { return value.ID })
}
func sortedDataQueries(values []dataQuery) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i].ID < values[j].ID }) && uniqueDataIDs(values, func(value dataQuery) string { return value.ID })
}
func sortedDataPlans(values []dataPlan) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i].ID < values[j].ID }) && uniqueDataIDs(values, func(value dataPlan) string { return value.ID })
}
func sortedDataMigrations(values []dataMigration) bool {
	if !sort.SliceIsSorted(values, func(i, j int) bool {
		return values[i].Version < values[j].Version || values[i].Version == values[j].Version && values[i].ID < values[j].ID
	}) {
		return false
	}
	for i := 1; i < len(values); i++ {
		if values[i-1].ID == values[i].ID {
			return false
		}
	}
	return true
}
func sortedDataColumns(values []dataColumn) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i].ID < values[j].ID }) && uniqueDataIDs(values, func(value dataColumn) string { return value.ID })
}
func sortedDataConstraints(values []dataConstraint) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i].ID < values[j].ID }) && uniqueDataIDs(values, func(value dataConstraint) string { return value.ID })
}
func sortedDataIndexes(values []dataIndex) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i].ID < values[j].ID }) && uniqueDataIDs(values, func(value dataIndex) string { return value.ID })
}
func sortedDataParameters(values []dataParameter) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i].ID < values[j].ID }) && uniqueDataIDs(values, func(value dataParameter) string { return value.ID })
}
func sortedDataAssignments(values []dataAssignment) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return values[i].Column < values[j].Column }) && uniqueDataIDs(values, func(value dataAssignment) string { return value.Column })
}
func sortedDataRefs(values []dataRef) bool {
	return sort.SliceIsSorted(values, func(i, j int) bool { return dataRefKey(values[i]) < dataRefKey(values[j]) }) && sortUniqueDataRefs(values)
}

func uniqueDataIDs[T any](values []T, key func(T) string) bool {
	for i := 1; i < len(values); i++ {
		if key(values[i-1]) == key(values[i]) {
			return false
		}
	}
	return true
}

func sortUniqueDataRefs(values []dataRef) bool {
	for i := 1; i < len(values); i++ {
		if dataRefKey(values[i-1]) == dataRefKey(values[i]) {
			return false
		}
	}
	return true
}

func dataModelNodeCount(model dataModel) int {
	count := 1 + len(model.Imports) + len(model.Tables) + len(model.Queries) + len(model.Plans) + len(model.Migrations)
	for _, table := range model.Tables {
		count += len(table.Columns) + len(table.Constraints) + len(table.Indexes)
	}
	for _, query := range model.Queries {
		count += len(query.Joins)*4 + len(query.Parameters) + len(query.Assignments) + len(query.Conflict.UpdateColumns) + len(query.Projection) + len(query.Returning) + len(query.Results) + len(query.Ordering) + dataPredicateNodeCount(query.Predicate)
	}
	for _, plan := range model.Plans {
		count += len(plan.Statements)
	}
	for _, migration := range model.Migrations {
		count += len(migration.Predecessors) + len(migration.Operations)
	}
	return count
}

func dataModelHasLongSymbol(model dataModel, maximum int) bool {
	tooLong := func(value string) bool { return len(value) > maximum }
	for _, table := range model.Tables {
		if tooLong(table.ID) {
			return true
		}
		for _, column := range table.Columns {
			if tooLong(column.ID) {
				return true
			}
		}
		for _, constraint := range table.Constraints {
			if tooLong(constraint.ID) {
				return true
			}
			for _, column := range constraint.Columns {
				if tooLong(column) {
					return true
				}
			}
		}
		for _, index := range table.Indexes {
			if tooLong(index.ID) {
				return true
			}
			for _, column := range index.Columns {
				if tooLong(column) {
					return true
				}
			}
		}
	}
	for _, query := range model.Queries {
		if tooLong(query.ID) || tooLong(query.Table.Symbol) || tooLong(query.Conflict.Constraint) {
			return true
		}
		for _, parameter := range query.Parameters {
			if tooLong(parameter.ID) {
				return true
			}
		}
		for _, assignment := range query.Assignments {
			if tooLong(assignment.Column) {
				return true
			}
		}
		for _, column := range query.Conflict.UpdateColumns {
			if tooLong(column) {
				return true
			}
		}
		for _, projection := range append(append([]dataProjection(nil), query.Projection...), query.Returning...) {
			if tooLong(projection.Column) || tooLong(projection.Alias) || tooLong(projection.Table.Symbol) {
				return true
			}
		}
		for _, result := range query.Results {
			if tooLong(result.ID) || tooLong(result.Source) {
				return true
			}
		}
		for _, ordering := range query.Ordering {
			if tooLong(ordering.Column) || tooLong(ordering.Table.Symbol) {
				return true
			}
		}
	}
	for _, plan := range model.Plans {
		if tooLong(plan.ID) {
			return true
		}
		for _, statement := range plan.Statements {
			if tooLong(statement.ID) || tooLong(statement.Query.Symbol) {
				return true
			}
		}
	}
	for _, migration := range model.Migrations {
		if tooLong(migration.ID) {
			return true
		}
		for _, operation := range migration.Operations {
			if tooLong(operation.Table.Symbol) || tooLong(operation.Column.ID) || tooLong(operation.Index.ID) {
				return true
			}
		}
	}
	return false
}

func dataPredicateNodeCount(predicate dataPredicate) int {
	count := 0
	if predicate.Kind != "" {
		count++
	}
	for _, item := range predicate.Items {
		count += 1 + dataPredicateNodeCount(item)
	}
	return count
}
