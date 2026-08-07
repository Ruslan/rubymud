package mapper

import (
	"encoding/json"
	"fmt"

	"rubymud/go/internal/storage"
)

// ---------------------------------------------------------------------------
// The JSON <-> ProfileSpec boundary, and the compile gate the STORE enforces.
//
// profile.go is deliberately free of I/O; this file is the one place in the
// package that knows a spec can arrive as bytes and that those bytes have a home
// in SQLite. Everything here is off the incoming-line hot path: a spec is parsed
// and compiled when a session (re)builds its accumulator, never per line.
//
// WHY THE VALIDATOR IS REGISTERED RATHER THAN CALLED DIRECTLY:
// `mapper` imports `storage` (Index is built from []storage.Room), so `storage`
// can never import `mapper` — the compile check cannot be a direct call from the
// store. The store instead exposes a registration slot and FAILS CLOSED when it
// is empty, and this package fills it in init(). That makes the invariant
// structural: any binary that can *use* a room-format profile necessarily links
// this package, so the gate is necessarily installed. There is no wiring line
// anyone can forget in main().
// ---------------------------------------------------------------------------

func init() {
	storage.RegisterMapperSpecValidator(ValidateSpecJSON)
}

// ParseSpecJSON unmarshals a ProfileSpec from its JSON form WITHOUT compiling
// it. Callers that need a usable profile should use CompileJSON; this exists for
// the write path, which has to overwrite spec.Name from the row name before
// compiling (see the web layer).
//
// Unknown fields are ignored rather than rejected: a spec written by a newer
// build must degrade to "the fields this build understands" instead of making
// the session unparseable.
func ParseSpecJSON(data []byte) (ProfileSpec, error) {
	var spec ProfileSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return ProfileSpec{}, fmt.Errorf("mapper: invalid profile spec JSON: %w", err)
	}
	return spec, nil
}

// MarshalSpec renders a spec back to its canonical JSON form — the form the DB
// stores. Round-tripping a spec through Compile().Spec() and this function is
// what guarantees the stored bytes are exactly what the compiler accepted, with
// no unknown/garbage keys carried along from the request body.
func MarshalSpec(spec ProfileSpec) ([]byte, error) {
	return json.Marshal(spec)
}

// CompileJSON is the SINGLE load path from stored bytes to a usable Profile:
// unmarshal, then Compile. The session's profile resolution and the store's
// validation gate both go through it, so a spec that a write accepted is by
// construction a spec a session can load.
func CompileJSON(specJSON string) (Profile, error) {
	spec, err := ParseSpecJSON([]byte(specJSON))
	if err != nil {
		return Profile{}, err
	}
	return Compile(spec)
}

// ValidateSpecJSON is CompileJSON reduced to its error — the compile-only check
// behind the store's write gate and the REST validate endpoint. The error text
// is Compile's verbatim message, which is what the settings form shows inline;
// do not wrap it further downstream or the user stops seeing which rule failed.
func ValidateSpecJSON(specJSON string) error {
	_, err := CompileJSON(specJSON)
	return err
}
