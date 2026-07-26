package protobufwiremap

import (
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/protobufmodel"
)

// InterfaceProjection is one active canonical Interface's validated procedure
// and message wire history. It contains no Implementation, module,
// configuration, or runtime identity.
type InterfaceProjection struct {
	id                 string
	contractDigest     string
	protobufPackage    string
	service            string
	method             string
	procedure          string
	requestMessage     string
	responseMessage    string
	messageProjections []MessageProjection
}

// ID returns the exact canonical Interface ID.
func (p InterfaceProjection) ID() string { return p.id }

// ContractDigest returns the exact normalized authored Interface digest.
func (p InterfaceProjection) ContractDigest() string { return p.contractDigest }

// ProtobufPackage returns the deterministic generated Protobuf package.
func (p InterfaceProjection) ProtobufPackage() string { return p.protobufPackage }

// Service returns the unqualified stable Protobuf service name.
func (p InterfaceProjection) Service() string { return p.service }

// Method returns the stable unary Protobuf method name.
func (p InterfaceProjection) Method() string { return p.method }

// Procedure returns the exact stable Connect HTTP procedure path.
func (p InterfaceProjection) Procedure() string { return p.procedure }

// RequestMessage returns the unqualified generated request message name.
func (p InterfaceProjection) RequestMessage() string { return p.requestMessage }

// ResponseMessage returns the unqualified generated response message name.
func (p InterfaceProjection) ResponseMessage() string { return p.responseMessage }

// Messages returns generated-name-sorted active message projections.
func (p InterfaceProjection) Messages() []MessageProjection {
	result := make([]MessageProjection, len(p.messageProjections))
	for index, message := range p.messageProjections {
		result[index] = cloneMessageProjection(message)
	}
	return result
}

// ActiveInterfaces returns exact-ID-sorted active Interface wire projections
// with defensive storage. Inactive Interface and message history remains only
// in CanonicalJSON.
func (m Map) ActiveInterfaces() []InterfaceProjection {
	result := make([]InterfaceProjection, len(m.activeInterfaces))
	for index, value := range m.activeInterfaces {
		result[index] = cloneInterfaceProjection(value)
	}
	return result
}

type interfaceHistory struct {
	Active          bool                               `json:"active"`
	ContractDigest  string                             `json:"contract_digest"`
	ProtobufPackage string                             `json:"protobuf_package"`
	Service         string                             `json:"service"`
	Method          string                             `json:"method"`
	Procedure       string                             `json:"procedure"`
	RequestMessage  string                             `json:"request_message"`
	ResponseMessage string                             `json:"response_message"`
	Messages        map[string]interfaceMessageHistory `json:"messages"`
}

type interfaceMessageHistory struct {
	Active          bool                                `json:"active"`
	GoName          string                              `json:"go_name"`
	Fields          map[string]interfaceFieldAssignment `json:"fields"`
	ReservedNumbers []int                               `json:"reserved_numbers"`
	ReservedNames   []string                            `json:"reserved_names"`
}

type interfaceFieldAssignment struct {
	GoName string `json:"go_name"`
	Number int    `json:"number"`
}

type activeInterface struct {
	ContractDigest  string                            `json:"contract_digest"`
	ProtobufPackage string                            `json:"protobuf_package"`
	Service         string                            `json:"service"`
	Method          string                            `json:"method"`
	Procedure       string                            `json:"procedure"`
	RequestMessage  string                            `json:"request_message"`
	ResponseMessage string                            `json:"response_message"`
	Messages        map[string]activeInterfaceMessage `json:"messages"`
}

type activeInterfaceMessage struct {
	GoName          string                              `json:"go_name"`
	Fields          map[string]interfaceFieldAssignment `json:"fields"`
	ReservedNumbers []int                               `json:"reserved_numbers"`
	ReservedNames   []string                            `json:"reserved_names"`
}

func reconcileInterfaces(current map[string]interfaceHistory, model protobufmodel.InterfaceModel) error {
	if current == nil {
		return errors.New("interfaces history is absent")
	}
	active := make(map[string]struct{}, len(model.Operations()))
	for _, operation := range model.Operations() {
		active[operation.ID().String()] = struct{}{}
	}
	for identifier, record := range current {
		record.Active = false
		for name, message := range record.Messages {
			message.Active = false
			record.Messages[name] = message
		}
		current[identifier] = record
	}

	for _, operation := range model.HistoryOperations() {
		identifier := operation.ID().String()
		_, selected := active[identifier]
		identity := operation.Identity()
		requestMessage, err := unqualifiedMessage(identity.Package(), identity.RequestType())
		if err != nil {
			return fmt.Errorf("Interface %s request identity: %v", identifier, err)
		}
		responseMessage, err := unqualifiedMessage(identity.Package(), identity.ResponseType())
		if err != nil {
			return fmt.Errorf("Interface %s response identity: %v", identifier, err)
		}

		record, exists := current[identifier]
		if !exists {
			record = interfaceHistory{
				ProtobufPackage: identity.Package(),
				Service:         identity.Service(),
				Method:          identity.Method(),
				Procedure:       identity.Procedure(),
				RequestMessage:  requestMessage,
				ResponseMessage: responseMessage,
				Messages:        make(map[string]interfaceMessageHistory),
			}
		}
		if record.ProtobufPackage != identity.Package() ||
			record.Service != identity.Service() ||
			record.Method != identity.Method() ||
			record.Procedure != identity.Procedure() ||
			record.RequestMessage != requestMessage ||
			record.ResponseMessage != responseMessage {
			return fmt.Errorf(
				"Interface %s procedure or message identity changed from %s/%s/%s/%s/%s/%s to %s/%s/%s/%s/%s/%s",
				identifier,
				record.ProtobufPackage,
				record.Service,
				record.Method,
				record.Procedure,
				record.RequestMessage,
				record.ResponseMessage,
				identity.Package(),
				identity.Service(),
				identity.Method(),
				identity.Procedure(),
				requestMessage,
				responseMessage,
			)
		}
		for name, message := range record.Messages {
			message.Active = false
			record.Messages[name] = message
		}

		for _, message := range operation.Messages() {
			name := message.ProtobufName()
			history, exists := record.Messages[name]
			if !exists {
				history = interfaceMessageHistory{
					GoName:          message.GoName(),
					Fields:          make(map[string]interfaceFieldAssignment),
					ReservedNumbers: []int{},
					ReservedNames:   []string{},
				}
			}
			history.GoName = message.GoName()
			history, err = reconcileInterfaceMessage(history, message.Fields())
			if err != nil {
				return fmt.Errorf("Interface %s message %s: %v", identifier, name, err)
			}
			history.Active = selected
			record.Messages[name] = history
		}

		request, requestExists := record.Messages[record.RequestMessage]
		response, responseExists := record.Messages[record.ResponseMessage]
		if !requestExists || !responseExists {
			return fmt.Errorf("Interface %s request or response message is absent from the current projection", identifier)
		}
		if selected && (!request.Active || !response.Active) {
			return fmt.Errorf("active Interface %s request or response message is absent from the current projection", identifier)
		}
		record.Active = selected
		record.ContractDigest = operation.ContractDigest()
		current[identifier] = record
	}
	return nil
}

func reconcileInterfaceMessage(
	previous interfaceMessageHistory,
	fields []protobufmodel.InterfaceField,
) (interfaceMessageHistory, error) {
	result := cloneInterfaceMessageHistory(previous)
	if len(fields) > maximumFields {
		return interfaceMessageHistory{}, fmt.Errorf("%d fields exceeds maximum %d", len(fields), maximumFields)
	}

	ordered := append([]protobufmodel.InterfaceField(nil), fields...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].ProtobufName() < ordered[right].ProtobufName()
	})
	current := make(map[string]struct{}, len(ordered))
	usedNumbers := make(map[int]string, len(result.Fields)+len(result.ReservedNumbers))
	for name, assignment := range result.Fields {
		usedNumbers[assignment.Number] = name
	}
	for _, number := range result.ReservedNumbers {
		usedNumbers[number] = "reserved"
	}
	reservedNames := make(map[string]struct{}, len(result.ReservedNames))
	for _, name := range result.ReservedNames {
		reservedNames[name] = struct{}{}
	}

	for _, field := range ordered {
		name := field.ProtobufName()
		if !validFieldName(name) || !validInterfaceGoName(field.GoName()) {
			return interfaceMessageHistory{}, fmt.Errorf("field %q/%q has no valid deterministic identity", field.GoName(), name)
		}
		if _, duplicate := current[name]; duplicate {
			return interfaceMessageHistory{}, fmt.Errorf("field %q is duplicated", name)
		}
		current[name] = struct{}{}
		number := int(field.Number())
		if !permittedNumber(number) {
			return interfaceMessageHistory{}, fmt.Errorf("field %q has invalid authored number %d", name, field.Number())
		}

		if assignment, exists := result.Fields[name]; exists {
			if assignment.Number != number {
				return interfaceMessageHistory{}, fmt.Errorf(
					"field %q authored number changed from %d to %d",
					name,
					assignment.Number,
					number,
				)
			}
			assignment.GoName = field.GoName()
			result.Fields[name] = assignment
			continue
		}
		if _, reserved := reservedNames[name]; reserved {
			return interfaceMessageHistory{}, fmt.Errorf("field %q reuses a permanently reserved Protobuf name", name)
		}
		if owner, occupied := usedNumbers[number]; occupied {
			return interfaceMessageHistory{}, fmt.Errorf("field %q authored number %d is permanently occupied by %s", name, number, owner)
		}
		result.Fields[name] = interfaceFieldAssignment{GoName: field.GoName(), Number: number}
		usedNumbers[number] = name
	}

	for name, assignment := range result.Fields {
		if _, retained := current[name]; retained {
			continue
		}
		delete(result.Fields, name)
		result.ReservedNumbers = append(result.ReservedNumbers, assignment.Number)
		result.ReservedNames = append(result.ReservedNames, name)
	}
	sort.Ints(result.ReservedNumbers)
	sort.Strings(result.ReservedNames)
	return result, nil
}

func validateInterfaceHistories(values map[string]interfaceHistory) error {
	if values == nil || len(values) > maximumInterfaces {
		return fmt.Errorf("interfaces must be an object with at most %d entries", maximumInterfaces)
	}
	for identifier, record := range values {
		if identifier == "" || len(identifier) > 1024 || !utf8.ValidString(identifier) {
			return fmt.Errorf("interfaces contains invalid identity %q", identifier)
		}
		identity, err := canonicalInterfaceIdentity(identifier)
		if err != nil {
			return fmt.Errorf("invalid Interface identity %q: %v", identifier, err)
		}
		requestMessage, err := unqualifiedMessage(identity.Package(), identity.RequestType())
		if err != nil {
			return fmt.Errorf("invalid Interface %s request identity: %v", identifier, err)
		}
		responseMessage, err := unqualifiedMessage(identity.Package(), identity.ResponseType())
		if err != nil {
			return fmt.Errorf("invalid Interface %s response identity: %v", identifier, err)
		}
		if record.ProtobufPackage != identity.Package() ||
			record.Service != identity.Service() ||
			record.Method != identity.Method() ||
			record.Procedure != identity.Procedure() ||
			record.RequestMessage != requestMessage ||
			record.ResponseMessage != responseMessage {
			return fmt.Errorf(
				"invalid Interface %s procedure and message identities: must be %s/%s/%s/%s/%s/%s",
				identifier,
				identity.Package(),
				identity.Service(),
				identity.Method(),
				identity.Procedure(),
				requestMessage,
				responseMessage,
			)
		}
		if !validDigest(record.ContractDigest) {
			return fmt.Errorf("invalid Interface %s contract_digest", identifier)
		}
		if record.Messages == nil || len(record.Messages) < 2 || len(record.Messages) > maximumFields {
			return fmt.Errorf("invalid Interface %s messages: must contain between 2 and %d entries", identifier, maximumFields)
		}
		request, requestExists := record.Messages[requestMessage]
		response, responseExists := record.Messages[responseMessage]
		if !requestExists || !responseExists {
			return fmt.Errorf("invalid Interface %s messages: request and response history is required", identifier)
		}
		activeMessages := 0
		for name, message := range record.Messages {
			if !validMessageName(name) {
				return fmt.Errorf("invalid Interface %s message identity %q", identifier, name)
			}
			if err := validateInterfaceMessageHistory(message); err != nil {
				return fmt.Errorf("invalid Interface %s message %s: %v", identifier, name, err)
			}
			if message.Active {
				activeMessages++
			}
		}
		if record.Active {
			if !request.Active || !response.Active || activeMessages < 2 {
				return fmt.Errorf("invalid active Interface %s: request and response messages must be active", identifier)
			}
		} else if activeMessages != 0 {
			return fmt.Errorf("invalid inactive Interface %s: %d messages remain active", identifier, activeMessages)
		}
	}
	return nil
}

func validateInterfaceMessageHistory(value interfaceMessageHistory) error {
	if !validInterfaceGoName(value.GoName) {
		return fmt.Errorf("Go message name %q is invalid", value.GoName)
	}
	if value.Fields == nil || len(value.Fields) > maximumFields || value.ReservedNumbers == nil || value.ReservedNames == nil {
		return fmt.Errorf("fields and reservations must be arrays or objects bounded to %d entries", maximumFields)
	}
	usedNumbers := make(map[int]string, len(value.Fields)+len(value.ReservedNumbers))
	usedNames := make(map[string]string, len(value.Fields)+len(value.ReservedNames))
	usedGoNames := make(map[string]string, len(value.Fields))
	for name, assignment := range value.Fields {
		if !validFieldName(name) || !validInterfaceGoName(assignment.GoName) {
			return fmt.Errorf("field %q/%q has an invalid identity", assignment.GoName, name)
		}
		if !permittedNumber(assignment.Number) {
			return fmt.Errorf("field %s has invalid number %d", name, assignment.Number)
		}
		if previous, duplicate := usedNumbers[assignment.Number]; duplicate {
			return fmt.Errorf("fields %s and %s duplicate number %d", previous, name, assignment.Number)
		}
		if previous, duplicate := usedGoNames[assignment.GoName]; duplicate {
			return fmt.Errorf("fields %s and %s duplicate Go name %s", previous, name, assignment.GoName)
		}
		usedNumbers[assignment.Number] = name
		usedNames[name] = name
		usedGoNames[assignment.GoName] = name
	}
	for index, number := range value.ReservedNumbers {
		if !permittedNumber(number) {
			return fmt.Errorf("reserved number %d is invalid", number)
		}
		if index != 0 && value.ReservedNumbers[index-1] >= number {
			return errors.New("reserved_numbers must be unique and ascending")
		}
		if field, collision := usedNumbers[number]; collision {
			return fmt.Errorf("reserved number %d collides with field %s", number, field)
		}
		usedNumbers[number] = "reserved"
	}
	for index, name := range value.ReservedNames {
		if !validFieldName(name) {
			return fmt.Errorf("reserved name %q is invalid", name)
		}
		if index != 0 && value.ReservedNames[index-1] >= name {
			return errors.New("reserved_names must be unique and lexically sorted")
		}
		if field, collision := usedNames[name]; collision {
			return fmt.Errorf("reserved name %s collides with field %s", name, field)
		}
		usedNames[name] = "reserved"
	}
	return nil
}

func activeInterfaceProjections(values map[string]interfaceHistory) []InterfaceProjection {
	identifiers := make([]string, 0, len(values))
	for identifier, record := range values {
		if record.Active {
			identifiers = append(identifiers, identifier)
		}
	}
	sort.Strings(identifiers)
	result := make([]InterfaceProjection, len(identifiers))
	for index, identifier := range identifiers {
		record := values[identifier]
		messageNames := make([]string, 0, len(record.Messages))
		for name, message := range record.Messages {
			if message.Active {
				messageNames = append(messageNames, name)
			}
		}
		sort.Strings(messageNames)
		messages := make([]MessageProjection, len(messageNames))
		for messageIndex, name := range messageNames {
			message := record.Messages[name]
			fieldNames := make([]string, 0, len(message.Fields))
			for fieldName := range message.Fields {
				fieldNames = append(fieldNames, fieldName)
			}
			sort.Strings(fieldNames)
			fields := make([]FieldProjection, len(fieldNames))
			for fieldIndex, fieldName := range fieldNames {
				assignment := message.Fields[fieldName]
				fields[fieldIndex] = FieldProjection{
					canonicalName: assignment.GoName,
					name:          fieldName,
					number:        assignment.Number,
				}
			}
			messages[messageIndex] = MessageProjection{
				canonicalName:   message.GoName,
				name:            name,
				fields:          fields,
				enums:           []EnumProjection{},
				reservedNumbers: append([]int(nil), message.ReservedNumbers...),
				reservedNames:   append([]string(nil), message.ReservedNames...),
			}
		}
		result[index] = InterfaceProjection{
			id:                 identifier,
			contractDigest:     record.ContractDigest,
			protobufPackage:    record.ProtobufPackage,
			service:            record.Service,
			method:             record.Method,
			procedure:          record.Procedure,
			requestMessage:     record.RequestMessage,
			responseMessage:    record.ResponseMessage,
			messageProjections: messages,
		}
	}
	return result
}

func activeInterfaceHistory(values map[string]interfaceHistory) map[string]activeInterface {
	result := make(map[string]activeInterface)
	for identifier, record := range values {
		if !record.Active {
			continue
		}
		messages := make(map[string]activeInterfaceMessage)
		for name, message := range record.Messages {
			if !message.Active {
				continue
			}
			fields := make(map[string]interfaceFieldAssignment, len(message.Fields))
			for fieldName, assignment := range message.Fields {
				fields[fieldName] = assignment
			}
			messages[name] = activeInterfaceMessage{
				GoName:          message.GoName,
				Fields:          fields,
				ReservedNumbers: append([]int(nil), message.ReservedNumbers...),
				ReservedNames:   append([]string(nil), message.ReservedNames...),
			}
		}
		result[identifier] = activeInterface{
			ContractDigest:  record.ContractDigest,
			ProtobufPackage: record.ProtobufPackage,
			Service:         record.Service,
			Method:          record.Method,
			Procedure:       record.Procedure,
			RequestMessage:  record.RequestMessage,
			ResponseMessage: record.ResponseMessage,
			Messages:        messages,
		}
	}
	return result
}

func cloneInterfaceHistories(values map[string]interfaceHistory) map[string]interfaceHistory {
	result := make(map[string]interfaceHistory, len(values))
	for identifier, record := range values {
		messages := make(map[string]interfaceMessageHistory, len(record.Messages))
		for name, message := range record.Messages {
			messages[name] = cloneInterfaceMessageHistory(message)
		}
		record.Messages = messages
		result[identifier] = record
	}
	return result
}

func cloneInterfaceMessageHistory(value interfaceMessageHistory) interfaceMessageHistory {
	fields := make(map[string]interfaceFieldAssignment, len(value.Fields))
	for name, assignment := range value.Fields {
		fields[name] = assignment
	}
	return interfaceMessageHistory{
		Active:          value.Active,
		GoName:          value.GoName,
		Fields:          fields,
		ReservedNumbers: append([]int{}, value.ReservedNumbers...),
		ReservedNames:   append([]string{}, value.ReservedNames...),
	}
}

func cloneInterfaceProjection(value InterfaceProjection) InterfaceProjection {
	result := value
	result.messageProjections = make([]MessageProjection, len(value.messageProjections))
	for index, message := range value.messageProjections {
		result.messageProjections[index] = cloneMessageProjection(message)
	}
	return result
}

func validInterfaceGoName(value string) bool {
	return value != "" &&
		len(value) <= 512 &&
		utf8.ValidString(value) &&
		!hasControl(value) &&
		validMessageName(value)
}

func canonicalInterfaceIdentity(identifier string) (protobufidentity.Identity, error) {
	identities, err := protobufidentity.Build([]protobufidentity.Surface{{
		PublicID:    identifier,
		CanonicalID: identifier,
	}})
	if err != nil {
		return protobufidentity.Identity{}, err
	}
	values := identities.Identities()
	if len(values) != 1 {
		return protobufidentity.Identity{}, errors.New("canonical Interface procedure identity is absent")
	}
	return values[0], nil
}
