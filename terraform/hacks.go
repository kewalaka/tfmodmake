package terraform

import "github.com/getkin/kin-openapi/openapi3"

// Hack represents a named workaround for a known API spec deficiency.
// Hacks are opt-in via the --hack CLI flag to keep their effects visible
// and confined to resources that need them.
type Hack string

const (
	// HackSuppressInlineID treats "id" fields as computed (non-writable) when
	// the parent object also has a "name" sibling property. This works around
	// Azure specs that fail to mark inline sub-resource ids as readOnly;
	// the id is derived from the parent resource path + name at deploy time.
	HackSuppressInlineID Hack = "suppress-inline-id"
)

// HackSet is a set of enabled hacks.
type HackSet map[Hack]struct{}

// Has reports whether the hack is enabled in this set.
func (hs HackSet) Has(h Hack) bool {
	_, ok := hs[h]
	return ok
}

// ParseHacks converts a list of hack name strings into a HackSet.
// Unknown hack names are returned as errors.
func ParseHacks(names []string) (HackSet, error) {
	known := map[string]Hack{
		string(HackSuppressInlineID): HackSuppressInlineID,
	}

	hs := make(HackSet, len(names))
	for _, name := range names {
		h, ok := known[name]
		if !ok {
			return nil, &UnknownHackError{Name: name, Known: knownHackNames(known)}
		}
		hs[h] = struct{}{}
	}
	return hs, nil
}

// knownHackNames returns sorted known hack names for error messages.
func knownHackNames(known map[string]Hack) []string {
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	return names
}

// UnknownHackError is returned when an unrecognized hack name is provided.
type UnknownHackError struct {
	Name  string
	Known []string
}

func (e *UnknownHackError) Error() string {
	return "unknown hack: " + e.Name
}

// isWritablePropertyInContext checks writability considering parent context and enabled hacks.
// When HackSuppressInlineID is enabled, "id" fields with a "name" sibling are treated as
// computed (non-writable), working around Azure specs that don't mark inline definition ids
// as readOnly.
func isWritablePropertyInContext(propName string, propSchema *openapi3.Schema, siblingProps map[string]*openapi3.SchemaRef, hacks HackSet) bool {
	if !isWritableProperty(propSchema) {
		return false
	}

	if hacks.Has(HackSuppressInlineID) && propName == "id" && siblingProps != nil {
		if _, hasName := siblingProps["name"]; hasName {
			return false
		}
	}

	return true
}
